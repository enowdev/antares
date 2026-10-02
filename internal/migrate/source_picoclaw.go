package migrate

// PicoClaw (sipeed/picoclaw) — https://github.com/sipeed/picoclaw
//
// Format, verified against the project's own Go source at commit bbf6893
// (2026-10-03):
//
//   - Home: $PICOCLAW_HOME or ~/.picoclaw; config $PICOCLAW_CONFIG or
//     <home>/config.json (pkg/config/envkeys.go). Config "version" 3 is current;
//     older versions are migrated by PicoClaw on load (pkg/config/migration.go).
//     https://github.com/sipeed/picoclaw/blob/main/pkg/config/config.go
//   - Secrets live beside it in .security.yml (pkg/config/security.go):
//     model_list."<model_name>:<n>".api_keys and channel_list.<name>.settings
//     .<secret field>. Values are plaintext, file://<name> (relative to the
//     config dir) or enc://<base64> — AES-256-GCM, key =
//     HKDF-SHA256(HMAC-SHA256(SHA256(ssh key), passphrase), salt,
//     "picoclaw-credential-v1"), passphrase from PICOCLAW_KEY_PASSPHRASE, SSH key
//     from PICOCLAW_SSH_KEY_PATH or ~/.ssh/picoclaw_ed25519.key
//     (pkg/credential/credential.go). An enc:// value that cannot be decrypted
//     makes the provider needs_input.
//   - model_list[]: {model_name, provider, model, api_base, api_keys,
//     auth_method, fallbacks, enabled}; the protocol is "provider", else the
//     model's "<protocol>/" prefix, else openai (pkg/providers/factory_provider.go
//     ExtractProtocol); default endpoints from pkg/providers/provider_metadata.go.
//   - agents.defaults.{workspace, model_name, model_fallbacks}; agents.list[]
//     extra agents → roles.
//   - channel_list.<name>: {enabled, type, allow_from, settings} (v3,
//     pkg/config/config_channel.go); v1/v2 "channels" with flat fields are read
//     best-effort.
//   - tools.mcp.servers.<name>: {enabled, command, args, env, type, url, headers}.
//   - Workspace (default <home>/workspace): AGENT.md (front matter + prompt),
//     SOUL.md, USER.md, legacy AGENTS.md/IDENTITY.md (pkg/agent/definition.go),
//     memory/MEMORY.md + daily notes memory/YYYYMM/YYYYMMDD.md
//     (pkg/agent/memory.go), skills/, cron/jobs.json (nanobot's format,
//     pkg/cron/service.go), HEARTBEAT.md + heartbeat.{enabled, interval minutes}
//     (pkg/heartbeat/service.go).
//   - Running: <home>/.picoclaw.pid ({"pid":…}, pkg/pid/pidfile.go).
//
// Not verified: v0 configs with a top-level "providers" map are read like
// nanobot's (PicoClaw migrates them itself on first start).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type picoclawSource struct{}

func init() { Register(picoclawSource{}) }

func (picoclawSource) ID() string   { return "picoclaw" }
func (picoclawSource) Name() string { return "PicoClaw" }

func picoclawPaths(root string) (home, config string) {
	if root != "" {
		home = ExpandHome(root)
		if IsFile(home) {
			return filepath.Dir(home), home
		}
		return home, filepath.Join(home, "config.json")
	}
	home = os.Getenv("PICOCLAW_HOME")
	if home == "" {
		home = filepath.Join(HomeDir(), ".picoclaw")
	}
	home = ExpandHome(home)
	config = os.Getenv("PICOCLAW_CONFIG")
	if config == "" {
		config = filepath.Join(home, "config.json")
	}
	return home, ExpandHome(config)
}

