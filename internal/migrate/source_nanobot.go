package migrate

// nanobot (HKUDS/nanobot) — https://github.com/HKUDS/nanobot
//
// Format, verified against the project's own source at commit 432421b
// (2026-10-03):
//
//   - Config: ~/.nanobot/config.json, a Pydantic model whose keys may be
//     camelCase or snake_case (config_base.py: alias_generator=to_camel,
//     populate_by_name=True). ${VAR} references are resolved from the
//     environment at load time.
//     https://github.com/HKUDS/nanobot/blob/main/nanobot/config/schema.py
//     https://github.com/HKUDS/nanobot/blob/main/nanobot/config/loader.py
//   - providers.<name>.{apiKey, apiBase, extraHeaders}; any extra key is an
//     OpenAI-compatible custom provider. Default endpoints come from
//     nanobot/providers/registry.py.
//   - agents.defaults.{model, provider, modelPreset, fallbackModels, workspace}
//     plus top-level modelPresets.
//   - Workspace (agents.defaults.workspace, default ~/.nanobot/workspace):
//     SOUL.md, USER.md, AGENTS.md, HEARTBEAT.md, memory/MEMORY.md,
//     skills/<name>/SKILL.md, cron/jobs.json (legacy: ~/.nanobot/cron/jobs.json).
//     Bundled templates are skipped when unchanged (agent/context.py
//     _is_template_content). memory/history.jsonl is conversation history and
//     is not migrated.
//   - tools.mcpServers.<name>.{type: stdio|sse|streamableHttp, command, args,
//     env, url, headers, auth}.
//   - channels.<name> per-channel dicts (channels/<name>/runtime.py configs).
//     WhatsApp is a WhatsApp Web (neonize) session — device-bound, not the Meta
//     Cloud API — so it is unsupported. Signal talks to a signal-cli JSON-RPC
//     daemon, Antares to signal-cli-rest-api (see the item detail).
//   - gateway.heartbeat.{enabled, intervalS}: a protected cron job reading
//     HEARTBEAT.md "## Active Tasks"; imported as an @every schedule.
//
// Not verified: NANOBOT_* environment overrides (pydantic-settings with "__"
// nesting) are not read; Feishu multi-instance configs only import the
// default (top-level) instance.
//
// This file also holds small helpers shared by the picoclaw, cowagent and
// qwenpaw sources (prefixed "nanobot" to stay clear of other sources' names):
// PicoClaw's cron store and workspace layout are ports of nanobot's.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type nanobotSource struct{}

func init() { Register(nanobotSource{}) }

func (nanobotSource) ID() string   { return "nanobot" }
func (nanobotSource) Name() string { return "nanobot" }

// nanobotRunning are pgrep -f patterns for nanobot's process titles
// (cli/process_identity.py: "nanobot-<role>" via setproctitle).
var nanobotRunning = []string{`nanobot-(gateway|agent|webui)`, `nanobot (gateway|agent|webui)`}

func (s nanobotSource) Detect(ctx context.Context, root string) (Detection, error) {
	dir := root
	if dir == "" {
		dir = filepath.Join(HomeDir(), ".nanobot")
	}
	dir = ExpandHome(dir)
	if !IsFile(filepath.Join(dir, "config.json")) && !IsDir(filepath.Join(dir, "workspace")) {
		return Detection{}, ErrNotFound
	}
	det := Detection{Source: s.ID(), Name: s.Name(), Root: dir, Running: RunningCheck(nanobotRunning, nil)}
	items, _ := nanobotBuild(det, nil)
	det.Summary = Summarize(items)
	return det, nil
}

func (s nanobotSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	items, warnings := nanobotBuild(det, env)
	det.Summary = Summarize(items)
	return Plan{Detection: det, Items: items, Warnings: warnings}, nil
}

