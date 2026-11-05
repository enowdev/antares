package migrate

// CowAgent (zhayujie/CowAgent, formerly chatgpt-on-wechat) —
// https://github.com/zhayujie/CowAgent
//
// Format, verified against the project's own source at commit 74cc1a7
// (2026-10-03):
//
//   - config.json lives in the data root: $COW_DATA_DIR (the desktop build
//     uses ~/.cow) or, for a source checkout, the checkout itself
//     (config.py get_data_root, load_config). Keys are flat
//     (config.py available_setting, config-template.json): one
//     <vendor>_api_key / <vendor>_api_base pair per vendor, "model",
//     "bot_type" ("custom:<id>" selects custom_providers[]), "use_linkai",
//     "channel_type" (comma list or array) and per-channel tokens.
//     https://github.com/zhayujie/CowAgent/blob/master/config.py
//   - ~/.cow/.env and <workspace>/.env hold credentials too
//     (bridge/agent_initializer.py, common/state_dir.py env_file); a key there
//     fills an empty config value.
//   - Bot type from the model name: bridge/agent_bridge.py
//     _MODEL_PREFIX_MAP / _resolve_bot_type.
//   - Workspace "agent_workspace" (default ~/cow, agent/prompt/workspace.py):
//     AGENT.md (persona → soul), RULE.md (rules → AGENTS.md), USER.md,
//     MEMORY.md, memory/YYYY-MM-DD.md daily notes (+ users/, dreams/),
//     knowledge/, skills/, mcp.json ({"mcpServers":…}, else config
//     "mcp_servers"; agent/tools/mcp/service.py), scheduler/tasks.json
//     ({"tasks":{id:{name,enabled,schedule:{type:cron|interval|once,…},
//     action:{type:agent_task|send_message|tool_call,…}}}};
//     agent/tools/scheduler/), subagents/*.md (front matter name/description
//     + body; agent/subagent/assets/README.md). Unchanged templates (zh/en)
//     and placeholder-only files are skipped.
//   - Running: <data root>/.cow.pid (cli/commands/process.py).
//
// Not verified: the DashScope and MiniMax default endpoints (CowAgent uses
// their SDKs); LinkAI's OpenAI-compatible path (/v1); chat_fallback chains and
// multi-agent "agents" profiles are not imported.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type cowagentSource struct{}

func init() { Register(cowagentSource{}) }

func (cowagentSource) ID() string   { return "cowagent" }
func (cowagentSource) Name() string { return "CowAgent" }

func (s cowagentSource) Detect(ctx context.Context, root string) (Detection, error) {
	var dir string
	switch {
	case root != "":
		dir = ExpandHome(root)
		if !IsFile(filepath.Join(dir, "config.json")) && !IsFile(filepath.Join(dir, "AGENT.md")) {
			return Detection{}, ErrNotFound
		}
	default:
		for _, c := range []string{os.Getenv("COW_DATA_DIR"), filepath.Join(HomeDir(), ".cow")} {
			if c != "" && IsFile(filepath.Join(ExpandHome(c), "config.json")) {
				dir = ExpandHome(c)
				break
			}
		}
		if dir == "" && IsFile(filepath.Join(HomeDir(), "cow", "AGENT.md")) {
			dir = filepath.Join(HomeDir(), "cow") // a workspace without a desktop data dir
		}
		if dir == "" {
			return Detection{}, ErrNotFound
		}
	}
	det := Detection{Source: s.ID(), Name: s.Name(), Root: dir}
	det.Running = nanobotPIDAlive(filepath.Join(dir, ".cow.pid")) || RunningCheck([]string{`CowAgent`}, nil)
	items, _ := cowagentBuild(det, nil)
	det.Summary = Summarize(items)
	return det, nil
}

func (s cowagentSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	items, warnings := cowagentBuild(det, env)
	det.Summary = Summarize(items)
	return Plan{Detection: det, Items: items, Warnings: warnings}, nil
}

// cowagentVendor is one flat-key vendor in CowAgent's config.
type cowagentVendor struct {
	bot, keyField, baseField, defBase, vendor, label, kind string
}