func (s picoclawSource) Detect(ctx context.Context, root string) (Detection, error) {
	home, cfgPath := picoclawPaths(root)
	if !IsFile(cfgPath) {
		return Detection{}, ErrNotFound
	}
	det := Detection{Source: s.ID(), Name: s.Name(), Root: home}
	var head struct {
		BuildInfo struct {
			Version string `json:"version"`
		} `json:"build_info"`
	}
	_ = ReadJSON(cfgPath, &head)
	det.Version = head.BuildInfo.Version
	det.Running = nanobotPIDAlive(filepath.Join(home, ".picoclaw.pid")) ||
		RunningCheck([]string{`picoclaw( |-launcher).*(gateway|agent)`, `picoclaw-launcher`}, nil)
	items, _ := picoclawBuild(det, nil, false)
	det.Summary = Summarize(items)
	return det, nil
}

func (s picoclawSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	items, warnings := picoclawBuild(det, env, true)
	det.Summary = Summarize(items)
	return Plan{Detection: det, Items: items, Warnings: warnings}, nil
}

// picoclawBuild reads the install at det.Root; decrypt=false (Detect) leaves
// enc:// values unread.
func picoclawBuild(det Detection, env Env, decrypt bool) ([]Item, []string) {
	home, cfgPath := picoclawPaths(det.Root)
	if det.Root != "" && !IsFile(cfgPath) {
		cfgPath = filepath.Join(det.Root, "config.json")
	}
	cfgDir := filepath.Dir(cfgPath)
	var warnings []string
	cfg := map[string]any{}
	if err := ReadJSON(cfgPath, &cfg); err != nil {
		return nil, []string{"config.json could not be read: " + err.Error()}
	}
	sec := map[string]any{}
	if p := filepath.Join(cfgDir, ".security.yml"); IsFile(p) {
		if err := ReadYAML(p, &sec); err != nil {
			warnings = append(warnings, ".security.yml could not be read: "+err.Error())
		}
	}
	res := &picoclawResolver{dir: cfgDir, decrypt: decrypt}

	defaults := nanobotMap(nanobotMap(cfg, "agents"), "defaults")
	ws := nanobotStr(defaults, "workspace")
	if ws == "" {
		ws = filepath.Join(home, "workspace")
	}
	ws = ExpandHome(ws)

	var items []Item
	provs := newNanobotProviders(env)

	// model_list → providers. Entries without a key are PicoClaw's shipped
	// examples unless the default model points at one.
	defaultAlias := nanobotStr(defaults, "model_name")
	if defaultAlias == "" {
		defaultAlias = nanobotStr(defaults, "model")
	}
	type resolved struct{ id, model string }
	aliases := map[string]resolved{}
	secModels := nanobotMap(sec, "model_list")
	counts := map[string]int{}
	var oauth []Item
	for _, raw := range nanobotList(cfg, "model_list") {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		alias := nanobotStr(e, "model_name")
		idx := counts[alias]
		counts[alias]++
		protocol, modelID := picoclawProtocol(nanobotStr(e, "provider"), nanobotStr(e, "model"))
		rawKeys := nanobotStrings(nanobotGet(e, "api_keys"))
		if k := nanobotStr(e, "api_key"); k != "" {
			rawKeys = append(rawKeys, k)
		}
		if se := nanobotMap(secModels, fmt.Sprintf("%s:%d", alias, idx)); se != nil {
			rawKeys = append(nanobotStrings(nanobotGet(se, "api_keys")), rawKeys...)
			if k := nanobotStr(se, "api_key"); k != "" {
				rawKeys = append([]string{k}, rawKeys...)
			}
		}
		key, note := "", ""
		for _, rk := range rawKeys {
			if v, n := res.resolve(rk); v != "" {
				key, note = v, ""
				break
			} else if n != "" && note == "" {
				note = n
			}
		}
		base := nanobotStr(e, "api_base")
		auth := strings.ToLower(nanobotStr(e, "auth_method"))
		isDefault := alias != "" && alias == defaultAlias
		enabled := nanobotBool(e, "enabled", false)
		if key == "" && note == "" && !isDefault && !enabled {
			continue
		}
		switch {
		case auth == "oauth" || auth == "token" || picoclawOAuth[protocol]:
			oauth = append(oauth, OAuthProviderItem(Slug(protocol), nanobotTitle(protocol)+" ("+alias+")"))
			continue
		case picoclawCLI[protocol]:
			oauth = append(oauth, UnsupportedItem(CatProvider, Slug(protocol), alias, "a CLI-backed provider ("+protocol+"); Antares talks to APIs directly"))
			continue
		}
		vendor := protocol
		if v := picoclawVendorAlias[protocol]; v != "" {
			vendor = v
		}
		eff := base
		if eff == "" {
			eff = picoclawDefaultBase[protocol]
		}
		srcKind := "openai-compatible"
		if strings.Contains(protocol, "anthropic") {
			srcKind = "anthropic"
		}
		label := picoclawLabels[protocol]
		if label == "" {
			label = nanobotTitle(protocol)
		}
		p := nanobotIdentity(vendor, label, srcKind, eff)
		p.APIKey = key
		if h := nanobotStringMap(e, "custom_headers"); len(h) > 0 {
			p.Headers = h
		}
		p.Models = []string{modelID}
		id := provs.add(p, note)
		if alias != "" {
			if _, seen := aliases[alias]; !seen {
				aliases[alias] = resolved{id, modelID}
			}
		}
	}
	// v0 configs: a nanobot-style providers map.
	for name, raw := range nanobotMap(cfg, "providers") {
		pm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, note := res.resolve(nanobotStr(pm, "api_key"))
		base := nanobotStr(pm, "api_base")
		if key == "" && base == "" {
			continue
		}
		p := nanobotIdentity(name, nanobotTitle(name), "openai-compatible", base)
		p.APIKey = key
		provs.add(p, note)
	}
	items = append(items, oauth...)

	var modelItems []Item
	if r, ok := aliases[defaultAlias]; ok {
		mp := ModelPayload{Provider: r.id, Model: r.model}
		for _, fb := range nanobotStrings(nanobotGet(defaults, "model_fallbacks")) {
			if f, ok := aliases[fb]; ok {
				mp.Fallback = append(mp.Fallback, f.id+"/"+f.model)
			}
		}
		modelItems = append(modelItems, ModelItem(env, mp))
	}
	items = append(items, provs.items()...)
	items = append(items, modelItems...)

	// Persona. AGENT.md (new layout) carries the agent's prompt under YAML
	// front matter; AGENTS.md + IDENTITY.md are the legacy layout.
	agentRaw := ReadText(filepath.Join(ws, "AGENT.md"))
	if agentRaw != "" && !picoclawTemplates[ContentHash(agentRaw)] {
		if it, ok := TextItem(env, CatAgentsMD, stripFrontMatter(agentRaw), "PicoClaw AGENT.md"); ok {
			items = append(items, it)
		}
	} else if agentRaw == "" {
		if it, ok := TextItem(env, CatAgentsMD, ReadText(filepath.Join(ws, "AGENTS.md")), "PicoClaw AGENTS.md"); ok {
			items = append(items, it)
		}
	}
	soul := ReadText(filepath.Join(ws, "SOUL.md"))
	if picoclawTemplates[ContentHash(soul)] {
		soul = ""
	}
	from := "PicoClaw SOUL.md"
	if agentRaw == "" {
		if id := ReadText(filepath.Join(ws, "IDENTITY.md")); id != "" {
			soul = strings.TrimSpace(soul + "\n\n" + id)
			from = "PicoClaw SOUL.md + IDENTITY.md"
		}
	}
	if it, ok := TextItem(env, CatSoul, soul, from); ok {
		items = append(items, it)
	}
	if u := ReadText(filepath.Join(ws, "USER.md")); u != "" && !picoclawTemplates[ContentHash(u)] {
		if it, ok := TextItem(env, CatUserMD, u, "PicoClaw USER.md"); ok {
			items = append(items, it)
		}
	}

	// Memory and daily notes.
	if text := ReadText(filepath.Join(ws, "memory", "MEMORY.md")); text != "" && !picoclawTemplates[ContentHash(text)] {
		items = append(items, MemoryItems(nanobotDropTemplateEntries(SplitMemory(text, ""), picoclawMemoryTemplateEntries), "global", "import:picoclaw")...)
	}
	items = append(items, nanobotKnowledge(env, filepath.Join(ws, "memory"), "memory/", func(rel string) bool {
		return !picoclawDailyNote.MatchString(rel)
	})...)

	for _, sk := range ScanSkills(filepath.Join(ws, "skills"), 1) {
		items = append(items, SkillItem(env, sk))
	}

	// MCP.
	servers := nanobotMap(nanobotMap(nanobotMap(cfg, "tools"), "mcp"), "servers")
	names := make([]string, 0, len(servers))
	for k := range servers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		m, ok := servers[name].(map[string]any)
		if !ok {
			continue
		}
		it := MCPItem(env, MCPPayload{
			Name: name, Transport: nanobotStr(m, "type"), Command: nanobotStr(m, "command"),
			Args: nanobotStrings(nanobotGet(m, "args")), Env: nanobotStringMap(m, "env"),
			URL: nanobotStr(m, "url"), Headers: nanobotStringMap(m, "headers"),
		})
		if !nanobotBool(m, "enabled", true) {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in PicoClaw", " · ")
		}
		items = append(items, it)
	}

	// Schedules.
	items = append(items, nanobotStoreJobs(filepath.Join(ws, "cron", "jobs.json"), "picoclaw")...)
	hb := nanobotMap(cfg, "heartbeat")
	every := time.Duration(nanobotInt(hb, "interval", 30)) * time.Minute
	if it, ok := nanobotHeartbeatItem(ReadText(filepath.Join(ws, "HEARTBEAT.md")), "Add your heartbeat tasks below this line:", every, nanobotBool(hb, "enabled", true)); ok {
		items = append(items, it)
	}

	// Extra agents → roles.
	for _, raw := range nanobotList(nanobotMap(cfg, "agents"), "list") {
		a, ok := raw.(map[string]any)
		if !ok || nanobotBool(a, "default", false) {
			continue
		}
		id := nanobotStr(a, "id")
		aws := ExpandHome(nanobotStr(a, "workspace"))
		if id == "" || aws == "" || filepath.Clean(aws) == filepath.Clean(ws) {
			continue
		}
		prompt := stripFrontMatter(ReadText(filepath.Join(aws, "AGENT.md")))
		if prompt == "" {
			prompt = ReadText(filepath.Join(aws, "AGENTS.md"))
		}
		if s := ReadText(filepath.Join(aws, "SOUL.md")); s != "" && !picoclawTemplates[ContentHash(s)] {
			prompt = strings.TrimSpace(prompt + "\n\n" + s)
		}
		model := ""
		switch m := nanobotGet(a, "model").(type) {
		case string:
			model = m
		case map[string]any:
			model = nanobotStr(m, "primary")
		}
		if r, ok := aliases[model]; ok {
			model = r.id + "/" + r.model
		}
		name := nanobotStr(a, "name")
		items = append(items, RoleItem(env, RolePayload{Name: id, Title: name, Prompt: prompt, Model: model}))
	}

	items = append(items, picoclawChannels(env, cfg, sec, res)...)
	return items, warnings
}

