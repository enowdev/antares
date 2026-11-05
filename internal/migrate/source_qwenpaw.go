package migrate

// QwenPaw (agentscope-ai/QwenPaw, formerly CoPaw) —
// https://github.com/agentscope-ai/QwenPaw
//
// Format, verified against the project's own source at commit 80e412d
// (2026-10-03, version 2.2.2b4):
//
//   - Working dir: $QWENPAW_WORKING_DIR, else ~/.copaw when it exists (legacy
//     install), else ~/.qwenpaw. Secrets: $QWENPAW_SECRET_DIR, else
//     "<working dir>.secret" (src/qwenpaw/constant.py).
//     https://github.com/agentscope-ai/QwenPaw/blob/main/src/qwenpaw/constant.py
//   - Providers: <secret>/providers/{builtin,custom}/<id>.json — a Provider
//     model dump {id, name, base_url, api_key, chat_model, models,
//     extra_models} with api_key stored as "ENC:<Fernet token>"; the active
//     model in <secret>/providers/active_model.json {provider_id, model}
//     (providers/provider_manager.py, provider_persistence.py). Older installs
//     keep <secret>/providers.json {providers, custom_providers, active_llm}.
//   - Fernet master key (security/secret_store.py): 32 random bytes, hex, in
//     the OS keychain under service "qwenpaw" (legacy "copaw"), account
//     "master_key" — or "master_key:<sha256(secret dir)[:16]>" when the
//     working/secret dir was relocated by env, or $QWENPAW_KEYRING_ACCOUNT —
//     else <secret>/.master_key. Fernet key = urlsafe_b64(raw[:32]), i.e.
//     signing key raw[:16], AES-128-CBC key raw[16:32]. The file is tried
//     first (no prompt); the keychain read goes through DefaultKeychain. If no
//     key decrypts a value the provider is needs_input.
//   - config.json: agents.{active_agent, profiles.<id>.workspace_dir};
//     channels / mcp.clients at the root (legacy) or in each workspace's
//     agent.json, which also has active_model, fallback_models, heartbeat
//     {enabled, every}, name, description (config/config.py).
//   - Workspace (default <working dir>/workspaces/default): AGENTS.md, SOUL.md,
//     PROFILE.md (identity + user profile → USER.md), MEMORY.md, memory/*.md,
//     skills/, jobs.json {version, jobs:[{name, enabled, schedule:{type:
//     cron|once, cron, timezone}, task_type: agent|text, request.input, text}]}
//     (app/crons/models.py), HEARTBEAT.md. Bundled md templates (agents/md_files,
//     all languages) are skipped when unchanged; their YAML front matter is
//     stripped.
//
// Not verified: the process name used for Running ("qwenpaw app" / "copaw
// app"); provider keys kept only in <working dir>/.env are not imported.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type qwenpawSource struct{}

func init() { Register(qwenpawSource{}) }

func (qwenpawSource) ID() string   { return "qwenpaw" }
func (qwenpawSource) Name() string { return "QwenPaw" }

func (s qwenpawSource) Detect(ctx context.Context, root string) (Detection, error) {
	var cands []string
	if root != "" {
		cands = []string{ExpandHome(root)}
	} else {
		if d := os.Getenv("QWENPAW_WORKING_DIR"); d != "" {
			cands = append(cands, ExpandHome(d))
		}
		cands = append(cands, filepath.Join(HomeDir(), ".copaw"), filepath.Join(HomeDir(), ".qwenpaw"))
	}
	for _, dir := range cands {
		if IsFile(filepath.Join(dir, "config.json")) || IsDir(filepath.Join(dir, "workspaces")) {
			det := Detection{Source: s.ID(), Name: s.Name(), Root: dir}
			det.Running = RunningCheck([]string{`(qwenpaw|copaw) app`}, nil)
			items, _ := qwenpawBuild(det, nil, false)
			det.Summary = Summarize(items)
			return det, nil
		}
	}
	return Detection{}, ErrNotFound
}

func (s qwenpawSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	items, warnings := qwenpawBuild(det, env, true)
	det.Summary = Summarize(items)
	return Plan{Detection: det, Items: items, Warnings: warnings}, nil
}

