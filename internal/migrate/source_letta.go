package migrate

// Letta Code (github.com/letta-ai/letta-code). Formats read from the
// project's source at commit 1fcc966 (v0.34.2, 2026-10-02); paths below are
// relative to https://github.com/letta-ai/letta-code/blob/main/.
//
//   - Root ~/.letta (src/settings-manager.ts:979; $LETTA_HOME is honoured only
//     for crons.json, src/cron/cron-file.ts:134).
//   - settings.json (src/settings-manager.ts:64-111): env {LETTA_BASE_URL,
//     LETTA_API_KEY, provider keys}, preferredBackendMode, agents[]
//     {agentId, pinned, mcpServers[{name, transport stdio|http|sse, command,
//     args, env, url, headers}]} (src/agent-settings.ts:7-19,
//     src/mcp-client.ts:14-41).
//   - Local backend $LETTA_LOCAL_BACKEND_DIR or ~/.letta/lc-local-backend
//     (src/utils/local-backend-paths.ts:8-26): agents/<base64url(id)>.json
//     {id, name, description, system, tags, model, model_settings
//     {provider_type}, hidden} (src/backend/local/local-types.ts:18-28);
//     hidden, or hidden unset with tag "role:subagent", are skipped
//     (local-agent-record.ts:25-43, src/agent/agent-tags.ts:15).
//     providers/auth.json {providers: {<name>: {provider_type, auth {type
//     api|oauth, key}, base_url}}} in plain text
//     (local-provider-auth-store.ts:41-121).
//   - Memory (memfs): local agents <backend>/memfs/<agentId>/memory/, API
//     agents ~/.letta/agents/<agentId>/memory/ (src/backend/local/paths.ts:48,
//     src/agent/memory-filesystem.ts:46-57). Blocks are Markdown files with
//     front matter: persona.md / human.md (memfs v2,
//     src/backend/local/initial-memory.ts:17-78) or system/persona.md,
//     system/human.md (v1) or memory/system/… (src/agent/personality.ts:46).
//     persona → SOUL.md, human → USER.md, other blocks → memories.
//   - Skills (src/agent/skills.ts:181-205): ~/.letta/skills/ (recursive
//     SKILL.md) and <memory>/skills/.
//   - crons.json (src/cron/cron-file.ts:33-118): {version: 1, tasks: [{id,
//     name, cron, timezone, recurring, prompt, status active|paused|fired|
//     missed|cancelled, scheduled_for}]}; recurring=false is one-shot.
//   - Channels ~/.letta/channels/<channelId>/accounts.json {accounts: [{channel,
//     accountId, enabled, allowedUsers, token | botToken + appToken,
//     __letta_secret_refs}]} and routing.json (legacy routing.yaml, JSON
//     content) {routes: [{accountId, chatId, chatType, enabled}]}
//     (src/channels/accounts.ts:104-215, src/channels/routing-store.ts).
//     WhatsApp is a linked-device (Baileys) session
//     (src/channels/whatsapp/plugin.ts) and Signal uses a signal-cli JSON-RPC
//     bridge (src/channels/types.ts:670) — both unsupported.
//   - Keychain (src/utils/secrets.ts:16, src/channels/credential-store.ts:88):
//     service "letta-code"; channel tokens at account
//     "channel:<channelId>:<accountId>:<field>" when __letta_secret_refs marks
//     the field. Read through migrate.DefaultKeychain during Plan only.
//   - Running: listeners/*.lock {pid} (src/websocket/listener/
//     manual-instance-lock.ts:88) and crons.json scheduler_owners.*.pid.
//
// UNVERIFIED: how Bun.secrets maps {service, name} onto each OS store (the
// go-keyring service/account pair is assumed); whether provider keys in
// settings.json env are used by Letta (they are imported as keys the user
// stored); memfs dirs are git repos and the working tree is read, not HEAD.
// Not imported: the Letta Cloud API key/agents on Letta's servers, local agent
// secrets (agent:<id>:secrets:*), conversations (chat history).

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type lettaSource struct{}

func init() { Register(lettaSource{}) }

func (lettaSource) ID() string   { return "letta" }
func (lettaSource) Name() string { return "Letta Code" }

func lettaDefaultRoot() string { return filepath.Join(HomeDir(), ".letta") }

// lettaBackendDir is the local backend dir for root; $LETTA_LOCAL_BACKEND_DIR
// applies only to the default root.
func lettaBackendDir(root string) string {
	if v := strings.TrimSpace(os.Getenv("LETTA_LOCAL_BACKEND_DIR")); v != "" && root == lettaDefaultRoot() {
		return ExpandHome(v)
	}
	return filepath.Join(root, "lc-local-backend")
}