// cowagentVendors lists the vendors with an API key in available_setting,
// keyed by the bot type that uses them (common/const.py).
var cowagentVendors = []cowagentVendor{
	{"openai", "open_ai_api_key", "open_ai_api_base", "https://api.openai.com/v1", "openai", "OpenAI", "openai-compatible"},
	{"claudeAPI", "claude_api_key", "claude_api_base", "https://api.anthropic.com/v1", "anthropic", "Anthropic", "anthropic"},
	{"gemini", "gemini_api_key", "gemini_api_base", "https://generativelanguage.googleapis.com", "gemini", "Google Gemini", "gemini"},
	{"deepseek", "deepseek_api_key", "deepseek_api_base", "https://api.deepseek.com/v1", "deepseek", "DeepSeek", "openai-compatible"},
	{"zhipu", "zhipu_ai_api_key", "zhipu_ai_api_base", "https://open.bigmodel.cn/api/paas/v4", "zhipu", "Zhipu AI", "openai-compatible"},
	{"moonshot", "moonshot_api_key", "moonshot_base_url", "https://api.moonshot.cn/v1", "moonshot", "Moonshot (Kimi)", "openai-compatible"},
	{"doubao", "ark_api_key", "ark_base_url", "https://ark.cn-beijing.volces.com/api/v3", "doubao", "Doubao (Volcengine Ark)", "openai-compatible"},
	{"dashscope", "dashscope_api_key", "", "https://dashscope.aliyuncs.com/compatible-mode/v1", "dashscope", "Qwen (DashScope)", "openai-compatible"},
	{"minimax", "minimax_api_key", "Minimax_base_url", "https://api.minimaxi.com/v1", "minimax", "MiniMax", "openai-compatible"},
	{"qianfan", "qianfan_api_key", "qianfan_api_base", "https://qianfan.baidubce.com/v2", "qianfan", "Baidu Qianfan", "openai-compatible"},
	{"modelscope", "modelscope_api_key", "modelscope_base_url", "https://api-inference.modelscope.cn/v1", "modelscope", "ModelScope", "openai-compatible"},
	{"mimo", "mimo_api_key", "mimo_api_base", "https://api.xiaomimimo.com/v1", "mimo", "Xiaomi MiMo", "openai-compatible"},
	{"linkai", "linkai_api_key", "linkai_api_base", "https://api.link-ai.tech", "linkai", "LinkAI", "openai-compatible"},
	{"custom", "custom_api_key", "custom_api_base", "", "custom", "CowAgent custom provider", "openai-compatible"},
}

// cowagentBotAliases maps bot types to the vendor table's bot names.
var cowagentBotAliases = map[string]string{
	"chatGPT": "openai", "openAI": "openai", "openai": "openai", "qwen": "dashscope", "MiniMax": "minimax",
}