// qwenpawSecretDir mirrors constant.py SECRET_DIR.
func qwenpawSecretDir(root string) string {
	if d := os.Getenv("QWENPAW_SECRET_DIR"); d != "" {
		return ExpandHome(d)
	}
	return filepath.Clean(root) + ".secret"
}

func qwenpawBuild(det Detection, env Env, decrypt bool) ([]Item, []string) {
	root := det.Root
	secret := qwenpawSecretDir(root)
	var warnings []string
	cfg := map[string]any{}
	if p := filepath.Join(root, "config.json"); IsFile(p) {
		if err := ReadJSON(p, &cfg); err != nil {
			warnings = append(warnings, "config.json could not be read: "+err.Error())
		}
	}
	keys := &qwenpawKeys{secretDir: secret, enabled: decrypt}

	// Agents: the active one is the main import; the others become roles.
	agents := nanobotMap(cfg, "agents")
	profiles := nanobotMap(agents, "profiles")
	type agentRef struct{ id, ws string }
	var refs []agentRef
	for id, raw := range profiles {
		pm, _ := raw.(map[string]any)
		ws := nanobotStr(pm, "workspace_dir")
		if ws == "" {
			ws = filepath.Join(root, "workspaces", id)
		}
		if !nanobotBool(pm, "enabled", true) {
			continue
		}
		refs = append(refs, agentRef{id, ExpandHome(ws)})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].id < refs[j].id })
	if len(refs) == 0 {
		refs = []agentRef{{"default", filepath.Join(root, "workspaces", "default")}}
	}
	mainID := nanobotStr(agents, "active_agent")
	if mainID == "" {
		mainID = "default"
	}
	main := refs[0]
	for _, r := range refs {
		if r.id == mainID {
			main = r
		}
	}
	agentCfg := map[string]any{}
	if p := filepath.Join(main.ws, "agent.json"); IsFile(p) {
		_ = ReadJSON(p, &agentCfg)
	}
	ws := main.ws

	var items []Item
	provs := newNanobotProviders(env)

	// Providers.
	type provFile struct {
		data   map[string]any
		custom bool
	}
	var files []provFile
	for _, sub := range []string{"builtin", "custom"} {
		matches, _ := filepath.Glob(filepath.Join(secret, "providers", sub, "*.json"))
		sort.Strings(matches)
		for _, m := range matches {
			var d map[string]any
			if ReadJSON(m, &d) == nil && d != nil {
				if nanobotStr(d, "id") == "" {
					d["id"] = strings.TrimSuffix(filepath.Base(m), ".json")
				}
				files = append(files, provFile{d, sub == "custom"})
			}
		}
	}
	active := map[string]any{}
	_ = ReadJSON(filepath.Join(secret, "providers", "active_model.json"), &active)
	if legacy := filepath.Join(secret, "providers.json"); len(files) == 0 && IsFile(legacy) {
		var l map[string]any
		if ReadJSON(legacy, &l) == nil {
			for _, group := range []struct {
				key    string
				custom bool
			}{{"providers", false}, {"custom_providers", true}} {
				m := nanobotMap(l, group.key)
				ids := make([]string, 0, len(m))
				for id := range m {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					if d, ok := m[id].(map[string]any); ok {
						d["id"] = id
						files = append(files, provFile{d, group.custom})
					}
				}
			}
			if len(active) == 0 {
				active = nanobotMap(l, "active_llm")
			}
		}
	}
	if am := nanobotMap(agentCfg, "active_model"); nanobotStr(am, "provider_id") != "" {
		active = am
	}
	activeProv, activeModel := nanobotStr(active, "provider_id"), nanobotStr(active, "model")
	srcToID := map[string]string{}
	for _, f := range files {
		d := f.data
		id := nanobotStr(d, "id")
		rawKey := nanobotStr(d, "api_key")
		base := nanobotStr(d, "base_url")
		if base == "" {
			base = qwenpawDefaultBase[id]
		}
		isActive := id == activeProv
		if rawKey == "" && !isActive && !(IsLocalURL(base) && nanobotBool(d, "enabled", false)) {
			continue
		}
		label := nanobotStr(d, "name")
		if label == "" {
			label = id
		}
		switch {
		case id == "azure-openai":
			items = append(items, UnsupportedItem(CatProvider, "azure", "Azure OpenAI", "add Azure OpenAI in Antares with its deployment and API version"))
			continue
		case id == "qwenpaw-local" || id == "copaw-local":
			items = append(items, UnsupportedItem(CatProvider, Slug(id), label, "a local model runtime managed by QwenPaw"))
			continue
		case base == "":
			items = append(items, UnsupportedItem(CatProvider, Slug(id), label, "the provider has no API base"))
			continue
		}
		key, note := keys.decrypt(rawKey)
		kind := "openai-compatible"
		switch nanobotStr(d, "chat_model") {
		case "AnthropicChatModel":
			kind = "anthropic"
		case "GeminiChatModel":
			kind = "gemini"
		}
		vendor := id
		if v := qwenpawVendorAlias[id]; v != "" {
			vendor = v
		}
		if f.custom {
			vendor = label
		}
		p := nanobotIdentity(vendor, label, kind, base)
		p.APIKey = key
		for _, m := range nanobotList(d, "extra_models") {
			if mm, ok := m.(map[string]any); ok {
				if mid := nanobotStr(mm, "id"); mid != "" {
					p.Models = append(p.Models, mid)
				}
			}
		}
		if isActive && activeModel != "" {
			p.Models = append([]string{activeModel}, p.Models...)
		}
		srcToID[id] = provs.add(p, note)
	}
	var modelItems []Item
	if pid := srcToID[activeProv]; pid != "" && activeModel != "" {
		mp := ModelPayload{Provider: pid, Model: activeModel}
		for _, raw := range nanobotList(agentCfg, "fallback_models") {
			fm, _ := raw.(map[string]any)
			if fid := srcToID[nanobotStr(fm, "provider_id")]; fid != "" && nanobotStr(fm, "model") != "" {
				mp.Fallback = append(mp.Fallback, fid+"/"+nanobotStr(fm, "model"))
				provs.addModel(fid, nanobotStr(fm, "model"))
			}
		}
		modelItems = append(modelItems, ModelItem(env, mp))
	}
	items = append(items, provs.items()...)
	items = append(items, modelItems...)
	if keys.failed && keys.err != "" {
		warnings = append(warnings, "Some QwenPaw keys could not be decrypted ("+keys.err+"); enter them by hand.")
	}

	// Persona files.
	for _, f := range []struct {
		cat  Category
		file string
	}{{CatSoul, "SOUL.md"}, {CatAgentsMD, "AGENTS.md"}, {CatUserMD, "PROFILE.md"}} {
		text := ReadText(filepath.Join(ws, f.file))
		if text == "" || qwenpawTemplates[ContentHash(text)] {
			continue
		}
		if it, ok := TextItem(env, f.cat, stripFrontMatter(strings.ReplaceAll(text, "\r\n", "\n")), "QwenPaw "+f.file); ok {
			items = append(items, it)
		}
	}
	if text := ReadText(filepath.Join(ws, "MEMORY.md")); text != "" && !qwenpawTemplates[ContentHash(text)] {
		items = append(items, MemoryItems(nanobotDropTemplateEntries(SplitMemory(text, ""), qwenpawMemoryTemplateEntries), "global", "import:qwenpaw")...)
	}
	items = append(items, nanobotKnowledge(env, filepath.Join(ws, "memory"), "memory/", nil)...)
	builtin := qwenpawBuiltinSkills(ws)
	for _, sk := range ScanSkills(filepath.Join(ws, "skills"), 1) {
		if builtin(filepath.Base(sk.Dir)) {
			continue
		}
		items = append(items, SkillItem(env, sk))
	}

	// MCP: the agent's own clients, else the legacy root ones.
	mcp := nanobotMap(nanobotMap(agentCfg, "mcp"), "clients")
	if mcp == nil {
		mcp = nanobotMap(nanobotMap(cfg, "mcp"), "clients")
	}
	names := make([]string, 0, len(mcp))
	for k := range mcp {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		m, ok := mcp[k].(map[string]any)
		if !ok {
			continue
		}
		name := nanobotStr(m, "name")
		if name == "" {
			name = k
		}
		t := nanobotStr(m, "transport")
		if t == "" {
			t = nanobotStr(m, "type")
		}
		it := MCPItem(env, MCPPayload{
			Name: Slug(name), Transport: t, Command: nanobotStr(m, "command"), Args: nanobotStrings(m["args"]),
			Env: keys.decryptMap(nanobotStringMap(m, "env")), URL: nanobotStr(m, "url"), Headers: keys.decryptMap(nanobotStringMap(m, "headers")),
		})
		if !nanobotBool(m, "enabled", true) {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in QwenPaw", " · ")
		}
		items = append(items, it)
	}

	// Schedules.
	items = append(items, qwenpawJobs(filepath.Join(ws, "jobs.json"))...)
	if main.id == "default" {
		items = append(items, qwenpawJobs(filepath.Join(root, "jobs.json"))...)
	}
	hb := nanobotMap(agentCfg, "heartbeat")
	if hb == nil {
		hb = nanobotMap(nanobotMap(agents, "defaults"), "heartbeat")
	}
	if text := ReadText(filepath.Join(ws, "HEARTBEAT.md")); text != "" && !qwenpawTemplates[ContentHash(text)] {
		every := nanobotStr(hb, "every")
		if every == "" {
			every = "6h"
		}
		if f := strings.Fields(every); len(f) == 5 {
			if tasks := nanobotHeartbeatTasks(stripFrontMatter(text), ""); tasks != "" {
				prompt := "Heartbeat check. Review these recurring tasks and act on any that need attention; if nothing does, reply only HEARTBEAT_OK.\n\n" + tasks
				items = append(items, CronItem("heartbeat", CronPayload{Name: "Heartbeat", Schedule: nanobotCronExpr(every), Prompt: prompt}, nanobotBool(hb, "enabled", false)))
			}
		} else {
			d, err := time.ParseDuration(every)
			if err != nil || d <= 0 {
				d = 30 * time.Minute
			}
			if it, ok := nanobotHeartbeatItem(stripFrontMatter(text), "", d, nanobotBool(hb, "enabled", false)); ok {
				items = append(items, it)
			}
		}
	}

	// Other agents → roles.
	for _, r := range refs {
		if r.id == main.id || qwenpawBuiltinAgents[r.id] {
			continue
		}
		var ac map[string]any
		_ = ReadJSON(filepath.Join(r.ws, "agent.json"), &ac)
		var parts []string
		for _, f := range []string{"SOUL.md", "AGENTS.md"} {
			if t := ReadText(filepath.Join(r.ws, f)); t != "" && !qwenpawTemplates[ContentHash(t)] {
				parts = append(parts, stripFrontMatter(strings.ReplaceAll(t, "\r\n", "\n")))
			}
		}
		model := ""
		if am := nanobotMap(ac, "active_model"); am != nil {
			if fid := srcToID[nanobotStr(am, "provider_id")]; fid != "" && nanobotStr(am, "model") != "" {
				model = fid + "/" + nanobotStr(am, "model")
			}
		}
		if len(parts) == 0 {
			continue // only shipped template files: nothing the user wrote
		}
		title := nanobotStr(ac, "name")
		items = append(items, RoleItem(env, RolePayload{Name: r.id, Title: title, Summary: nanobotStr(ac, "description"),
			Prompt: strings.TrimSpace(strings.Join(parts, "\n\n")), Model: model}))
	}

	// Channels: the agent's, else the legacy root block.
	ch := nanobotMap(agentCfg, "channels")
	if ch == nil {
		ch = nanobotMap(cfg, "channels")
	}
	items = append(items, qwenpawChannels(env, ch, keys)...)
	return items, warnings
}

