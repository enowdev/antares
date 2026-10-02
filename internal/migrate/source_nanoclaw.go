package migrate

// NanoClaw (github.com/nanocoai/nanoclaw). Formats read from the project's
// source: v2 on main (2.4.0, 2026-10-03) and v1 at commit 90acff28ad (the last
// full v1 tree). M = https://github.com/nanocoai/nanoclaw/blob/main,
// V1 = https://github.com/nanocoai/nanoclaw/blob/90acff28ad.
//
//   - NanoClaw runs from its cloned repo (PROJECT_ROOT = cwd, M/src/config.ts);
//     ~/.config/nanoclaw holds only mount/sender allowlists. Nothing points
//     from there to the install, so the install is found from the launchd
//     plist WorkingDirectory (com.nanoclaw, com.nanoclaw-v2-<slug>), the
//     systemd unit (nanoclaw*.service), $NANOCLAW_V1_PATH, or ~/nanoclaw,
//     ~/nanoclaw-v2 (README clone dir). --root may name the repo directly.
//   - v1 vs v2: store/messages.db vs data/v2.db; version from package.json.
//   - .env (M/src/env.ts, KEY=VALUE): ASSISTANT_NAME, TZ, v2
//     NANOCLAW_DEFAULT_MODEL; v1 agent auth ANTHROPIC_API_KEY,
//     ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_OAUTH_TOKEN
//     (V1/src/container-runner.ts readSecrets); channel tokens
//     TELEGRAM_BOT_TOKEN, DISCORD_BOT_TOKEN, SLACK_BOT_TOKEN/SLACK_APP_TOKEN
//     (+ _<NAME> instances; M/setup/migrate-v2/shared.ts).
//   - Credential gateway: late v1 and v2 keep keys in a OneCLI / iron-proxy
//     vault (ONECLI_URL, NANOCLAW_GATEWAY_PROVIDER), which is not readable on
//     disk → the provider is needs_input.
//   - v1 groups (V1/src/db.ts): registered_groups(jid, name, folder, is_main,
//     …); memory in groups/<folder>/CLAUDE.md, shared groups/global/CLAUDE.md.
//     Mapped: global/CLAUDE.md → AGENTS.md, the main group's CLAUDE.md →
//     memories, other groups' CLAUDE.md → knowledge.
//   - v1 scheduled_tasks(id, group_folder, prompt, schedule_type
//     cron|interval|once, schedule_value — interval in milliseconds, status
//     active|paused|completed) (V1/src/task-scheduler.ts).
//   - v2 (M/src/db/schema.ts): agent_groups(id, name, folder),
//     container_configs(agent_group_id, provider, model, mcp_servers JSON
//     {name: {command,args,env} | {url,headers}}), messaging_groups
//     (channel_type, platform_id); per group groups/<folder>/
//     instructions.prepend.md (persona/standing instructions),
//     CLAUDE.local.md (the v1 memory) and memory/*.md. Tasks are rows of
//     data/v2-sessions/<group>/<session>/inbound.db messages_in (kind='task',
//     recurrence cron or NULL = one-shot, content JSON {prompt},
//     M/src/mailbox/sqlite/schema.ts, M/src/modules/scheduling/create.ts).
//   - Skills: agent-created skills in data/sessions/<folder>/.claude/skills
//     (v1) and data/v2-sessions/<group>/.claude-shared/skills (v2); copies of
//     the bundled container/skills are skipped.
//   - WhatsApp is Baileys (store/auth/creds.json) → unsupported.
//   - Running: launchd/systemd labels above, <root>/nanoclaw.pid, process
//     "<root>/dist/index.js".
//
// UNVERIFIED: v1 columns added after 90acff28ad; Matrix and other channel
// env keys (not imported); the CLAUDE.md → memory/knowledge split is a
// judgement (NanoClaw itself treats the whole file as the group's memory).

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type nanoclawSource struct{}

func init() { Register(nanoclawSource{}) }

func (nanoclawSource) ID() string   { return "nanoclaw" }
func (nanoclawSource) Name() string { return "NanoClaw" }