// lettaCronFile honours $LETTA_HOME for the default root.
func lettaCronFile(root string) string {
	if v := strings.TrimSpace(os.Getenv("LETTA_HOME")); v != "" && root == lettaDefaultRoot() {
		return filepath.Join(ExpandHome(v), "crons.json")
	}
	return filepath.Join(root, "crons.json")
}

func lettaLooksInstalled(root string) bool {
	return IsFile(filepath.Join(root, "settings.json")) || IsDir(filepath.Join(lettaBackendDir(root), "agents")) ||
		IsFile(lettaCronFile(root)) || IsDir(filepath.Join(root, "channels")) || IsDir(filepath.Join(root, "agents"))
}

func (s lettaSource) Detect(ctx context.Context, root string) (Detection, error) {
	if root == "" {
		root = lettaDefaultRoot()
	}
	root = ExpandHome(root)
	if !lettaLooksInstalled(root) {
		return Detection{}, ErrNotFound
	}
	d := Detection{Source: s.ID(), Name: s.Name(), Root: root}
	d.Running = lettaRunning(root) || RunningCheck([]string{"letta-code/dist", "@letta-ai/letta-code"}, nil)
	p := lettaPlan(d, nil, false)
	d.Summary = Summarize(p.items)
	return d, nil
}

// lettaPIDAlive is replaceable in tests.
var lettaPIDAlive = func(pid int) bool { return zeroclawPIDAlive(pid) }

func lettaRunning(root string) bool {
	locks, _ := filepath.Glob(filepath.Join(root, "listeners", "*.lock"))
	for _, l := range locks {
		var rec struct {
			PID int `json:"pid"`
		}
		if ReadJSON(l, &rec) == nil && lettaPIDAlive(rec.PID) {
			return true
		}
	}
	var cf struct {
		SchedulerOwners map[string]struct {
			PID int `json:"pid"`
		} `json:"scheduler_owners"`
	}
	if ReadJSON(lettaCronFile(root), &cf) == nil {
		for _, o := range cf.SchedulerOwners {
			if lettaPIDAlive(o.PID) {
				return true
			}
		}
	}
	return false
}

func (s lettaSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	if !IsDir(det.Root) {
		return Plan{}, ErrNotFound
	}
	p := lettaPlan(det, env, true)
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

type lettaAgent struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	System        string   `json:"system"`
	Tags          []string `json:"tags"`
	Model         string   `json:"model"`
	Hidden        *bool    `json:"hidden"`
	ModelSettings struct {
		ProviderType string `json:"provider_type"`
	} `json:"model_settings"`
	memDir string
}

type lettaSettings struct {
	Env                  map[string]string `json:"env"`
	PreferredBackendMode string            `json:"preferredBackendMode"`
	Agents               []struct {
		AgentID    string           `json:"agentId"`
		Pinned     bool             `json:"pinned"`
		MCPServers []map[string]any `json:"mcpServers"`
	} `json:"agents"`
}

type lettaPlanner struct {
	det      Detection
	env      Env
	secrets  bool // false during Detect: no keychain
	backend  string
	settings lettaSettings
	agents   []lettaAgent // visible local agents, primary first
	primary  *lettaAgent
	memDir   string // the primary agent's memory dir
	items    []Item
	warn     []string
	planned  map[string]bool
	byName   map[string]string // provider record name / type → Antares id
}

func lettaPlan(det Detection, env Env, secrets bool) *lettaPlanner {
	p := &lettaPlanner{det: det, env: env, secrets: secrets, backend: lettaBackendDir(det.Root),
		planned: map[string]bool{}, byName: map[string]string{}}
	if path := filepath.Join(det.Root, "settings.json"); IsFile(path) {
		if err := ReadJSON(path, &p.settings); err != nil {
			p.warn = append(p.warn, "settings.json could not be read: "+err.Error())
		}
	}
	p.loadAgents()
	p.providers()
	p.model()
	p.persona()
	p.memory()
	p.skills()
	p.mcp()
	p.cron()
	p.channels()
	p.roles()
	return p
}