// qwenpawBuiltinAgents are the agents QwenPaw creates itself
// (constant.py BUILTIN_QA_AGENT_ID, LEGACY_QA_AGENT_ID).
var qwenpawBuiltinAgents = map[string]bool{"QwenPaw_QA_Agent_0.2": true, "CoPaw_QA_Agent_0.1beta1": true}

// qwenpawBuiltinSkillNames are the skill folders QwenPaw ships
// (src/qwenpaw/agents/skills/<name>-{en,zh} plus pool skills seen on real
// installs), used only when a workspace has no skill.json manifest.
var qwenpawBuiltinSkillNames = map[string]bool{
	"browser": true, "browser_cdp": true, "browser_visible": true, "channel_message": true, "chat_with_agent": true,
	"cron": true, "dingtalk_channel": true, "docx": true, "file_reader": true, "guidance": true, "himalaya": true,
	"mailbox": true, "make_plan": true, "make-skill": true, "multi_agent_collaboration": true, "news": true,
	"pdf": true, "pptx": true, "QA_source_index": true, "xlsx": true,
}

// qwenpawBuiltinSkills reports whether a workspace skill folder is one QwenPaw
// installed itself. The workspace's skill.json manifest records each skill's
// "source" ("builtin" for shipped skills, "customized" for user-made or
// imported ones; agents/skill_system/store.py, pool_service.py); without a
// manifest the shipped names are used.
func qwenpawBuiltinSkills(ws string) func(dir string) bool {
	var manifest struct {
		Skills map[string]map[string]any `json:"skills"`
	}
	if ReadJSON(filepath.Join(ws, "skill.json"), &manifest) != nil || manifest.Skills == nil {
		return func(dir string) bool { return qwenpawBuiltinSkillNames[dir] }
	}
	return func(dir string) bool {
		e, ok := manifest.Skills[dir]
		return ok && nanobotStr(e, "source") == "builtin"
	}
}