// nanoclawIsInstall reports whether dir is a NanoClaw checkout.
func nanoclawIsInstall(dir string) bool {
	if !IsDir(filepath.Join(dir, "groups")) && !IsFile(filepath.Join(dir, "store", "messages.db")) && !IsFile(filepath.Join(dir, "data", "v2.db")) {
		return false
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if ReadJSON(filepath.Join(dir, "package.json"), &pkg) == nil {
		return strings.Contains(strings.ToLower(pkg.Name), "nanoclaw")
	}
	return IsFile(filepath.Join(dir, "store", "messages.db")) || IsFile(filepath.Join(dir, "data", "v2.db"))
}

var nanoclawWorkDirRE = regexp.MustCompile(`(?s)<key>WorkingDirectory</key>\s*<string>([^<]+)</string>`)
var nanoclawProgArgRE = regexp.MustCompile(`<string>([^<]+)/dist/index\.js</string>`)

// nanoclawCandidates lists possible install dirs, most specific first.
func nanoclawCandidates() []string {
	home := HomeDir()
	var out []string
	if v := strings.TrimSpace(os.Getenv("NANOCLAW_V1_PATH")); v != "" {
		out = append(out, ExpandHome(v))
	}
	plists, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "com.nanoclaw*.plist"))
	for _, pl := range plists {
		t := ReadText(pl)
		if m := nanoclawWorkDirRE.FindStringSubmatch(t); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		}
		if m := nanoclawProgArgRE.FindStringSubmatch(t); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	units, _ := filepath.Glob(filepath.Join(home, ".config", "systemd", "user", "nanoclaw*.service"))
	for _, u := range units {
		for _, line := range strings.Split(ReadText(u), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "WorkingDirectory="); ok {
				out = append(out, ExpandHome(strings.TrimSpace(v)))
			}
		}
	}
	for _, d := range []string{"nanoclaw", "nanoclaw-v2", filepath.Join("src", "nanoclaw"), filepath.Join("projects", "nanoclaw")} {
		out = append(out, filepath.Join(home, d))
	}
	return out
}

func (s nanoclawSource) Detect(ctx context.Context, root string) (Detection, error) {
	var dir string
	if root != "" {
		root = ExpandHome(root)
		if nanoclawIsInstall(root) {
			dir = root
		}
	} else {
		for _, c := range nanoclawCandidates() {
			if nanoclawIsInstall(c) {
				dir = c
				break
			}
		}
	}
	cfg := filepath.Join(HomeDir(), ".config", "nanoclaw")
	if dir == "" {
		if root == "" && IsDir(cfg) {
			// Installed, but the checkout is somewhere we cannot guess.
			return Detection{Source: s.ID(), Name: s.Name(), Root: cfg,
				Summary: "install folder not found — run the import with --root <your nanoclaw folder>"}, nil
		}
		return Detection{}, ErrNotFound
	}
	d := Detection{Source: s.ID(), Name: s.Name(), Root: dir}
	var pkg struct {
		Version string `json:"version"`
	}
	if ReadJSON(filepath.Join(dir, "package.json"), &pkg) == nil {
		d.Version = pkg.Version
	}
	d.Running = nanoclawRunning(dir)
	p := nanoclawPlan(d, nil)
	d.Summary = Summarize(p.items)
	return d, nil
}

// nanoclawPIDAlive is replaceable in tests.
var nanoclawPIDAlive = func(pid int) bool { return zeroclawPIDAlive(pid) }

func nanoclawRunning(dir string) bool {
	if n, err := strconv.Atoi(ReadText(filepath.Join(dir, "nanoclaw.pid"))); err == nil && nanoclawPIDAlive(n) {
		return true
	}
	labels := []string{"com.nanoclaw"}
	plists, _ := filepath.Glob(filepath.Join(HomeDir(), "Library", "LaunchAgents", "com.nanoclaw-v2-*.plist"))
	for _, pl := range plists {
		labels = append(labels, strings.TrimSuffix(filepath.Base(pl), ".plist"))
	}
	return RunningCheck([]string{filepath.Join(dir, "dist", "index.js")}, labels)
}