func (p *lettaPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// ---- agents and memfs -----------------------------------------------------------

// lettaMemoryDir returns the memfs dir for an agent id, if present.
func (p *lettaPlanner) lettaMemoryDir(id string) string {
	for _, d := range []string{
		filepath.Join(p.backend, "memfs", id, "memory"),
		filepath.Join(p.det.Root, "agents", id, "memory"),
	} {
		if IsDir(d) {
			return d
		}
	}
	return ""
}

func (p *lettaPlanner) loadAgents() {
	files, _ := filepath.Glob(filepath.Join(p.backend, "agents", "*.json"))
	sort.Strings(files)
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "._") {
			continue
		}
		var a lettaAgent
		if ReadJSON(f, &a) != nil || a.ID == "" {
			continue
		}
		if a.Hidden != nil && *a.Hidden || a.Hidden == nil && contains(a.Tags, "role:subagent") {
			continue
		}
		if a.Name == "" {
			a.Name = "Letta Code"
		}
		a.memDir = p.lettaMemoryDir(a.ID)
		p.agents = append(p.agents, a)
	}
	pinned := map[string]bool{}
	for _, s := range p.settings.Agents {
		if s.Pinned {
			pinned[s.AgentID] = true
		}
	}
	rank := func(a lettaAgent) int {
		switch {
		case pinned[a.ID]:
			return 0
		case a.Name == "Letta Code":
			return 1
		}
		return 2
	}
	sort.SliceStable(p.agents, func(i, j int) bool {
		if ri, rj := rank(p.agents[i]), rank(p.agents[j]); ri != rj {
			return ri < rj
		}
		return p.agents[i].Name < p.agents[j].Name
	})
	if len(p.agents) > 0 {
		p.primary = &p.agents[0]
		p.memDir = p.primary.memDir
		return
	}
	// No local agents: an API-backend agent's memfs (pinned first).
	var ids []string
	for _, s := range p.settings.Agents {
		if s.Pinned {
			ids = append(ids, s.AgentID)
		}
	}
	dirs, _ := os.ReadDir(filepath.Join(p.det.Root, "agents"))
	for _, d := range dirs {
		if d.IsDir() {
			ids = append(ids, d.Name())
		}
	}
	for _, id := range ids {
		if d := filepath.Join(p.det.Root, "agents", id, "memory"); IsDir(d) {
			p.memDir = d
			return
		}
	}
}

// lettaBlock reads a memory block by label, in any memfs layout, without its
// front matter.
func lettaBlock(memDir, label string) (string, string) {
	if memDir == "" {
		return "", ""
	}
	for _, rel := range []string{label + ".md", filepath.Join("system", label+".md"), filepath.Join("memory", "system", label+".md")} {
		if t := ReadText(filepath.Join(memDir, rel)); t != "" {
			return strings.TrimSpace(stripFrontMatter(strings.ReplaceAll(t, "\r\n", "\n"))), filepath.ToSlash(rel)
		}
	}
	return "", ""
}

func (p *lettaPlanner) persona() {
	if t, from := lettaBlock(p.memDir, "persona"); t != "" {
		if it, ok := TextItem(p.env, CatSoul, t, "persona block ("+from+")"); ok {
			p.add(it)
		}
	}
	if t, from := lettaBlock(p.memDir, "human"); t != "" {
		if it, ok := TextItem(p.env, CatUserMD, t, "human block ("+from+")"); ok {
			p.add(it)
		}
	}
}

// memory turns the primary agent's other memory blocks into memories: one
// entry per bullet/paragraph, tagged with the block label.
func (p *lettaPlanner) memory() {
	if p.memDir == "" {
		return
	}
	skip := map[string]bool{"persona": true, "human": true, "memory": true, "onboarding": true}
	var entries []MemoryEntry
	_ = filepath.WalkDir(p.memDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(p.memDir, path)
		if d.IsDir() {
			if path != p.memDir && (strings.HasPrefix(d.Name(), ".") || rel == "skills") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if skip[strings.ToLower(label)] {
			return nil
		}
		for _, e := range SplitMarkdownEntries(ReadText(path)) {
			if e.Heading == "" {
				e.Heading = label
			}
			entries = append(entries, e)
		}
		return nil
	})
	p.add(MemoryItems(entries, "global", "import:letta")...)
}

// ---- providers and model ---------------------------------------------------------

// lettaVendor maps a Letta provider_type to an Antares vendor name.
var lettaVendor = map[string]string{
	"openai": "openai", "openai-responses": "openai", "anthropic": "anthropic", "openrouter": "openrouter",
	"google_ai": "gemini", "google": "gemini", "gemini": "gemini", "zai": "zai", "zai_coding": "zai",
	"minimax": "minimax", "moonshot": "moonshot", "ollama": "ollama", "lmstudio_openai": "lmstudio",
	"lmstudio": "lmstudio", "deepseek": "deepseek", "groq": "groq", "xai": "xai", "mistral": "mistral",
	"chatgpt_oauth": "codex", "bedrock": "bedrock",
}