// qwenpawVendorAlias maps built-in provider ids (providers/data/index.json)
// to vendor names nanobotIdentity knows.
var qwenpawVendorAlias = map[string]string{
	"openai-response": "openai", "kimi-cn": "moonshot", "kimi-intl": "moonshot", "minimax-cn": "minimax",
	"zhipu-cn": "zhipu", "zhipu-cn-codingplan": "zhipu", "zhipu-intl": "zai", "zhipu-intl-codingplan": "zai",
	"lmstudio": "lmstudio",
}

// qwenpawDefaultBase are the built-in endpoints (providers/data/index.json
// api_urls), used when a stored provider has no base_url.
var qwenpawDefaultBase = map[string]string{
	"openrouter": "https://openrouter.ai/api/v1", "modelscope": "https://api-inference.modelscope.cn/v1",
	"dashscope":              "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"aliyun-codingplan":      "https://coding.dashscope.aliyuncs.com/v1",
	"aliyun-codingplan-intl": "https://coding-intl.dashscope.aliyuncs.com/v1",
	"aliyun-tokenplan":       "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
	"aliyun-tokenplan-intl":  "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
	"opencode":               "https://opencode.ai/zen/v1", "kilo": "https://api.kilo.ai/api/gateway",
	"openai": "https://api.openai.com/v1", "openai-response": "https://api.openai.com/v1",
	"anthropic": "https://api.anthropic.com", "gemini": "https://generativelanguage.googleapis.com",
	"deepseek": "https://api.deepseek.com", "kimi-cn": "https://api.moonshot.cn/v1", "kimi-intl": "https://api.moonshot.ai/v1",
	"kimi-codingplan": "https://api.kimi.com/coding/v1", "minimax-cn": "https://api.minimaxi.com/anthropic",
	"minimax": "https://api.minimax.io/anthropic", "zhipu-cn": "https://open.bigmodel.cn/api/paas/v4",
	"zhipu-cn-codingplan": "https://open.bigmodel.cn/api/coding/paas/v4", "zhipu-intl": "https://api.z.ai/api/paas/v4",
	"zhipu-intl-codingplan": "https://api.z.ai/api/coding/paas/v4", "siliconflow-cn": "https://api.siliconflow.cn/v1",
	"siliconflow-intl": "https://api.siliconflow.com/v1", "volcengine-cn": "https://ark.cn-beijing.volces.com/api/v3",
	"volcengine-cn-codingplan": "https://ark.cn-beijing.volces.com/api/coding/v3",
	"volcengine-cn-agentplan":  "https://ark.cn-beijing.volces.com/api/plan/v3",
	"mimo-tokenplan":           "https://token-plan-cn.xiaomimimo.com/v1",
	"mimo":                     "https://api.xiaomimimo.com/v1",
	"ollama":                   "http://localhost:11434/v1",
	"lmstudio":                 "http://localhost:1234/v1",
}