func cowagentBuild(det Detection, env Env) ([]Item, []string) {
	root := det.Root
	var warnings []string
	cfg := map[string]any{}
	hasConfig := IsFile(filepath.Join(root, "config.json"))
	if hasConfig {
		if err := ReadJSON(filepath.Join(root, "config.json"), &cfg); err != nil {
			warnings = append(warnings, "config.json could not be read: "+err.Error())
		}
	}
	ws := nanobotStr(cfg, "agent_workspace")
	switch {
	case ws != "":
		ws = ExpandHome(ws)
	case !hasConfig:
		ws = root
	default:
		ws = filepath.Join(HomeDir(), "cow")
	}
	// .env files fill empty config values (lower-cased names match config
	// keys, as config.py does for environment overrides).
	for _, envFile := range []string{filepath.Join(HomeDir(), ".cow", ".env"), filepath.Join(ws, ".env")} {
		for k, v := range ReadDotEnv(envFile) {
			lk := strings.ToLower(k)
			if lk == "minimax_base_url" {
				lk = "Minimax_base_url"
			}
			if v != "" && nanobotStr(cfg, lk) == "" {
				cfg[lk] = v
			}
		}
	}
	str := func(k string) string {
		v, _ := cfg[k].(string)
		return strings.TrimSpace(v)
	}

	var items, unsupported []Item
	provs := newNanobotProviders(env)
	botToID := map[string]string{}
	azure, _ := cfg["use_azure_chatgpt"].(bool)
	for _, v := range cowagentVendors {
		key := str(v.keyField)
		if key == "" {
			continue
		}
		if v.bot == "openai" && azure {
			unsupported = append(unsupported, UnsupportedItem(CatProvider, "azure", "Azure OpenAI", "add Azure OpenAI in Antares with its deployment and API version"))
			continue
		}
		base := ""
		if v.baseField != "" {
			base = str(v.baseField)
		}
		if base == "" {
			base = v.defBase
		}
		base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/chat/completions")
		if v.bot == "linkai" && !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		if v.bot == "custom" && base == "" {
			unsupported = append(unsupported, UnsupportedItem(CatProvider, "custom", v.label, "the custom provider has no API base"))
			continue
		}
		p := nanobotIdentity(v.vendor, v.label, v.kind, base)
		p.APIKey = key
		botToID[v.bot] = provs.add(p, "")
	}
	for _, raw := range nanobotList(cfg, "custom_providers") {
		cp, ok := raw.(map[string]any)
		if !ok || nanobotStr(cp, "id") == "" {
			continue
		}
		name := nanobotStr(cp, "name")
		if name == "" {
			name = "custom " + nanobotStr(cp, "id")
		}
		base := nanobotStr(cp, "api_base")
		if base == "" {
			unsupported = append(unsupported, UnsupportedItem(CatProvider, Slug(name), name, "the custom provider has no API base"))
			continue
		}
		p := nanobotIdentity(name, name, "openai-compatible", base)
		p.APIKey = nanobotStr(cp, "api_key")
		if m := nanobotStr(cp, "model"); m != "" {
			p.Models = []string{m}
		}
		botToID["custom:"+nanobotStr(cp, "id")] = provs.add(p, "")
	}
	for _, legacy := range []struct{ key, title string }{
		{"baidu_wenxin_api_key", "Baidu Wenxin (legacy)"}, {"xunfei_api_key", "iFlytek Spark"}, {"qwen_access_key_id", "Qwen (legacy access key)"},
	} {
		if str(legacy.key) != "" {
			unsupported = append(unsupported, UnsupportedItem(CatProvider, Slug(legacy.title), legacy.title, "signature-based login Antares does not support; use an API key provider"))
		}
	}

	// Default model.
	var modelItems []Item
	if model := str("model"); model != "" && hasConfig {
		bot := str("bot_type")
		if b, _ := cfg["use_linkai"].(bool); b && str("linkai_api_key") != "" {
			bot = "linkai"
		}
		if bot == "" {
			bot = cowagentBotForModel(model)
		}
		if a := cowagentBotAliases[bot]; a != "" {
			bot = a
		}
		if id := botToID[bot]; id != "" {
			provs.addModel(id, model)
			modelItems = append(modelItems, ModelItem(env, ModelPayload{Provider: id, Model: model}))
		}
	}
	items = append(items, provs.items()...)
	items = append(items, unsupported...)
	items = append(items, modelItems...)

	// Persona files.
	for _, f := range []struct {
		cat  Category
		file string
	}{{CatSoul, "AGENT.md"}, {CatAgentsMD, "RULE.md"}, {CatUserMD, "USER.md"}} {
		text := ReadText(filepath.Join(ws, f.file))
		if text == "" || cowagentTemplates[ContentHash(text)] || cowagentPlaceholderOnly(text) {
			continue
		}
		if it, ok := TextItem(env, f.cat, text, "CowAgent "+f.file); ok {
			items = append(items, it)
		}
	}
	if text := ReadText(filepath.Join(ws, "MEMORY.md")); text != "" && !cowagentTemplates[ContentHash(text)] {
		items = append(items, MemoryItems(nanobotDropTemplateEntries(SplitMemory(text, ""), cowagentMemoryTemplateEntries), "global", "import:cowagent")...)
	}
	items = append(items, nanobotKnowledge(env, filepath.Join(ws, "memory"), "memory/", nil)...)
	items = append(items, nanobotKnowledge(env, filepath.Join(ws, "knowledge"), "knowledge/", nil)...)

	for _, sk := range ScanSkills(filepath.Join(ws, "skills"), 1) {
		items = append(items, SkillItem(env, sk))
	}

	items = append(items, cowagentMCP(env, ws, cfg)...)
	items = append(items, cowagentTasks(filepath.Join(ws, "scheduler", "tasks.json"))...)
	items = append(items, cowagentSubagents(env, filepath.Join(ws, "subagents"))...)
	items = append(items, cowagentChannels(env, cfg, str)...)
	return items, warnings
}