func (p *lettaPlanner) taken(id string) bool {
	return p.planned[id] || p.env != nil && p.env.ProviderExists(id)
}

func (p *lettaPlanner) addProvider(names []string, pp ProviderPayload, detail string) {
	for _, n := range names {
		p.byName[n] = pp.ID
	}
	if p.planned[pp.ID] {
		return
	}
	p.planned[pp.ID] = true
	p.add(ProviderItem(p.env, pp, detail))
}

func (p *lettaPlanner) providers() {
	var store struct {
		Providers map[string]struct {
			Name         string `json:"name"`
			ProviderType string `json:"provider_type"`
			BaseURL      string `json:"base_url"`
			Auth         struct {
				Type string `json:"type"`
				Key  string `json:"key"`
			} `json:"auth"`
		} `json:"providers"`
	}
	_ = ReadJSON(filepath.Join(p.backend, "providers", "auth.json"), &store)
	for _, name := range sortedKeys(store.Providers) {
		rec := store.Providers[name]
		typ := strings.ToLower(rec.ProviderType)
		vname := lettaVendor[typ]
		if vname == "" {
			vname = typ
		}
		v, known := Vendor(vname)
		label := firstNonEmpty(v.Label, name)
		switch {
		case rec.Auth.Type == "oauth" || known && v.OAuth:
			p.add(OAuthProviderItem(Slug(strings.TrimPrefix(name, "lc-")), label))
			continue
		case typ == "bedrock":
			p.add(UnsupportedItem(CatProvider, "bedrock", "AWS Bedrock", "AWS credentials are not migrated; set Bedrock up in Antares"))
			continue
		}
		key := rec.Auth.Key
		if key == "not-needed" {
			key = ""
		}
		var pp ProviderPayload
		switch {
		case known && (rec.BaseURL == "" || typ != "openai-compatible"):
			pp = ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: firstNonEmpty(rec.BaseURL, v.BaseURL)}
		case rec.BaseURL != "":
			n := strings.TrimPrefix(name, "lc-")
			if n == "openai-compatible" || n == "llama-cpp" {
				n = hostLabel(rec.BaseURL)
			}
			pp = ProviderPayload{ID: CustomProviderID(p.taken, n), Label: n, Kind: "openai-compatible", BaseURL: rec.BaseURL}
		default:
			p.add(UnsupportedItem(CatProvider, Slug(name), name, "unknown provider type \""+rec.ProviderType+"\" with no endpoint"))
			continue
		}
		pp.APIKey = key
		p.addProvider([]string{name, typ, strings.TrimPrefix(name, "lc-")}, pp, "key from lc-local-backend/providers/auth.json")
	}
	for _, e := range []struct{ env, vendor string }{
		{"ANTHROPIC_API_KEY", "anthropic"}, {"OPENAI_API_KEY", "openai"}, {"OPENROUTER_API_KEY", "openrouter"},
		{"GEMINI_API_KEY", "gemini"}, {"GOOGLE_API_KEY", "gemini"},
	} {
		key := p.settings.Env[e.env]
		v, _ := Vendor(e.vendor)
		if key == "" || p.planned[v.ID] {
			continue
		}
		p.addProvider([]string{e.vendor}, ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL, APIKey: key},
			"key from settings.json env "+e.env)
	}
	if p.settings.Env["LETTA_API_KEY"] != "" || p.settings.PreferredBackendMode == "api" {
		p.add(UnsupportedItem(CatProvider, "letta-cloud", "Letta Cloud",
			"agents on the Letta API run on Letta's servers; Antares has no Letta provider (their memory files are imported when present locally)"))
	}
}