// qwenpawTemplates are ContentHash values of every version, in git history up
// to 80e412d (including the earlier src/copaw tree), of the bundled workspace
// files in agents/md_files/{en,zh,ru,id,local/*,qa/*} (AGENTS, SOUL, PROFILE,
// MEMORY, TOOLS, HEARTBEAT, BOOTSTRAP…). A real install's files matched older
// versions, so the current ones alone are not enough.
var qwenpawTemplates = nanobotHashSet(`
	000a8b0fcec3 00c20f460a3e 059a15bc472b 05f25a7968e8 0a1899629613 0e2643900c3a 1253c8d42dcd
	12c833b6c6e9 144fe035f56f 18c29d6ea6bb 1a289c303bcc 1cb811606e4a 1cd8bc4ee218 1da333d7107a
	25327a65cbb3 27e44025fbee 2a5fc0446f48 2e81a517e00d 2eca75691362 30a4c98e1bae 30f9fb726882
	327b514237c5 3abc0d32b505 3dea836ab304 3e1294b90bad 3e26560a3d03 3f9e7c727343 422c7c144d8e
	454277a38cbf 458edfb552be 4877bd748f07 4a140d6f7efe 4b670c3748fe 503a7853c66d 54cb4b284eeb
	56d1c7c0cc23 56d49b4a083c 5796a7f072e2 5baf70210528 5e41b93cd59d 60036d886441 696ab07ee8c6
	6a4deb979d17 6e0fd38ecefb 7203f222535a 72c06804e7b3 757586694464 7927ed0b549c 7c4e8113bfd4
	7cb74a9c66fc 80fb4aac9139 83c612c499e8 869dfed81d70 898ffaf69b4f 8a91f3aa90f2 8c2898fda618
	8cf579cc3d0a 8d25ba703e35 91c0c25c6c71 96973f800320 9ad6d4254bd9 9adb76af8835 9cf90907aad3
	9e317db91ae3 a59d5d200ba2 a8ddcb255f7a abe9c2c70d19 aef002e3e832 b14a58275750 bc255dcf61b3
	bc2fbf4352fa bd400f9e3bcd c85fef174bff d03456be002e d220d8213704 d2784ecb87d0 d5053c6283f7
	d57affa0ef70 d6561ee8e516 d8a387a247eb d8c06ea62deb df0208acc41e dfe8f0a63997 e03efc457ba8
	e076d3a50d52 e116d5ae5b97 e3aa4f8a87c8 e538ae25e706 e6766be33e18 e68da7419f7f e7e6236ef8e7
	ebb58cf3851a efd602afcfcb f00532759e6f f05e838f25e2 f06ea286c34f f5c78a82074f f6c574ca389f
	f987233ccc34`)