// cowagentBotForModel mirrors AgentBridge._resolve_bot_type for a model name.
func cowagentBotForModel(model string) string {
	switch model {
	case "wenxin", "wenxin-4":
		return "baidu"
	case "xunfei":
		return "xunfei"
	case "qwen", "qwen-turbo", "qwen-plus", "qwen-max":
		return "dashscope"
	case "qianfan":
		return "qianfan"
	case "modelscope":
		return "modelscope"
	case "moonshot", "moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k":
		return "moonshot"
	}
	l := strings.ToLower(model)
	if strings.HasPrefix(l, "minimax") || model == "abab6.5-chat" {
		return "minimax"
	}
	for _, p := range []struct{ prefix, bot string }{
		{"qwen", "dashscope"}, {"qwq", "dashscope"}, {"qvq", "dashscope"}, {"gemini", "gemini"}, {"glm", "zhipu"},
		{"claude", "claudeAPI"}, {"moonshot", "moonshot"}, {"kimi", "moonshot"}, {"doubao", "doubao"},
		{"deepseek", "deepseek"}, {"ernie", "qianfan"}, {"mimo-", "mimo"},
	} {
		if strings.HasPrefix(l, p.prefix) {
			return p.bot
		}
	}
	return "openai"
}

// cowagentTemplates are ContentHash values of the zh/en AGENT.md, USER.md,
// RULE.md, MEMORY.md and BOOTSTRAP.md templates in agent/prompt/workspace.py
// at 74cc1a7 (current version only; earlier versions not checked).
var cowagentTemplates = nanobotHashSet(`
	5023ec8078d5 52223b717854 5b8b0a4051dc 5cadd547c9a8 6c56ab30c9f0 6e3721a69e0c 83b7177df58c
	b1c6155b32f0 d071ae837626 fe83cb6e7275`)

// cowagentMemoryTemplateEntries are the entries of the MEMORY.md templates.
// It is empty: both (zh/en) are only a heading, an italic note and a rule,
// which nanobotPlaceholder already drops.
var cowagentMemoryTemplateEntries = map[string]bool{}

// cowagentPlaceholderOnly mirrors workspace.py _is_template_placeholder: at
// most three non-heading lines, one of them a template placeholder.
func cowagentPlaceholderOnly(content string) bool {
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			lines = append(lines, t)
		}
	}
	if len(lines) > 3 {
		return false
	}
	for _, l := range lines {
		for _, p := range []string{"*(填写", "*(在首次对话时填写", "*(可选)", "*(根据需要添加", "*(filled during", "*(ask during", "*(optional)", "*(how the user"} {
			if strings.Contains(l, p) {
				return true
			}
		}
	}
	return false
}

func cowagentMCP(env Env, ws string, cfg map[string]any) []Item {
	var raw any
	if p := filepath.Join(ws, "mcp.json"); IsFile(p) {
		var data map[string]any
		if ReadJSON(p, &data) == nil {
			raw = data["mcpServers"]
			if raw == nil {
				raw = data["mcp_servers"]
			}
			if raw == nil {
				raw = data
			}
		}
	} else {
		raw = cfg["mcp_servers"]
	}
	entries := map[string]map[string]any{}
	switch v := raw.(type) {
	case map[string]any:
		for name, e := range v {
			if m, ok := e.(map[string]any); ok {
				entries[name] = m
			}
		}
	case []any:
		for _, e := range v {
			if m, ok := e.(map[string]any); ok && nanobotStr(m, "name") != "" {
				entries[nanobotStr(m, "name")] = m
			}
		}
	}
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Item
	for _, name := range names {
		m := entries[name]
		t := nanobotStr(m, "type")
		if t == "" {
			t = nanobotStr(m, "transport")
		}
		it := MCPItem(env, MCPPayload{
			Name: name, Transport: t, Command: nanobotStr(m, "command"), Args: nanobotStrings(m["args"]),
			Env: nanobotStringMap(m, "env"), URL: nanobotStr(m, "url"), Headers: nanobotStringMap(m, "headers"),
		})
		if !nanobotBool(m, "enabled", true) {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in CowAgent", " · ")
		}
		out = append(out, it)
	}
	return out
}