var picoclawDailyNote = regexp.MustCompile(`^\d{6}/\d{8}\.md$`)

// picoclawProtocol mirrors ExtractProtocol: the explicit provider wins (model
// kept whole), else the model's first path segment, else "openai".
func picoclawProtocol(provider, model string) (protocol, modelID string) {
	model = strings.TrimSpace(model)
	if p := strings.ToLower(strings.TrimSpace(provider)); p != "" {
		return p, model
	}
	if i := strings.Index(model, "/"); i > 0 {
		return strings.ToLower(model[:i]), model[i+1:]
	}
	return "openai", model
}

// picoclawOAuth / picoclawCLI are protocols without a portable API key.
var picoclawOAuth = map[string]bool{"github-copilot": true, "copilot": true, "antigravity": true, "google-antigravity": true}
var picoclawCLI = map[string]bool{"claude-cli": true, "claudecli": true, "codex-cli": true, "codexcli": true}

// picoclawVendorAlias maps PicoClaw protocol ids to the vendor names
// nanobotIdentity knows (provider_metadata.go Aliases).
var picoclawVendorAlias = map[string]string{
	"gpt": "openai", "claude": "anthropic", "anthropic-messages": "anthropic", "google": "gemini",
	"qwen-portal": "dashscope", "qwen": "dashscope", "qwen-intl": "dashscope", "qwen-international": "dashscope",
	"dashscope-intl": "dashscope", "qwen-us": "dashscope", "dashscope-us": "dashscope",
	"glm": "zhipu", "z.ai": "zai", "z-ai": "zai", "azure-openai": "azure",
}