// qwenpawMemoryTemplateEntries are the entries (as split by SplitMemory) of
// every bundled MEMORY.md/TOOLS.md version, dropped from a user's file.
var qwenpawMemoryTemplateEntries = nanobotHashSet(`
	0b61c1ed7582 200e7943adce 28dcd34051fa 2d76f4ae5f35 4a8f96660e9d 50566440c1d9 56bb90531b58
	59b6806dbcbd 5f056428cebb 690b24df7764 78c8586ba5c1 7964c6178770 7cc16e877376 8a2d68288a62
	926edc474a06 92ca89593af1 950f6edfe497 a6b2bde3a032 ab6134dd0c4b af3c15b9562e b3fc3dbe0a47
	b7cf13998f15 b7e1f3009abc c8c426c13117 d13c3d38badc d67874d0556d e2637e1c0498`)

func qwenpawJobs(path string) []Item {
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
			id = "job-" + strconv.Itoa(i+1)
		}
		key := Slug(id)
		name := nanobotStr(j, "name")
		if name == "" {
			name = id
		}
		sched := nanobotMap(j, "schedule")
		if t := nanobotStr(sched, "type"); t == "once" {
			out = append(out, UnsupportedItem(CatCron, key, name, "one-shot schedule"))
			continue
		}
		var prompt string
		if nanobotStr(j, "task_type") == "text" {
			if t := nanobotStr(j, "text"); t != "" {
				prompt = "Send me this message, word for word:\n\n" + t
			}
		} else {
			prompt = qwenpawInputText(nanobotGet(nanobotMap(j, "request"), "input"))
		}
		c := CronPayload{Name: name, Schedule: nanobotCronExpr(nanobotStr(sched, "cron")), Prompt: prompt, Timezone: nanobotStr(sched, "timezone")}
		out = append(out, CronItem(key, c, nanobotBool(j, "enabled", true)))
	}
	return out
}