func cowagentTasks(path string) []Item {
	var store struct {
		Tasks map[string]map[string]any `json:"tasks"`
	}
	if !IsFile(path) || ReadJSON(path, &store) != nil {
		return nil
	}
	ids := make([]string, 0, len(store.Tasks))
	for id := range store.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Item
	for _, id := range ids {
		t := store.Tasks[id]
		key := Slug(id)
		name := nanobotStr(t, "name")
		if name == "" {
			name = "task " + id
		}
		sched, action := nanobotMap(t, "schedule"), nanobotMap(t, "action")
		var expr string
		switch nanobotStr(sched, "type") {
		case "cron":
			expr = nanobotCronExpr(nanobotStr(sched, "expression"))
		case "interval":
			secs := nanobotInt(sched, "seconds", 0)
			if secs < 60 {
				out = append(out, UnsupportedItem(CatCron, key, name, "intervals under a minute are not supported"))
				continue
			}
			expr = EverySchedule(time.Duration(secs) * time.Second)
		case "once":
			out = append(out, UnsupportedItem(CatCron, key, name, "one-shot schedule"))
			continue
		default:
			out = append(out, UnsupportedItem(CatCron, key, name, "unknown schedule type"))
			continue
		}
		var prompt string
		switch nanobotStr(action, "type") {
		case "agent_task":
			prompt = nanobotStr(action, "task_description")
		case "send_message":
			if c := nanobotStr(action, "content"); c != "" {
				prompt = "Send me this reminder, word for word:\n\n" + c
			}
		default:
			out = append(out, UnsupportedItem(CatCron, key, name, "the task calls a CowAgent tool directly, not a prompt"))
			continue
		}
		out = append(out, CronItem(key, CronPayload{Name: name, Schedule: expr, Prompt: prompt}, nanobotBool(t, "enabled", true)))
	}
	return out
}

func cowagentSubagents(env Env, dir string) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Item
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || strings.EqualFold(n, "README.md") {
			continue
		}
		text := strings.ReplaceAll(ReadText(filepath.Join(dir, n)), "\r\n", "\n")
		name, desc := skillFrontMatter(filepath.Join(dir, n))
		if name == "" {
			name = strings.TrimSuffix(n, ".md")
		}
		out = append(out, RoleItem(env, RolePayload{Name: name, Title: name, Summary: desc, Prompt: stripFrontMatter(text)}))
	}
	return out
}

func cowagentChannels(env Env, cfg map[string]any, str func(string) string) []Item {
	active := map[string]bool{}
	for _, c := range nanobotStrings(cfg["channel_type"]) {
		active[strings.ToLower(c)] = true
	}
	var out []Item
	add := func(platform string, on bool, fields map[string]any, required []string, detail string) {
		hasCred := false
		missing := ""
		for k, v := range fields {
			if v == "" {
				delete(fields, k)
			}
		}
		for _, r := range required {
			if fields[r] == nil {
				if missing == "" {
					missing = r
				}
			} else {
				hasCred = true
			}
		}
		if !on && !hasCred {
			return
		}
		it := ChannelItem(env, platform, "", fields, missing)
		if detail != "" {
			it.Detail = detail
		}
		if !on {
			it.Detail = strings.TrimPrefix(it.Detail+" · not enabled in CowAgent", " · ")
		}
		out = append(out, it)
	}
	add("telegram", active["telegram"], map[string]any{"bot_token": str("telegram_token")}, []string{"bot_token"}, "")
	add("discord", active["discord"], map[string]any{"bot_token": str("discord_token")}, []string{"bot_token"}, "")
	add("slack", active["slack"], map[string]any{"bot_token": str("slack_bot_token"), "app_token": str("slack_app_token")}, []string{"bot_token", "app_token"}, "")
	feishuDetail := ""
	if m := str("feishu_event_mode"); m == "" || m == "websocket" {
		feishuDetail = "CowAgent used Feishu's long connection; Antares receives Feishu events on a webhook"
	}
	add("feishu", active["feishu"], map[string]any{"app_id": str("feishu_app_id"), "app_secret": str("feishu_app_secret"), "verify_token": str("feishu_token")}, []string{"app_id", "app_secret"}, feishuDetail)

	// Channels Antares has no gateway for, listed when enabled.
	seen := map[string]bool{}
	for _, c := range []struct{ typ, key, label string }{
		{"dingtalk", "dingtalk", "DingTalk"}, {"wecom_bot", "wecom", "WeCom"}, {"wechatcom_app", "wecom", "WeCom"},
		{"weixin", "wechat", "WeChat"}, {"wechatmp", "wechat", "WeChat"}, {"wechatmp_service", "wechat", "WeChat"},
		{"wechat_kf", "wechat", "WeChat"}, {"wx", "wechat", "WeChat"},
	} {
		if active[c.typ] && !seen[c.key] {
			seen[c.key] = true
			out = append(out, UnsupportedItem(CatChannel, c.key, c.label, "Antares has no "+c.label+" gateway"))
		}
	}
	return out
}