var picoclawLabels = map[string]string{
	"volcengine": "Volcengine", "mimo": "Xiaomi MiMo", "zhipu": "Zhipu AI", "glm": "Zhipu AI",
	"siliconflow": "SiliconFlow", "modelscope": "ModelScope", "longcat": "LongCat", "venice": "Venice AI",
	"nearai": "NEAR AI Cloud", "shengsuanyun": "ShengsuanYun", "vivgrid": "Vivgrid", "avian": "Avian",
	"novita": "Novita AI", "litellm": "LiteLLM", "gpt4free": "GPT4Free", "alibaba-coding": "Alibaba Coding Plan",
	"alibaba-coding-anthropic": "Alibaba Coding Plan (Anthropic)",
}

// picoclawDefaultBase are PicoClaw's default endpoints
// (pkg/providers/provider_metadata.go DefaultAPIBase).
var picoclawDefaultBase = map[string]string{
	"openai": "https://api.openai.com/v1", "gpt": "https://api.openai.com/v1",
	"anthropic": "https://api.anthropic.com/v1", "claude": "https://api.anthropic.com/v1",
	"anthropic-messages": "https://api.anthropic.com/v1",
	"gemini":             "https://generativelanguage.googleapis.com/v1beta", "google": "https://generativelanguage.googleapis.com/v1beta",
	"deepseek": "https://api.deepseek.com/v1", "openrouter": "https://openrouter.ai/api/v1",
	"qwen-portal": "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen": "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"qwen-intl": "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "dashscope-intl": "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
	"qwen-us": "https://dashscope-us.aliyuncs.com/compatible-mode/v1", "dashscope-us": "https://dashscope-us.aliyuncs.com/compatible-mode/v1",
	"moonshot": "https://api.moonshot.cn/v1", "volcengine": "https://ark.cn-beijing.volces.com/api/v3",
	"zhipu": "https://open.bigmodel.cn/api/paas/v4", "glm": "https://open.bigmodel.cn/api/paas/v4",
	"groq": "https://api.groq.com/openai/v1", "mistral": "https://api.mistral.ai/v1",
	"nvidia": "https://integrate.api.nvidia.com/v1", "cerebras": "https://api.cerebras.ai/v1",
	"ollama": "http://localhost:11434/v1", "vllm": "http://localhost:8000/v1", "lmstudio": "http://localhost:1234/v1",
	"gpt4free": "http://localhost:1337/v1", "g4f": "http://localhost:1337/v1",
	"venice": "https://api.venice.ai/api/v1", "nearai": "https://cloud-api.near.ai/v1",
	"shengsuanyun": "https://router.shengsuanyun.com/api/v1", "siliconflow": "https://api.siliconflow.cn/v1",
	"vivgrid": "https://api.vivgrid.com/v1", "minimax": "https://api.minimaxi.com/v1",
	"longcat": "https://api.longcat.chat/openai", "modelscope": "https://api-inference.modelscope.cn/v1",
	"mimo": "https://api.xiaomimimo.com/v1", "avian": "https://api.avian.io/v1",
	"zai": "https://api.z.ai/api/coding/paas/v4", "z.ai": "https://api.z.ai/api/coding/paas/v4", "z-ai": "https://api.z.ai/api/coding/paas/v4",
	"alibaba-coding":           "https://coding-intl.dashscope.aliyuncs.com/v1",
	"alibaba-coding-anthropic": "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic",
	"novita":                   "https://api.novita.ai/openai", "litellm": "http://localhost:4000/v1",
}