// qwenpawInputText extracts the text of a cron request input: a string, or
// AgentScope messages [{role, content: [{type: "text", text}]}].
func qwenpawInputText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		if t := nanobotStr(x, "text"); t != "" {
			return t
		}
		return qwenpawInputText(x["content"])
	case []any:
		var parts []string
		for _, e := range x {
			if t := qwenpawInputText(e); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func qwenpawChannels(env Env, ch map[string]any, keys *qwenpawKeys) []Item {
	names := make([]string, 0, len(ch))
	for k := range ch {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Item
	for _, name := range names {
		c, ok := ch[name].(map[string]any)
		if !ok {
			continue
		}
		enabled := nanobotBool(c, "enabled", false)
		get := func(k string) string { v, _ := keys.decrypt(nanobotStr(c, k)); return v }
		fields := map[string]any{}
		var required []string
		detail := ""
		switch name {
		case "telegram":
			fields["bot_token"], required = get("bot_token"), []string{"bot_token"}
		case "discord":
			fields["bot_token"], required = get("bot_token"), []string{"bot_token"}
		case "slack":
			fields["bot_token"], fields["app_token"], required = get("bot_token"), get("app_token"), []string{"bot_token", "app_token"}
		case "matrix":
			fields["homeserver"], fields["user_id"], fields["access_token"] = get("homeserver"), get("user_id"), get("access_token")
			required = []string{"access_token"}
		case "feishu":
			fields["app_id"], fields["app_secret"], fields["verify_token"] = get("app_id"), get("app_secret"), get("verification_token")
			required = []string{"app_id", "app_secret"}
			detail = "QwenPaw used Feishu's long connection; Antares receives Feishu events on a webhook"
		case "console":
			continue
		default:
			if enabled {
				label := qwenpawChannelLabels[name]
				if label == "" {
					label = PlatformLabel(name)
				}
				out = append(out, UnsupportedItem(CatChannel, Slug(name), label, "Antares has no "+label+" gateway"))
			}
			continue
		}
		missing := ""
		hasCred := false
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
		if !enabled && !hasCred {
			continue
		}
		if users := nanobotStrings(c["allow_from"]); len(users) > 0 {
			fields["allowed_users"] = users
		}
		it := ChannelItem(env, name, "", fields, missing)
		if detail != "" {
			it.Detail = detail
		}
		if !enabled {
			it.Detail = strings.TrimPrefix(it.Detail+" · disabled in QwenPaw", " · ")
		}
		out = append(out, it)
	}
	return out
}

var qwenpawChannelLabels = map[string]string{
	"imessage": "iMessage", "dingtalk": "DingTalk", "qq": "QQ", "onebot": "QQ (OneBot)", "wecom": "WeCom",
	"wechat": "WeChat", "weixin": "WeChat", "mattermost": "Mattermost", "mqtt": "MQTT", "voice": "Twilio voice",
	"sip": "SIP voice", "xiaoyi": "XiaoYi", "yuanbao": "Yuanbao",
}

// ---- Fernet ----------------------------------------------------------------

// qwenpawKeys finds the master key lazily, on the first ENC: value.
type qwenpawKeys struct {
	secretDir string
	enabled   bool
	tried     bool
	key       []byte // 32 bytes
	failed    bool
	err       string
}

func (k *qwenpawKeys) decrypt(v string) (string, string) {
	if !strings.HasPrefix(v, "ENC:") {
		return v, ""
	}
	if !k.enabled {
		return "", "the key is encrypted"
	}
	token := strings.TrimPrefix(v, "ENC:")
	if !k.tried {
		k.tried = true
		k.key, k.err = qwenpawMasterKey(k.secretDir, token)
	}
	if k.key == nil {
		k.failed = true
		return "", "the key is encrypted and QwenPaw's master key could not be read"
	}
	plain, err := qwenpawFernetDecrypt(k.key, token)
	if err != nil {
		k.failed = true
		return "", "the key could not be decrypted (master key changed?)"
	}
	return string(plain), ""
}

func (k *qwenpawKeys) decryptMap(m map[string]string) map[string]string {
	for key, v := range m {
		if d, _ := k.decrypt(v); d != "" || strings.HasPrefix(v, "ENC:") {
			m[key] = d
		}
	}
	return m
}

// qwenpawMasterKey returns the first candidate key that verifies sample:
// <secret>/.master_key, then the OS keychain (service qwenpaw, then copaw).
func qwenpawMasterKey(secretDir, sample string) ([]byte, string) {
	try := func(hexKey string) []byte {
		hexKey = strings.TrimSpace(hexKey)
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) < 32 {
			return nil
		}
		raw = raw[:32]
		if _, err := qwenpawFernetDecrypt(raw, sample); err != nil {
			return nil
		}
		return raw
	}
	if k := try(ReadText(filepath.Join(secretDir, ".master_key"))); k != nil {
		return k, ""
	}
	if DefaultKeychain == nil {
		return nil, "no keychain"
	}
	var lastErr string
	for _, account := range qwenpawAccounts(secretDir) {
		for _, service := range []string{"qwenpaw", "copaw"} {
			v, err := DefaultKeychain.Get(service, account)
			if err != nil {
				if !errors.Is(err, ErrNoSecret) {
					lastErr = "keychain: " + err.Error()
				}
				continue
			}
			if k := try(v); k != nil {
				return k, ""
			}
			lastErr = "the keychain's master key does not match"
		}
	}
	if lastErr == "" {
		lastErr = "no master key in the keychain or " + filepath.Join(filepath.Base(secretDir), ".master_key")
	}
	return nil, lastErr
}

// qwenpawAccounts lists keychain accounts to try (secret_store.py
// _keyring_account): the explicit override, the per-install account derived
// from the secret dir, and the historical "master_key".
func qwenpawAccounts(secretDir string) []string {
	var out []string
	if a := os.Getenv("QWENPAW_KEYRING_ACCOUNT"); a != "" {
		out = append(out, a)
	}
	dir := secretDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	sum := sha256.Sum256([]byte(dir))
	hashed := "master_key:" + hex.EncodeToString(sum[:])[:16]
	if os.Getenv("QWENPAW_WORKING_DIR") != "" || os.Getenv("QWENPAW_SECRET_DIR") != "" {
		return append(out, hashed, "master_key")
	}
	return append(out, "master_key", hashed)
}

// qwenpawFernetDecrypt decrypts a Fernet token (spec:
// https://github.com/fernet/spec/blob/master/Spec.md) with a 32-byte key:
// signing key = key[:16], encryption key = key[16:]. Token = base64url(0x80 |
// timestamp[8] | IV[16] | AES-128-CBC ciphertext | HMAC-SHA256[32]). The TTL
// is not checked (QwenPaw decrypts without one).
func qwenpawFernetDecrypt(key []byte, token string) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("fernet: key must be 32 bytes")
	}
	token = strings.TrimSpace(token)
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		data, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(token, "="))
		if err != nil {
			return nil, errors.New("fernet: invalid base64")
		}
	}
	if len(data) < 1+8+16+16+32 || data[0] != 0x80 {
		return nil, errors.New("fernet: invalid token")
	}
	body, sig := data[:len(data)-32], data[len(data)-32:]
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return nil, errors.New("fernet: signature mismatch")
	}
	iv, ct := body[9:25], body[25:]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("fernet: invalid ciphertext")
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New("fernet: invalid padding")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, errors.New("fernet: invalid padding")
		}
	}
	return plain[:len(plain)-pad], nil
}