func (s nanoclawSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	if !IsDir(det.Root) {
		return Plan{}, ErrNotFound
	}
	p := nanoclawPlan(det, env)
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

type nanoclawGroup struct {
	id, name, folder string
	jid              string
	main             bool
}

type nanoclawPlanner struct {
	det    Detection
	env    Env
	dotenv map[string]string
	v2     bool
	groups []nanoclawGroup // main first
	items  []Item
	warn   []string
	chats  map[string][]string // platform → chat ids (allowlist)
}

func nanoclawPlan(det Detection, env Env) *nanoclawPlanner {
	p := &nanoclawPlanner{det: det, env: env, chats: map[string][]string{}}
	if !nanoclawIsInstall(det.Root) {
		p.warn = append(p.warn, "NanoClaw's install folder was not found; run the import with --root pointing at your nanoclaw checkout.")
		return p
	}
	p.dotenv = ReadDotEnv(filepath.Join(det.Root, ".env"))
	p.v2 = IsFile(filepath.Join(det.Root, "data", "v2.db"))
	if p.v2 {
		p.loadV2()
	} else {
		p.loadV1()
	}
	p.providers()
	p.persona()
	p.skills()
	p.cron()
	p.channels()
	p.roles()
	return p
}

func (p *nanoclawPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// ---- groups --------------------------------------------------------------------------

// nanoclawChatPlatform maps a v1 JID prefix / v2 channel_type to a platform.
func nanoclawChatPlatform(s string) string {
	switch strings.ToLower(s) {
	case "tg", "telegram":
		return "telegram"
	case "slack":
		return "slack"
	case "dc", "discord":
		return "discord"
	}
	return ""
}

func (p *nanoclawPlanner) addChat(platform, id string) {
	if platform != "" && id != "" && !contains(p.chats[platform], id) {
		p.chats[platform] = append(p.chats[platform], id)
	}
}

func (p *nanoclawPlanner) loadV1() {
	db, err := zeroclawOpenDB(filepath.Join(p.det.Root, "store", "messages.db"))
	if err == nil {
		defer db.Close()
		cols := zeroclawColumns(db, "registered_groups")
		if cols["folder"] {
			rows, err := db.Query("SELECT COALESCE(jid,''), COALESCE(name,''), folder, " + zeroclawColOr(cols, "is_main", "0") + " FROM registered_groups ORDER BY " + zeroclawColOr(cols, "added_at", "''"))
			if err == nil {
				for rows.Next() {
					var g nanoclawGroup
					var isMain int
					if rows.Scan(&g.jid, &g.name, &g.folder, &isMain) == nil {
						g.main = isMain != 0 || g.folder == "main"
						p.groups = append(p.groups, g)
						if pre, id, ok := strings.Cut(g.jid, ":"); ok {
							p.addChat(nanoclawChatPlatform(pre), id)
						}
					}
				}
				rows.Close()
			}
		}
	}
	if len(p.groups) == 0 && IsDir(filepath.Join(p.det.Root, "groups", "main")) {
		p.groups = append(p.groups, nanoclawGroup{name: "main", folder: "main", main: true})
	}
	p.sortGroups()
}

func (p *nanoclawPlanner) loadV2() {
	db, err := zeroclawOpenDB(filepath.Join(p.det.Root, "data", "v2.db"))
	if err != nil {
		return
	}
	defer db.Close()
	if rows, err := db.Query("SELECT id, COALESCE(name,''), folder FROM agent_groups ORDER BY created_at, id"); err == nil {
		for rows.Next() {
			var g nanoclawGroup
			if rows.Scan(&g.id, &g.name, &g.folder) == nil {
				g.main = g.folder == "main"
				p.groups = append(p.groups, g)
			}
		}
		rows.Close()
	}
	if len(p.groups) > 0 && !p.groups[0].main {
		hasMain := false
		for _, g := range p.groups {
			hasMain = hasMain || g.main
		}
		if !hasMain {
			p.groups[0].main = true
		}
	}
	if rows, err := db.Query("SELECT COALESCE(channel_type,''), COALESCE(platform_id,'') FROM messaging_groups WHERE denied_at IS NULL"); err == nil {
		for rows.Next() {
			var ct, pid string
			if rows.Scan(&ct, &pid) == nil {
				p.addChat(nanoclawChatPlatform(ct), pid)
			}
		}
		rows.Close()
	}
	p.sortGroups()
}

func (p *nanoclawPlanner) sortGroups() {
	sort.SliceStable(p.groups, func(i, j int) bool { return p.groups[i].main && !p.groups[j].main })
}

func (p *nanoclawPlanner) mainGroup() *nanoclawGroup {
	if len(p.groups) > 0 && p.groups[0].main {
		return &p.groups[0]
	}
	return nil
}

// ---- providers and model -----------------------------------------------------------

func (p *nanoclawPlanner) providers() {
	e := p.dotenv
	base := e["ANTHROPIC_BASE_URL"]
	key := firstNonEmpty(e["ANTHROPIC_API_KEY"], e["ANTHROPIC_AUTH_TOKEN"])
	gateway := e["NANOCLAW_GATEWAY_PROVIDER"]
	if gateway == "" && e["ONECLI_URL"] != "" {
		gateway = "onecli"
	}
	planned := ""
	switch {
	case key != "":
		v, _ := Vendor("anthropic")
		pp := ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL, APIKey: key}
		if base != "" && !strings.Contains(base, "api.anthropic.com") {
			if bv, ok := VendorByBaseURL(base); ok {
				pp = ProviderPayload{ID: bv.ID, Label: bv.Label, Kind: bv.Kind, BaseURL: base, APIKey: key}
			} else {
				taken := func(id string) bool { return p.env != nil && p.env.ProviderExists(id) }
				pp = ProviderPayload{ID: CustomProviderID(taken, hostLabel(base)), Label: hostLabel(base), Kind: "anthropic", BaseURL: base, APIKey: key}
			}
		}
		planned = pp.ID
		p.add(ProviderItem(p.env, pp, "key from .env"))
	case gateway != "":
		v, _ := Vendor("anthropic")
		it := ProviderItem(p.env, ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL}, "NanoClaw's "+gateway+" credential gateway")
		if it.Status == StatusNeedsInput {
			it.Reason = "NanoClaw keeps the key in its " + gateway + " credential vault, which cannot be read; enter it to import this provider"
		}
		planned = v.ID
		p.add(it)
	}
	if e["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		p.add(OAuthProviderItem("claude-subscription", "Claude subscription (Claude Code login)"))
	}
	model := e["NANOCLAW_DEFAULT_MODEL"]
	if g := p.mainGroup(); g != nil && p.v2 && g.id != "" {
		if cc := p.containerConfig(g.id); cc != nil && str(cc["model"]) != "" {
			model = str(cc["model"])
		}
	}
	if model != "" && planned != "" {
		p.add(ModelItem(p.env, ModelPayload{Provider: planned, Model: model}))
	}
}