// picoclawTemplates are ContentHash values of every version, in git history up
// to bbf6893, of the workspace files PicoClaw's onboarding copies
// (workspace/AGENT.md, AGENTS.md, IDENTITY.md, SOUL.md, USER.md,
// memory/MEMORY.md); unchanged copies are skipped.
var picoclawTemplates = nanobotHashSet(`
	2bcfeb268c3b 5dd26fb4c883 78256f72873f c53844dd5e19 c7740a746bab e11f77580cd7 e92d2d3a389c
	ec18ef9c5ebc f4e6852339c5`)

// picoclawMemoryTemplateEntries are the entries (as split by SplitMemory) of
// every shipped memory/MEMORY.md version, dropped from a user's file.
var picoclawMemoryTemplateEntries = nanobotHashSet(`
	1b8c557c0f94 3937696948ca 6c065a8a0717 e35f01cc3374`)

func picoclawChannels(env Env, cfg, sec map[string]any, res *picoclawResolver) []Item {
	list := nanobotMap(cfg, "channel_list")
	legacy := false
	if list == nil {
		list, legacy = nanobotMap(cfg, "channels"), true
	}
	secList := nanobotMap(sec, "channel_list")
	if secList == nil {
		secList = nanobotMap(sec, "channels")
	}
	names := make([]string, 0, len(list))
	for k := range list {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Item
	seen := map[string]bool{}
	for _, name := range names {
		c, ok := list[name].(map[string]any)
		if !ok {
			continue
		}
		typ := strings.ToLower(nanobotStr(c, "type"))
		if typ == "" {
			typ = strings.ToLower(name)
		}
		settings := nanobotMap(c, "settings")
		if settings == nil || legacy {
			settings = c
		}
		merged := map[string]any{}
		for k, v := range settings {
			merged[k] = v
		}
		if sc := nanobotMap(nanobotMap(secList, name), "settings"); sc != nil {
			for k, v := range sc {
				merged[k] = v
			}
		} else if sc := nanobotMap(secList, name); sc != nil && legacy {
			for k, v := range sc {
				merged[k] = v
			}
		}
		get := func(k string) string {
			v, _ := res.resolve(nanobotStr(merged, k))
			return v
		}
		enabled := nanobotBool(c, "enabled", false)
		fields := map[string]any{}
		missing := ""
		hasCred := false
		req := func(field, val string) {
			if val != "" {
				fields[field] = val
				hasCred = true
			} else if missing == "" {
				missing = field
			}
		}
		opt := func(field, val string) {
			if val != "" {
				fields[field] = val
			}
		}
		detail := ""
		platform := typ
		switch typ {
		case "telegram":
			req("bot_token", get("token"))
		case "discord":
			req("bot_token", get("token"))
		case "slack":
			req("bot_token", get("bot_token"))
			req("app_token", get("app_token"))
		case "matrix":
			opt("homeserver", get("homeserver"))
			opt("user_id", get("user_id"))
			req("access_token", get("access_token"))
		case "feishu":
			opt("app_id", get("app_id"))
			req("app_secret", get("app_secret"))
			opt("verify_token", get("verification_token"))
			if fields["app_id"] == nil && missing == "" {
				missing = "app_id"
			}
			detail = "PicoClaw used Feishu's long connection; Antares receives Feishu events on a webhook"
		case "whatsapp", "whatsapp_native":
			if enabled {
				out = append(out, UnsupportedItem(CatChannel, Slug(name), "WhatsApp ("+name+")", "PicoClaw's WhatsApp is a device-bound WhatsApp Web session; Antares supports the Meta Cloud API — set it up in Antares"))
			}
			continue
		case "pico", "pico_client":
			continue // PicoClaw's own web UI channel
		default:
			if enabled {
				label := picoclawChannelLabels[typ]
				if label == "" {
					label = PlatformLabel(typ)
				}
				out = append(out, UnsupportedItem(CatChannel, Slug(name), label, "Antares has no "+label+" gateway"))
			}
			continue
		}
		if !enabled && !hasCred {
			continue
		}
		if seen[platform] {
			out = append(out, UnsupportedItem(CatChannel, Slug(name), PlatformLabel(platform)+" ("+name+")", "Antares has one "+PlatformLabel(platform)+" bot; another one is already in this plan"))
			continue
		}
		seen[platform] = true
		if users := nanobotStrings(nanobotGet(c, "allow_from")); len(users) > 0 {
			fields["allowed_users"] = users
		}
		it := ChannelItem(env, platform, "", fields, missing)
		if detail != "" {
			it.Detail = detail
		}
		if !enabled {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in PicoClaw", " · ")
		}
		out = append(out, it)
	}
	return out
}

var picoclawChannelLabels = map[string]string{
	"onebot": "QQ (OneBot)", "line": "LINE", "irc": "IRC", "vk": "VK", "maixcam": "MaixCam",
	"deltachat": "Delta Chat", "mqtt": "MQTT", "teams_webhook": "Teams webhook",
	"slack_webhook": "Slack webhook", "weixin": "WeChat", "wecom": "WeCom", "dingtalk": "DingTalk", "qq": "QQ",
}

// picoclawResolver resolves PicoClaw SecureString raw values.
type picoclawResolver struct {
	dir     string
	decrypt bool
}

// resolve returns the plain value, or "" with a note on why it could not be
// read.
func (r *picoclawResolver) resolve(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return "", ""
	case strings.HasPrefix(raw, "file://"):
		name := strings.TrimSpace(strings.TrimPrefix(raw, "file://"))
		p := filepath.Join(r.dir, name)
		rel, err := filepath.Rel(r.dir, p)
		if name == "" || err != nil || !filepath.IsLocal(rel) {
			return "", "the key file reference is invalid"
		}
		if v := ReadText(p); v != "" {
			return v, ""
		}
		return "", "the key file " + name + " could not be read"
	case strings.HasPrefix(raw, "enc://"):
		if !r.decrypt {
			return "", "the key is encrypted (enc://)"
		}
		v, err := picoclawDecrypt(raw, os.Getenv("PICOCLAW_KEY_PASSPHRASE"), picoclawSSHKeyPath())
		if err != nil {
			return "", "the key is encrypted (enc://) and could not be decrypted: " + err.Error()
		}
		return v, ""
	}
	return raw, ""
}