func (p *lettaPlanner) model() {
	if p.primary == nil {
		return
	}
	a := p.primary
	handle := a.Model
	if handle == "" || handle == "local/default" {
		return
	}
	prov, model := "", handle
	if pre, rest, ok := strings.Cut(handle, "/"); ok {
		if id := p.byName[pre]; id != "" {
			prov, model = id, rest
		} else if v, known := Vendor(firstNonEmpty(lettaVendor[pre], pre)); known && p.planned[v.ID] {
			prov, model = v.ID, rest
		}
	}
	if prov == "" {
		typ := strings.ToLower(a.ModelSettings.ProviderType)
		prov = p.byName[typ]
		if prov == "" {
			if v, known := Vendor(firstNonEmpty(lettaVendor[typ], typ)); known && p.planned[v.ID] {
				prov = v.ID
			}
		}
	}
	if prov == "" {
		p.warn = append(p.warn, "the default model "+handle+" uses a provider with no key here; pick a model in Antares")
		return
	}
	for _, it := range p.items {
		if pp := it.Payload.Provider; pp != nil && pp.ID == prov && !contains(pp.Models, model) {
			pp.Models = append(pp.Models, model)
		}
	}
	p.add(ModelItem(p.env, ModelPayload{Provider: prov, Model: model}))
}

// ---- skills, MCP, cron -------------------------------------------------------------

func (p *lettaPlanner) skills() {
	seen := map[string]bool{}
	roots := []string{filepath.Join(p.det.Root, "skills")}
	if p.memDir != "" {
		roots = append(roots, filepath.Join(p.memDir, "skills"))
	}
	for _, root := range roots {
		for _, sd := range ScanSkills(root, 3) {
			if !seen[sd.Name] {
				seen[sd.Name] = true
				p.add(SkillItem(p.env, sd))
			}
		}
	}
}

func (p *lettaPlanner) mcp() {
	seen := map[string]bool{}
	// The primary agent's servers first, then the rest.
	agents := p.settings.Agents
	sort.SliceStable(agents, func(i, j int) bool {
		return p.primary != nil && agents[i].AgentID == p.primary.ID && agents[j].AgentID != p.primary.ID
	})
	for _, a := range agents {
		for _, s := range a.MCPServers {
			name := Slug(str(s["name"]))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			p.add(MCPItem(p.env, MCPPayload{Name: name, Transport: str(s["transport"]), Command: str(s["command"]),
				Args: strList(s["args"]), Env: lettaStrMap(s["env"]), URL: str(s["url"]), Headers: lettaStrMap(s["headers"])}))
		}
	}
}

func lettaStrMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, x := range m {
		out[k] = str(x)
	}
	return out
}

func (p *lettaPlanner) cron() {
	var cf struct {
		Version int `json:"version"`
		Tasks   []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Description  string `json:"description"`
			Cron         string `json:"cron"`
			Timezone     string `json:"timezone"`
			Recurring    *bool  `json:"recurring"`
			Prompt       string `json:"prompt"`
			Status       string `json:"status"`
			ScheduledFor string `json:"scheduled_for"`
		} `json:"tasks"`
	}
	path := lettaCronFile(p.det.Root)
	if !IsFile(path) {
		return
	}
	if err := ReadJSON(path, &cf); err != nil || cf.Version != 1 {
		p.warn = append(p.warn, "crons.json could not be read (version 1 expected)")
		return
	}
	for _, t := range cf.Tasks {
		key := Slug(firstNonEmpty(t.ID, t.Name))
		name := firstNonEmpty(t.Name, t.Description, Truncate(t.Prompt, 40))
		switch t.Status {
		case "cancelled", "fired", "missed":
			continue // finished or removed in Letta
		}
		if t.Recurring != nil && !*t.Recurring {
			p.add(UnsupportedItem(CatCron, key, name, "one-shot schedule (runs once at "+t.ScheduledFor+")"))
			continue
		}
		p.add(CronItem(key, CronPayload{Name: name, Schedule: t.Cron, Prompt: t.Prompt, Timezone: t.Timezone}, t.Status != "paused"))
	}
}

// ---- channels ----------------------------------------------------------------------

func (p *lettaPlanner) channels() {
	dirs, _ := os.ReadDir(filepath.Join(p.det.Root, "channels"))
	used := map[string]string{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		chID := d.Name()
		dir := filepath.Join(p.det.Root, "channels", chID)
		var accts struct {
			Accounts []map[string]any `json:"accounts"`
		}
		if ReadJSON(filepath.Join(dir, "accounts.json"), &accts) != nil {
			continue
		}
		routes := lettaRoutes(dir)
		for _, a := range accts.Accounts {
			p.channelAccount(chID, a, routes, used)
		}
	}
}

type lettaRoute struct {
	AccountID string `json:"accountId"`
	ChatID    string `json:"chatId"`
	ChatType  string `json:"chatType"`
	Enabled   *bool  `json:"enabled"`
}