// containerConfig reads a v2 container_configs row as a map.
func (p *nanoclawPlanner) containerConfig(groupID string) map[string]any {
	db, err := zeroclawOpenDB(filepath.Join(p.det.Root, "data", "v2.db"))
	if err != nil {
		return nil
	}
	defer db.Close()
	cols := zeroclawColumns(db, "container_configs")
	if !cols["agent_group_id"] {
		return nil
	}
	var model, mcp string
	err = db.QueryRow("SELECT "+zeroclawColOr(cols, "model", "''")+", "+zeroclawColOr(cols, "mcp_servers", "'{}'")+
		" FROM container_configs WHERE agent_group_id = ?", groupID).Scan(&model, &mcp)
	if err != nil {
		return nil
	}
	out := map[string]any{"model": model}
	var servers map[string]any
	if json.Unmarshal([]byte(mcp), &servers) == nil {
		out["mcp_servers"] = servers
	}
	return out
}

// ---- persona, memory, knowledge ---------------------------------------------------------

func (p *nanoclawPlanner) groupDir(folder string) string {
	return filepath.Join(p.det.Root, "groups", folder)
}

func (p *nanoclawPlanner) persona() {
	var instr, from []string
	if t := ReadText(filepath.Join(p.groupDir("global"), "CLAUDE.md")); t != "" {
		instr = append(instr, t)
		from = append(from, "groups/global/CLAUDE.md")
	}
	g := p.mainGroup()
	if g != nil && p.v2 {
		if t := ReadText(filepath.Join(p.groupDir(g.folder), "instructions.prepend.md")); t != "" {
			instr = append(instr, t)
			from = append(from, "groups/"+g.folder+"/instructions.prepend.md")
		}
	}
	if it, ok := TextItem(p.env, CatAgentsMD, strings.Join(instr, "\n\n"), strings.Join(from, " + ")); ok {
		p.add(it)
	}
	if name := p.dotenv["ASSISTANT_NAME"]; name != "" {
		if it, ok := TextItem(p.env, CatSoul, "Your name is "+name+".", ".env ASSISTANT_NAME"); ok {
			p.add(it)
		}
	}
	// The main group's memory file → memories; other groups' → knowledge.
	memFile := "CLAUDE.md"
	if p.v2 {
		memFile = "CLAUDE.local.md"
	}
	var entries []MemoryEntry
	if g != nil {
		entries = SplitMarkdownEntries(ReadText(filepath.Join(p.groupDir(g.folder), memFile)))
	}
	p.add(MemoryItems(entries, "global", "import:nanoclaw")...)
	for _, og := range p.groups {
		if og.main {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("groups", og.folder, memFile))
		if it, ok := KnowledgeItem(p.env, rel, ReadText(filepath.Join(p.det.Root, rel))); ok {
			p.add(it)
		}
	}
	if p.v2 {
		for _, gg := range p.groups {
			dir := filepath.Join(p.groupDir(gg.folder), "memory")
			_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
					return nil
				}
				rel, _ := filepath.Rel(p.det.Root, path)
				if it, ok := KnowledgeItem(p.env, rel, ReadText(path)); ok {
					p.add(it)
				}
				return nil
			})
		}
	}
}