// nanobotBuild reads the install at det.Root. env may be nil (Detect).
func nanobotBuild(det Detection, env Env) ([]Item, []string) {
	root := det.Root
	var cfg map[string]any
	var warnings []string
	if p := filepath.Join(root, "config.json"); IsFile(p) {
		if err := ReadJSON(p, &cfg); err != nil {
			warnings = append(warnings, "config.json could not be read: "+err.Error())
		}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	defaults := nanobotMap(nanobotMap(cfg, "agents"), "defaults")
	ws := nanobotStr(defaults, "workspace")
	if ws == "" {
		ws = filepath.Join(root, "workspace")
	}
	ws = ExpandHome(ws)

	var items []Item
	provs := newNanobotProviders(env)

	// Providers. https://github.com/HKUDS/nanobot/blob/main/nanobot/config/schema.py (ProvidersConfig)
	providers := nanobotMap(cfg, "providers")
	names := make([]string, 0, len(providers))
	for k := range providers {
		names = append(names, k)
	}
	sort.Strings(names)
	sourceToID := map[string]string{}
	for _, raw := range names {
		pm, ok := providers[raw].(map[string]any)
		if !ok {
			continue
		}
		name := nanobotSnake(raw)
		key := nanobotStr(pm, "api_key")
		base := nanobotStr(pm, "api_base")
		if key == "" && base == "" {
			continue
		}
		if nanobotOAuthProviders[name] {
			items = append(items, OAuthProviderItem(Slug(name), nanobotTitle(name)))
			continue
		}
		var keyNote string
		if strings.Contains(key, "$") {
			v, ok := ExpandVars(key, nil)
			if !ok {
				v, keyNote = "", "key references an unset environment variable"
			}
			key = v
		}
		base, _ = ExpandVars(base, nil)
		label := nanobotStr(pm, "display_name")
		if label == "" {
			label = nanobotTitle(name)
		}
		srcKind := "openai-compatible"
		if nanobotAnthropicBackends[name] {
			srcKind = "anthropic"
		}
		eff := base
		if eff == "" {
			eff = nanobotDefaultBase[name]
		}
		if name == "custom" && label == "Custom" {
			label = "nanobot custom"
		}
		p := nanobotIdentity(name, label, srcKind, eff)
		p.APIKey = key
		p.Headers = nanobotStringMap(pm, "extra_headers")
		sourceToID[name] = provs.add(p, keyNote)
	}

	// Default model. agents.defaults (or the active model preset) names a
	// model and a provider ("auto" = inferred from the model prefix).
	modelName, provName := nanobotStr(defaults, "model"), nanobotStr(defaults, "provider")
	presets := nanobotMap(cfg, "model_presets")
	if pn := nanobotStr(defaults, "model_preset"); pn != "" && pn != "default" {
		if pr := nanobotMap(presets, pn); pr != nil {
			modelName, provName = nanobotStr(pr, "model"), nanobotStr(pr, "provider")
		}
	}
	resolve := func(model, prov string) (string, string) {
		if prov == "" || prov == "auto" {
			if i := strings.Index(model, "/"); i > 0 {
				prov = nanobotSnake(model[:i])
			}
		}
		prov = nanobotSnake(prov)
		id := sourceToID[prov]
		if id == "" && len(sourceToID) == 1 {
			for k, v := range sourceToID {
				prov, id = k, v
			}
		}
		if strings.HasPrefix(strings.ReplaceAll(strings.ToLower(model), "-", "_"), prov+"/") {
			model = model[len(prov)+1:]
		}
		return id, model
	}
	var modelItem []Item
	if modelName != "" && len(sourceToID) > 0 {
		pid, m := resolve(modelName, provName)
		mp := ModelPayload{Provider: pid, Model: m}
		for _, fb := range nanobotList(defaults, "fallback_models") {
			var fm, fp string
			switch v := fb.(type) {
			case string:
				pr := nanobotMap(presets, v)
				fm, fp = nanobotStr(pr, "model"), nanobotStr(pr, "provider")
			case map[string]any:
				fm, fp = nanobotStr(v, "model"), nanobotStr(v, "provider")
			}
			if fm == "" {
				continue
			}
			if id, m := resolve(fm, fp); id != "" {
				mp.Fallback = append(mp.Fallback, id+"/"+m)
				provs.addModel(id, m)
			} else {
				mp.Fallback = append(mp.Fallback, fm)
			}
		}
		if pid != "" {
			provs.addModel(pid, m)
			modelItem = append(modelItem, ModelItem(env, mp))
		}
	}
	items = append(items, provs.items()...)
	items = append(items, modelItem...)

	// Persona files.
	for _, f := range []struct {
		cat  Category
		file string
	}{{CatSoul, "SOUL.md"}, {CatAgentsMD, "AGENTS.md"}, {CatUserMD, "USER.md"}} {
		text := ReadText(filepath.Join(ws, f.file))
		if text == "" || nanobotTemplates[ContentHash(text)] {
			continue
		}
		if it, ok := TextItem(env, f.cat, text, "nanobot "+f.file); ok {
			items = append(items, it)
		}
	}

	// Long-term memory.
	if text := ReadText(filepath.Join(ws, "memory", "MEMORY.md")); text != "" && !nanobotTemplates[ContentHash(text)] {
		items = append(items, MemoryItems(nanobotDropTemplateEntries(SplitMemory(text, ""), nanobotMemoryTemplateEntries), "global", "import:nanobot")...)
	}

	// Skills.
	for _, sk := range ScanSkills(filepath.Join(ws, "skills"), 1) {
		items = append(items, SkillItem(env, sk))
	}

	// MCP servers.
	items = append(items, nanobotMCP(env, nanobotMap(nanobotMap(cfg, "tools"), "mcp_servers"))...)

	// Schedules: workspace cron store, else the legacy instance-level one
	// (cli/runtime_config.py migrates ~/.nanobot/cron/jobs.json into the
	// workspace).
	jobs := filepath.Join(ws, "cron", "jobs.json")
	if !IsFile(jobs) {
		jobs = filepath.Join(root, "cron", "jobs.json")
	}
	items = append(items, nanobotStoreJobs(jobs, "nanobot")...)
	hb := nanobotMap(nanobotMap(cfg, "gateway"), "heartbeat")
	hbEnabled := nanobotBool(hb, "enabled", true)
	interval := time.Duration(nanobotInt(hb, "interval_s", 1800)) * time.Second
	if it, ok := nanobotHeartbeatItem(ReadText(filepath.Join(ws, "HEARTBEAT.md")), "## Active Tasks", interval, hbEnabled); ok {
		items = append(items, it)
	}

	// Channels.
	items = append(items, nanobotChannels(env, nanobotMap(cfg, "channels"))...)
	return items, warnings
}

// nanobotOAuthProviders are logins nanobot keeps in its own token stores
// (schema.py: exclude=True), not portable keys.
var nanobotOAuthProviders = map[string]bool{"openai_codex": true, "github_copilot": true, "xai_grok": true}

// nanobotAnthropicBackends speak the Anthropic Messages API
// (providers/registry.py backend="anthropic").
var nanobotAnthropicBackends = map[string]bool{"anthropic": true, "kimi_coding": true, "minimax_anthropic": true}

// nanobotDefaultBase are nanobot's default endpoints
// (providers/registry.py default_api_base), used when apiBase is unset.
var nanobotDefaultBase = map[string]string{
	"openrouter": "https://openrouter.ai/api/v1", "orcarouter": "https://api.orcarouter.ai/v1",
	"edenai": "https://api.edenai.run/v3", "opencode": "https://opencode.ai/zen/v1",
	"opencode_zen": "https://opencode.ai/zen/v1", "opencode_go": "https://opencode.ai/zen/go/v1",
	"huggingface": "https://router.huggingface.co/v1", "skywork": "https://api.apifree.ai/agent/v1",
	"aihubmix": "https://aihubmix.com/v1", "siliconflow": "https://api.siliconflow.cn/v1",
	"novita": "https://api.novita.ai/openai", "volcengine": "https://ark.cn-beijing.volces.com/api/v3",
	"volcengine_coding_plan": "https://ark.cn-beijing.volces.com/api/coding/v3",
	"byteplus":               "https://ark.ap-southeast.bytepluses.com/api/v3",
	"byteplus_coding_plan":   "https://ark.ap-southeast.bytepluses.com/api/coding/v3",
	"anthropic":              "https://api.anthropic.com", "openai": "https://api.openai.com/v1",
	"deepseek": "https://api.deepseek.com", "gemini": "https://generativelanguage.googleapis.com/v1beta/openai/",
	"zhipu": "https://open.bigmodel.cn/api/paas/v4", "dashscope": "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"modelscope": "https://api-inference.modelscope.cn/v1", "moonshot": "https://api.moonshot.ai/v1",
	"kimi_coding": "https://api.kimi.com/coding/v1", "minimax": "https://api.minimax.io/v1",
	"minimax_anthropic": "https://api.minimax.io/anthropic", "mistral": "https://api.mistral.ai/v1",
	"stepfun": "https://api.stepfun.com/v1", "xiaomi_mimo": "https://api.xiaomimimo.com/v1",
	"longcat": "https://api.longcat.chat/openai/v1", "ant_ling": "https://api.ant-ling.com/v1",
	"ollama": "http://localhost:11434/v1", "lm_studio": "http://localhost:1234/v1",
	"atomic_chat": "http://localhost:1337/v1", "ovms": "http://localhost:8000/v3",
	"nvidia": "https://integrate.api.nvidia.com/v1", "groq": "https://api.groq.com/openai/v1",
	"qianfan": "https://qianfan.baidubce.com/v2",
}

// nanobotTemplates are ContentHash values of every version, in the repo's git
// history up to 432421b, of the workspace files nanobot ships
// (nanobot/templates/ and the older workspace/: SOUL, USER, AGENTS, TOOLS,
// IDENTITY, HEARTBEAT, memory/MEMORY); an unchanged copy is skipped.
var nanobotTemplates = nanobotHashSet(`
	0ec11f6e68ff 13132848231b 13fc3b6888f4 2b13195a8a18 2dbd2f4003fe 2dee05ffd1e8 3e6757e167ad
	45884f8088bc 4f41a73a6fed 55904eecf56f 5dcbc069ec61 7378fac0c7b3 772b115b1381 8cadb2b177c2
	8fae6040a3ab 9251e07f4c47 943a9beea34c 9504b6385a58 95bef5058759 985e6b1aec63 9e43cda22222
	a4fee090fe52 a50bdd57660a a6d70cbdab8e b06d5380767d b47a9f03bf63 bde72185d36d c2d6b90ce481
	c3a35a30777d dcd78d61ad4e e9ef436b290f f26f5a9c6275 fa89ccd6a145 fbf3d13192c8 fef3f8daad47`)

// nanobotMemoryTemplateEntries are the ContentHash values of the entries
// (as split by SplitMemory) of every shipped MEMORY.md/TOOLS.md version,
// dropped from a user's MEMORY.md.
var nanobotMemoryTemplateEntries = nanobotHashSet(`
	09f79b7f1101 148e2123a759 1683f0c64c32 18679ca9b6ea 1ca2d0d0a4a7 1f7fe167a20b 205455723c74
	20cde29c938d 24203fd011f0 251c6fffe3f5 25dc9fd40343 2ca2f2d1fa45 30fd8a620e04 3303bd10125b
	36c05616de24 3883ac050e01 3cf5033d7329 3d78362d9b6a 3db13a54f3a8 3fc20c00f0c3 45068b8931ac
	45f3fef34568 4604278288bf 4ab4a37af29e 4bf1cf3fed59 4d6bfdde5d88 4ec38edb57f7 4fccee776ae2
	562eb43fa3c4 5a3548f32491 5aea080dd27d 5aec263fab1c 5ec7594f6f8c 60646c7a3bb2 6208d77a38d7
	64081eea88da 6477de6b60fa 6975d3c2f2c4 6b499811b9aa 6c065a8a0717 6cf49541170e 703def43b7b2
	7109b6e2bd8b 71f7b05f4638 7459f657e665 768c45ee888d 76e3d89e7c2e 77142ffddb3a 78d0113b8591
	78ea22713001 79a958ce124d 8106ce317a46 89f7c8172c9d 8dfa96bc66dc 90d80b333478 913108960b5e
	988a1bd5fee1 99411ad1b346 9aad8d1afc9e a3b2d7056597 a533ed910088 aeb4cfb43d38 af1cfe16d2bf
	ba05af6a9845 ba1ac632f5c3 ba32f79a13a9 bb084655db50 beb613cf6659 c01ad8a890c1 c305a482aa4c
	c5cf14be0b2f c92e1b8ae1a5 cb552517e548 cc25a700ea68 cdd6db3c0b15 cf6e7bf63961 d01e674accf2
	d0debad3de57 d1e6e6e1a786 d3e571137b75 d963ed2f6dad d96e9415dc49 da3c31d1cac6 dd582c6880cd
	de14fb7a21cd de338f3638f6 e59c27df7700 e64ce71bef4d e6bd540dda6e e6ef101db753 eec725f729d2
	f0d731555d6f f2f1c347c30f f39a0996e291 f93a4eab9f6d fdfeea869708`)

func nanobotMCP(env Env, servers map[string]any) []Item {
	names := make([]string, 0, len(servers))
	for k := range servers {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Item
	for _, name := range names {
		m, ok := servers[name].(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(nanobotStr(m, "auth"), "oauth") {
			out = append(out, UnsupportedItem(CatMCP, name, name, "the server uses an OAuth login — connect it again in Antares"))
			continue
		}
		p := MCPPayload{
			Name: name, Transport: nanobotStr(m, "type"), Command: nanobotStr(m, "command"),
			Args: nanobotStrings(nanobotGet(m, "args")), Env: nanobotStringMap(m, "env"),
			URL: nanobotStr(m, "url"), Headers: nanobotStringMap(m, "headers"),
		}
		out = append(out, MCPItem(env, p))
	}
	return out
}

func nanobotChannels(env Env, channels map[string]any) []Item {
	names := make([]string, 0, len(channels))
	for k := range channels {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Item
	for _, name := range names {
		c, ok := channels[name].(map[string]any)
		if !ok {
			continue
		}
		enabled := nanobotBool(c, "enabled", false)
		platform := strings.ToLower(name)
		fields := map[string]any{}
		missing := ""
		hasCred := false
		set := func(field, val string, required bool) {
			if val != "" {
				fields[field] = val
				if field != "homeserver" && field != "user_id" && field != "app_id" && field != "number" && field != "api_url" {
					hasCred = true
				}
			} else if required && missing == "" {
				missing = field
			}
		}
		users := nanobotStrings(nanobotGet(c, "allow_from"))
		detail := ""
		switch platform {
		case "telegram":
			set("bot_token", nanobotStr(c, "token"), true)
		case "discord":
			set("bot_token", nanobotStr(c, "token"), true)
		case "slack":
			set("bot_token", nanobotStr(c, "bot_token"), true)
			set("app_token", nanobotStr(c, "app_token"), true)
			if m := nanobotStr(c, "mode"); m != "" && m != "socket" {
				detail = "nanobot used Slack " + m + " mode; Antares uses Socket Mode (needs the xapp- token)"
			}
		case "matrix":
			set("homeserver", nanobotStr(c, "homeserver"), false)
			set("user_id", nanobotStr(c, "user_id"), false)
			set("access_token", nanobotStr(c, "access_token"), true)
		case "signal":
			set("number", nanobotStr(c, "phone_number"), true)
			host, port := nanobotStr(c, "daemon_host"), nanobotInt(c, "daemon_port", 8080)
			if host == "" {
				host = "localhost"
			}
			set("api_url", fmt.Sprintf("http://%s:%d", host, port), false)
			users = nanobotStrings(nanobotGet(nanobotMap(c, "dm"), "allow_from"))
			hasCred = enabled
			detail = "nanobot used a signal-cli JSON-RPC daemon; Antares needs signal-cli-rest-api at api_url"
		case "feishu":
			set("app_id", nanobotStr(c, "app_id"), true)
			set("app_secret", nanobotStr(c, "app_secret"), true)
			set("verify_token", nanobotStr(c, "verification_token"), false)
			detail = "nanobot used Feishu's long connection; Antares receives Feishu events on a webhook"
		case "whatsapp":
			if enabled {
				out = append(out, UnsupportedItem(CatChannel, "whatsapp", "WhatsApp", "nanobot's WhatsApp is a device-bound WhatsApp Web session; Antares supports the Meta Cloud API — set it up in Antares"))
			}
			continue
		case "websocket":
			continue // nanobot's own web UI transport
		default:
			if enabled {
				out = append(out, UnsupportedItem(CatChannel, Slug(platform), nanobotChannelLabel(platform), "Antares has no "+nanobotChannelLabel(platform)+" gateway"))
			}
			continue
		}
		if !enabled && !hasCred {
			continue
		}
		if len(users) > 0 {
			fields["allowed_users"] = users
		}
		it := ChannelItem(env, platform, "", fields, missing)
		if detail != "" {
			it.Detail = detail
		}
		if !enabled {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in nanobot", " · ")
		}
		out = append(out, it)
	}
	return out
}

func nanobotChannelLabel(p string) string {
	switch p {
	case "weixin", "wechat":
		return "WeChat"
	case "napcat":
		return "QQ (NapCat)"
	case "msteams":
		return "Microsoft Teams"
	case "email":
		return "Email"
	}
	return PlatformLabel(p)
}

func nanobotTitle(name string) string {
	if v, ok := Vendor(name); ok {
		return v.Label
	}
	parts := strings.Split(strings.ReplaceAll(name, "-", "_"), "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// ---- Shared helpers (also used by picoclaw, cowagent, qwenpaw) -------------

// nanobotGet looks key up as written, then in its camelCase and snake_case
// forms (Pydantic configs accept both).
func nanobotGet(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		return v
	}
	if v, ok := m[nanobotCamel(key)]; ok {
		return v
	}
	if v, ok := m[nanobotSnake(key)]; ok {
		return v
	}
	return nil
}

func nanobotMap(m map[string]any, key string) map[string]any {
	v, _ := nanobotGet(m, key).(map[string]any)
	return v
}

func nanobotList(m map[string]any, key string) []any {
	v, _ := nanobotGet(m, key).([]any)
	return v
}

func nanobotStr(m map[string]any, key string) string {
	switch v := nanobotGet(m, key).(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func nanobotBool(m map[string]any, key string, def bool) bool {
	switch v := nanobotGet(m, key).(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func nanobotInt(m map[string]any, key string, def int) int {
	switch v := nanobotGet(m, key).(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// nanobotStrings converts a JSON/YAML list (or a comma-separated string) to
// strings.
func nanobotStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			switch s := e.(type) {
			case string:
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			case float64:
				out = append(out, strconv.FormatFloat(s, 'f', -1, 64))
			}
		}
	case []string:
		for _, s := range x {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.Split(x, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func nanobotStringMap(m map[string]any, key string) map[string]string {
	src, _ := nanobotGet(m, key).(map[string]any)
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		switch s := v.(type) {
		case string:
			if x, ok := ExpandVars(s, nil); ok {
				s = x
			}
			out[k] = s
		case float64, bool:
			out[k] = fmt.Sprint(s)
		}
	}
	return out
}

func nanobotCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func nanobotSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		if r == '-' {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// nanobotDropTemplateEntries removes memory entries that are part of a
// shipped template (headings' placeholder text, instructions).
func nanobotDropTemplateEntries(entries []MemoryEntry, template map[string]bool) []MemoryEntry {
	var out []MemoryEntry
	for _, e := range entries {
		c := strings.TrimSpace(e.Content)
		if template[ContentHash(c)] || nanobotPlaceholder(c) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// nanobotPlaceholder reports text that is only a template placeholder:
// "(…)", "*(…)*", "_…_" or a lone "*…*" note.
func nanobotPlaceholder(c string) bool {
	c = strings.TrimSpace(c)
	if strings.Trim(c, "-*_ ") == "" {
		return true // empty, or a thematic break ("---")
	}
	if strings.Contains(c, "\n") {
		return false
	}
	inner := strings.Trim(c, "*_ ")
	return strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") ||
		(strings.HasPrefix(c, "*") && strings.HasSuffix(c, "*") && len(c) > 2) ||
		(strings.HasPrefix(c, "_") && strings.HasSuffix(c, "_") && len(c) > 2)
}

// nanobotHeartbeatTasks returns the user's tasks in a HEARTBEAT.md: the text
// after marker (when present), without HTML comments, headings or blank
// lines. "" means there is nothing to run.
func nanobotHeartbeatTasks(content, marker string) string {
	if content == "" {
		return ""
	}
	if marker != "" {
		if i := strings.Index(content, marker); i >= 0 {
			content = content[i+len(marker):]
		}
	}
	for {
		i := strings.Index(content, "<!--")
		if i < 0 {
			break
		}
		j := strings.Index(content[i:], "-->")
		if j < 0 {
			content = content[:i]
			break
		}
		content = content[:i] + content[i+j+3:]
	}
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		lines = append(lines, strings.TrimRight(l, " \t"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// nanobotHeartbeatItem turns a HEARTBEAT.md task list into a disabled
// recurring schedule.
func nanobotHeartbeatItem(content, marker string, every time.Duration, enabled bool) (Item, bool) {
	tasks := nanobotHeartbeatTasks(content, marker)
	if tasks == "" {
		return Item{}, false
	}
	if every < time.Minute {
		every = 30 * time.Minute
	}
	prompt := "Heartbeat check. Review these recurring tasks and act on any that need attention; if nothing does, reply only HEARTBEAT_OK.\n\n" + tasks
	return CronItem("heartbeat", CronPayload{Name: "Heartbeat", Schedule: EverySchedule(every), Prompt: prompt}, enabled), true
}

// nanobotStoreJobs reads a nanobot/PicoClaw cron store
// ({"version":1,"jobs":[{id,name,enabled,schedule:{kind,atMs,everyMs,expr,tz},
// payload:{kind,message,command?}}]}); nanobot/cron/types.py and
// https://github.com/sipeed/picoclaw/blob/main/pkg/cron/service.go.
// system_event jobs (nanobot's own Dream/heartbeat jobs) are skipped.
func nanobotStoreJobs(path, source string) []Item {
	var store struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if !IsFile(path) || ReadJSON(path, &store) != nil {
		return nil
	}
	var out []Item
	for i, j := range store.Jobs {
		id := nanobotStr(j, "id")
		if id == "" {
			id = strconv.Itoa(i + 1)
		}
		key := Slug(id)
		name := nanobotStr(j, "name")
		if name == "" {
			name = "job " + id
		}
		sched := nanobotMap(j, "schedule")
		payload := nanobotMap(j, "payload")
		if nanobotStr(payload, "kind") == "system_event" {
			continue
		}
		if cmd := nanobotStr(payload, "command"); cmd != "" && nanobotStr(payload, "message") == "" {
			out = append(out, UnsupportedItem(CatCron, key, name, "the job runs a shell command, not a prompt"))
			continue
		}
		var expr string
		switch nanobotStr(sched, "kind") {
		case "cron":
			expr = nanobotCronExpr(nanobotStr(sched, "expr"))
		case "every":
			ms := nanobotInt(sched, "every_ms", 0)
			if ms <= 0 {
				out = append(out, UnsupportedItem(CatCron, key, name, "the job has no interval"))
				continue
			}
			expr = EverySchedule(time.Duration(ms) * time.Millisecond)
			if time.Duration(ms)*time.Millisecond < time.Minute {
				out = append(out, UnsupportedItem(CatCron, key, name, "intervals under a minute are not supported"))
				continue
			}
		case "at":
			out = append(out, UnsupportedItem(CatCron, key, name, "one-shot schedule"))
			continue
		default:
			out = append(out, UnsupportedItem(CatCron, key, name, "unknown schedule kind"))
			continue
		}
		c := CronPayload{Name: name, Schedule: expr, Prompt: nanobotStr(payload, "message"), Timezone: nanobotStr(sched, "tz")}
		out = append(out, CronItem(key, c, nanobotBool(j, "enabled", true)))
	}
	return out
}

// nanobotCronExpr normalises a five-field cron expression for Antares'
// parser: Sunday as 7 becomes 0. Six-field (seconds) expressions are left
// alone and rejected by CronItem.
func nanobotCronExpr(expr string) string {
	f := strings.Fields(expr)
	if len(f) == 5 {
		if f[4] == "7" {
			f[4] = "0"
		}
		return strings.Join(f, " ")
	}
	return strings.TrimSpace(expr)
}

// nanobotHashSet builds a set from space-separated ContentHash values.
func nanobotHashSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, h := range strings.Fields(s) {
		m[h] = true
	}
	return m
}

// nanobotPIDAlive reports whether the pid in a pid file (a bare number or a
// JSON object with "pid") is a live process. Tests replace it.
var nanobotPIDAlive = func(path string) bool {
	b := ReadText(path)
	if b == "" {
		return false
	}
	pid, err := strconv.Atoi(b)
	if err != nil {
		var obj struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal([]byte(b), &obj) != nil {
			return false
		}
		pid = obj.PID
	}
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// nanobotIdentity maps a source vendor name and its effective endpoint to an
// Antares provider. A known vendor keeps its Antares id when the endpoint is
// on the vendor's host; a catalogue vendor on another endpoint (a proxy, a
// regional host) becomes a custom provider; a non-catalogue vendor keeps its
// id with the source endpoint.
func nanobotIdentity(name, label, srcKind, effBase string) ProviderPayload {
	v, ok := Vendor(name)
	if !ok && effBase != "" {
		v, ok = VendorByBaseURL(effBase)
	}
	if ok && !v.OAuth {
		if effBase == "" || v.BaseURL == "" || nanobotSameHost(effBase, v.BaseURL) {
			base := v.BaseURL
			if base == "" {
				base = effBase
			}
			return ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: base}
		}
		if !isCatalogueID(v.ID) {
			return ProviderPayload{ID: v.ID, Label: v.Label, Kind: srcKind, BaseURL: effBase}
		}
		if label == "" || label == v.Label {
			label = v.Label + " (custom endpoint)"
		}
	}
	if label == "" {
		label = nanobotTitle(name)
	}
	if srcKind == "" {
		srcKind = "openai-compatible"
	}
	return ProviderPayload{ID: CustomProviderID(func(string) bool { return false }, label), Label: label, Kind: srcKind, BaseURL: effBase}
}

// nanobotSameHost compares endpoint hosts; loopback names are one host, told
// apart by port (Ollama on :11434 vs LM Studio on :1234).
func nanobotSameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil || ua.Hostname() == "" {
		return false
	}
	loop := func(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" }
	ha, hb := strings.ToLower(ua.Hostname()), strings.ToLower(ub.Hostname())
	if loop(ha) && loop(hb) {
		return ua.Port() == ub.Port()
	}
	return ha == hb
}

// nanobotProviders collects provider payloads for one plan, merging entries
// that are the same provider (same id, key and endpoint) and renaming a
// different one that would take an id already used in this plan.
type nanobotProviders struct {
	env   Env
	order []string
	byID  map[string]*ProviderPayload
	notes map[string]string
}

func newNanobotProviders(env Env) *nanobotProviders {
	return &nanobotProviders{env: env, byID: map[string]*ProviderPayload{}, notes: map[string]string{}}
}

// add records p and returns the id it ended up with.
func (s *nanobotProviders) add(p ProviderPayload, note string) string {
	if cur, ok := s.byID[p.ID]; ok {
		if cur.APIKey == p.APIKey && strings.TrimRight(cur.BaseURL, "/") == strings.TrimRight(p.BaseURL, "/") {
			for _, m := range p.Models {
				s.addModel(p.ID, m)
			}
			return p.ID
		}
		p.ID = NextFreeName(p.ID, func(id string) bool { _, used := s.byID[id]; return used })
	}
	cp := p
	cp.Models = nil
	s.byID[p.ID] = &cp
	s.order = append(s.order, p.ID)
	for _, m := range p.Models {
		s.addModel(p.ID, m)
	}
	if note != "" {
		s.notes[p.ID] = note
	}
	return p.ID
}

func (s *nanobotProviders) addModel(id, model string) {
	p := s.byID[id]
	if p == nil || model == "" {
		return
	}
	for _, m := range p.Models {
		if m == model {
			return
		}
	}
	p.Models = append(p.Models, model)
}

func (s *nanobotProviders) items() []Item {
	var out []Item
	for _, id := range s.order {
		p := *s.byID[id]
		detail := ""
		if n := len(p.Models); n == 1 {
			detail = p.Models[0]
		} else if n > 1 {
			detail = fmt.Sprintf("%d models", n)
		}
		it := ProviderItem(s.env, p, detail)
		if note := s.notes[id]; note != "" && it.Status == StatusNeedsInput {
			it.Reason = note
		}
		out = append(out, it)
	}
	return out
}

// nanobotKnowledge turns every Markdown file under dir (recursively) into a
// knowledge item; rel paths are prefixed with prefix. skip filters files.
func nanobotKnowledge(env Env, dir, prefix string, skip func(rel string) bool) []Item {
	if !IsDir(dir) {
		return nil
	}
	var out []Item
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if skip != nil && skip(rel) {
			return nil
		}
		if it, ok := KnowledgeItem(env, prefix+rel, ReadText(p)); ok {
			out = append(out, it)
		}
		return nil
	})
	return out
}