// lettaRoutes reads routing.json (or the legacy routing.yaml, which holds
// JSON).
func lettaRoutes(dir string) []lettaRoute {
	var r struct {
		Routes []lettaRoute `json:"routes"`
	}
	if ReadJSON(filepath.Join(dir, "routing.json"), &r) != nil {
		if ReadJSON(filepath.Join(dir, "routing.yaml"), &r) != nil {
			_ = ReadYAML(filepath.Join(dir, "routing.yaml"), &r)
		}
	}
	return r.Routes
}

// token reads a channel credential field: inline, or from the keychain when
// __letta_secret_refs marks it. ok=false when it should be there but is not.
func (p *lettaPlanner) token(chID, acctID, field string, a map[string]any) (string, bool) {
	if v := str(a[field]); v != "" {
		return v, true
	}
	refs, _ := a["__letta_secret_refs"].(map[string]any)
	if b, _ := refs[field].(bool); !b {
		return "", false
	}
	if !p.secrets {
		return "", true // present in the keychain; Detect does not read it
	}
	v, err := DefaultKeychain.Get("letta-code", "channel:"+chID+":"+acctID+":"+field)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

func (p *lettaPlanner) channelAccount(chID string, a map[string]any, routes []lettaRoute, used map[string]string) {
	platform := firstNonEmpty(str(a["channel"]), chID)
	acctID := str(a["accountId"])
	title := PlatformLabel(platform)
	if dn := str(a["displayName"]); dn != "" {
		title += " " + dn
	}
	key := platform
	if prev, dup := used[platform]; dup {
		key = platform + "-" + Slug(acctID)
		if SupportedPlatform(platform) {
			p.add(UnsupportedItem(CatChannel, key, title, "Antares has one "+PlatformLabel(platform)+" bot; "+prev+" is already being imported"))
			return
		}
	}
	switch platform {
	case "whatsapp":
		p.add(UnsupportedItem(CatChannel, key, title, "Letta's WhatsApp channel is a linked-device (Baileys) session; Antares supports the WhatsApp Cloud API only"))
		return
	case "signal":
		p.add(UnsupportedItem(CatChannel, key, title, "Letta talks to a signal-cli JSON-RPC bridge; Antares needs signal-cli-rest-api — set Signal up in Antares"))
		return
	case "telegram", "discord", "slack":
	default:
		p.add(UnsupportedItem(CatChannel, key, title, "Antares has no "+PlatformLabel(platform)+" gateway"))
		return
	}
	used[platform] = title
	var chats []string
	for _, r := range routes {
		if r.AccountID == acctID && r.ChatID != "" && (r.Enabled == nil || *r.Enabled) && !contains(chats, r.ChatID) {
			chats = append(chats, r.ChatID)
		}
	}
	fields := map[string]any{}
	if u := strList(a["allowedUsers"]); len(u) > 0 {
		fields["allowed_users"] = u
	}
	missing := ""
	switch platform {
	case "telegram", "discord":
		tok, ok := p.token(chID, acctID, "token", a)
		fields["bot_token"] = tok
		if !ok {
			missing = "bot_token"
		}
		if platform == "telegram" && len(chats) > 0 {
			fields["allowed_chats"] = chats
		}
	case "slack":
		bot, ok1 := p.token(chID, acctID, "botToken", a)
		app, ok2 := p.token(chID, acctID, "appToken", a)
		fields["bot_token"], fields["app_token"] = bot, app
		if !ok1 {
			missing = "bot_token"
		} else if !ok2 {
			missing = "app_token"
		}
		if chans := strList(a["allowed_channels"]); len(chans) > 0 {
			fields["allowed_channels"] = chans
		}
	}
	it := ChannelItem(p.env, platform, title, fields, missing)
	if b, ok := a["enabled"].(bool); ok && !b {
		it.Detail = "disabled in Letta"
	}
	p.add(it)
}

// ---- roles -------------------------------------------------------------------------

// roles turns every visible local agent other than the primary into a role;
// its persona block (else description) is the prompt.
func (p *lettaPlanner) roles() {
	for i, a := range p.agents {
		if i == 0 {
			continue
		}
		prompt, _ := lettaBlock(a.memDir, "persona")
		desc := ""
		if a.Description != nil {
			desc = *a.Description
		}
		if prompt == "" {
			prompt = desc
		}
		model := ""
		if pre, rest, ok := strings.Cut(a.Model, "/"); ok && p.byName[pre] != "" {
			model = p.byName[pre] + "/" + rest
		}
		p.add(RoleItem(p.env, RolePayload{Name: a.Name, Title: a.Name, Summary: firstNonEmpty(desc, "Letta Code agent"), Prompt: prompt, Model: model}))
	}
}