func picoclawSSHKeyPath() string {
	if p, ok := os.LookupEnv("PICOCLAW_SSH_KEY_PATH"); ok {
		return ExpandHome(p)
	}
	p := filepath.Join(HomeDir(), ".ssh", "picoclaw_ed25519.key")
	if IsFile(p) {
		return p
	}
	return ""
}

// picoclawDecrypt decrypts an enc:// credential (pkg/credential/credential.go
// resolveEncrypted): base64(salt[16] | nonce[12] | AES-256-GCM ciphertext).
func picoclawDecrypt(raw, passphrase, sshKeyPath string) (string, error) {
	if passphrase == "" {
		return "", errors.New("PICOCLAW_KEY_PASSPHRASE is not set")
	}
	if sshKeyPath == "" {
		return "", errors.New("no SSH key (PICOCLAW_SSH_KEY_PATH or ~/.ssh/picoclaw_ed25519.key)")
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "enc://"))
	if err != nil {
		return "", errors.New("invalid base64")
	}
	if len(blob) < 16+12+1 {
		return "", errors.New("payload too short")
	}
	sshBytes, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return "", errors.New("cannot read the SSH key")
	}
	key, err := picoclawDeriveKey(passphrase, sshBytes, blob[:16])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, blob[16:28], blob[28:], nil)
	if err != nil {
		return "", errors.New("wrong passphrase or SSH key")
	}
	return string(plain), nil
}

func picoclawDeriveKey(passphrase string, sshKey, salt []byte) ([]byte, error) {
	sum := sha256.Sum256(sshKey)
	mac := hmac.New(sha256.New, sum[:])
	mac.Write([]byte(passphrase))
	return hkdf.Key(sha256.New, mac.Sum(nil), salt, "picoclaw-credential-v1", 32)
}