// ---- skills, MCP, cron -----------------------------------------------------------

func (p *nanoclawPlanner) skills() {
	bundled := map[string]bool{}
	for _, sd := range ScanSkills(filepath.Join(p.det.Root, "container", "skills"), 1) {
		bundled[sd.Name] = true
	}
	var roots []string
	if p.v2 {
		roots, _ = filepath.Glob(filepath.Join(p.det.Root, "data", "v2-sessions", "*", ".claude-shared", "skills"))
	} else {
		roots, _ = filepath.Glob(filepath.Join(p.det.Root, "data", "sessions", "*", ".claude", "skills"))
	}
	sort.Strings(roots)
	seen := map[string]bool{}
	for _, r := range roots {
		for _, sd := range ScanSkills(r, 1) {
			if bundled[sd.Name] || seen[sd.Name] {
				continue
			}
			seen[sd.Name] = true
			p.add(SkillItem(p.env, sd))
		}
	}
	if !p.v2 {
		return
	}
	if g := p.mainGroup(); g != nil && g.id != "" {
		if cc := p.containerConfig(g.id); cc != nil {
			servers, _ := cc["mcp_servers"].(map[string]any)
			for _, name := range sortedKeys(servers) {
				s, _ := servers[name].(map[string]any)
				p.add(MCPItem(p.env, MCPPayload{Name: Slug(name), Transport: str(s["type"]), Command: str(s["command"]),
					Args: strList(s["args"]), Env: lettaStrMap(s["env"]), URL: str(s["url"]), Headers: lettaStrMap(s["headers"])}))
			}
		}
	}
}

func (p *nanoclawPlanner) cron() {
	if p.v2 {
		p.cronV2()
		return
	}
	db, err := zeroclawOpenDB(filepath.Join(p.det.Root, "store", "messages.db"))
	if err != nil {
		return
	}
	defer db.Close()
	cols := zeroclawColumns(db, "scheduled_tasks")
	if !cols["schedule_type"] {
		return
	}
	rows, err := db.Query("SELECT id, COALESCE(group_folder,''), prompt, schedule_type, schedule_value, " +
		zeroclawColOr(cols, "status", "'active'") + ", " + zeroclawColOr(cols, "script", "''") +
		" FROM scheduled_tasks ORDER BY " + zeroclawColOr(cols, "created_at", "''") + ", id")
	if err != nil {
		p.warn = append(p.warn, "scheduled_tasks could not be read: "+err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, folder, prompt, typ, val, status, script string
		if rows.Scan(&id, &folder, &prompt, &typ, &val, &status, &script) != nil || status == "completed" {
			continue
		}
		p.add(p.taskItem(id, prompt, typ, val, status == "active", folder, script))
	}
}

func (p *nanoclawPlanner) taskItem(id, prompt, typ, val string, active bool, folder, script string) Item {
	key := Slug(id)
	name := Truncate(firstLine(prompt), 48)
	if folder != "" && !(p.mainGroup() != nil && p.mainGroup().folder == folder) {
		name += " (" + folder + ")"
	}
	var sched string
	switch typ {
	case "cron":
		sched = val
	case "interval":
		ms, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil || ms <= 0 {
			// v2's own migrator wrote durations like "30m".
			d, derr := time.ParseDuration(val)
			if derr != nil {
				return UnsupportedItem(CatCron, key, name, "interval "+strconv.Quote(val)+" could not be read")
			}
			ms = d.Milliseconds()
		}
		sched = EverySchedule(time.Duration(ms) * time.Millisecond)
	case "once":
		return UnsupportedItem(CatCron, key, name, "one-shot schedule (runs once at "+val+")")
	default:
		return UnsupportedItem(CatCron, key, name, "unknown schedule type "+strconv.Quote(typ))
	}
	it := CronItem(key, CronPayload{Name: name, Schedule: sched, Prompt: prompt, Timezone: p.dotenv["TZ"]}, active)
	if script != "" && it.Status == StatusReady {
		it.Detail += " · its pre-run script is not migrated"
	}
	return it
}

func (p *nanoclawPlanner) cronV2() {
	dbs, _ := filepath.Glob(filepath.Join(p.det.Root, "data", "v2-sessions", "*", "*", "inbound.db"))
	sort.Strings(dbs)
	seen := map[string]bool{}
	for _, path := range dbs {
		db, err := zeroclawOpenDB(path)
		if err != nil {
			continue
		}
		p.cronV2DB(db, seen)
		db.Close()
	}
}

func (p *nanoclawPlanner) cronV2DB(db *sql.DB, seen map[string]bool) {
	cols := zeroclawColumns(db, "messages_in")
	if !cols["kind"] {
		return
	}
	rows, err := db.Query("SELECT id, " + zeroclawColOr(cols, "series_id", "''") + ", COALESCE(status,'pending'), " +
		zeroclawColOr(cols, "recurrence", "''") + ", " + zeroclawColOr(cols, "process_after", "''") + ", content FROM messages_in WHERE kind = 'task' ORDER BY seq")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, series, status, rec, after, content string
		if rows.Scan(&id, &series, &status, &rec, &after, &content) != nil {
			continue
		}
		key := firstNonEmpty(series, id)
		if seen[key] || status == "completed" || status == "failed" || status == "cancelled" {
			continue
		}
		seen[key] = true
		var c struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal([]byte(content), &c)
		if rec == "" {
			p.add(UnsupportedItem(CatCron, Slug(key), Truncate(firstLine(c.Prompt), 48), "one-shot schedule (runs once at "+after+")"))
			continue
		}
		p.add(p.taskItem(key, c.Prompt, "cron", rec, status != "paused", "", ""))
	}
}

// ---- channels, roles ------------------------------------------------------------------

func (p *nanoclawPlanner) channels() {
	if IsDir(filepath.Join(p.det.Root, "store", "auth")) {
		p.add(UnsupportedItem(CatChannel, "whatsapp", "WhatsApp",
			"NanoClaw's WhatsApp is a linked-device (Baileys) session; Antares supports the WhatsApp Cloud API only"))
	}
	e := p.dotenv
	add := func(platform string, fields map[string]any, missing string) {
		if list := p.chats[platform]; len(list) > 0 {
			switch platform {
			case "telegram":
				fields["allowed_chats"] = list
			case "slack":
				fields["allowed_channels"] = list
			}
		}
		p.add(ChannelItem(p.env, platform, "", fields, missing))
	}
	if t := e["TELEGRAM_BOT_TOKEN"]; t != "" {
		add("telegram", map[string]any{"bot_token": t}, "")
	}
	if t := e["DISCORD_BOT_TOKEN"]; t != "" {
		add("discord", map[string]any{"bot_token": t}, "")
	}
	if b := e["SLACK_BOT_TOKEN"]; b != "" {
		missing := ""
		if e["SLACK_APP_TOKEN"] == "" {
			missing = "app_token"
		}
		add("slack", map[string]any{"bot_token": b, "app_token": e["SLACK_APP_TOKEN"]}, missing)
	}
	for _, k := range sortedKeys(e) {
		for _, pre := range []string{"TELEGRAM_BOT_TOKEN_", "DISCORD_BOT_TOKEN_", "SLACK_BOT_TOKEN_"} {
			if name, ok := strings.CutPrefix(k, pre); ok && e[k] != "" {
				platform := strings.ToLower(strings.TrimSuffix(strings.SplitN(pre, "_", 2)[0], "_"))
				p.add(UnsupportedItem(CatChannel, platform+"-"+Slug(name), PlatformLabel(platform)+" ("+name+")",
					"Antares has one "+PlatformLabel(platform)+" bot; extra bot instances are not imported"))
			}
		}
	}
}

// roles: v2 agent groups other than the main one.
func (p *nanoclawPlanner) roles() {
	if !p.v2 {
		return
	}
	for _, g := range p.groups {
		if g.main {
			continue
		}
		prompt := ReadText(filepath.Join(p.groupDir(g.folder), "instructions.prepend.md"))
		model := ""
		if cc := p.containerConfig(g.id); cc != nil {
			model = str(cc["model"])
		}
		p.add(RoleItem(p.env, RolePayload{Name: firstNonEmpty(g.folder, g.name), Title: firstNonEmpty(g.name, g.folder),
			Summary: "NanoClaw agent group", Prompt: prompt, Model: model}))
	}
}
