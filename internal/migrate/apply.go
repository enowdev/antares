package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cron"
	"github.com/enowdev/antares/internal/roles"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
	"gopkg.in/yaml.v3"
)

// Deps are the live services Apply and Undo write through.
type Deps struct {
	// Store receives memories and cron jobs; required when the plan has any.
	Store store.Store
	// RAG indexes knowledge items; nil means RAG is off (items are skipped).
	RAG tools.RAGProvider
	// Now is the clock (UTC timestamps for backup dirs); nil = time.Now.
	Now func() time.Time
}

// ManifestVersion is bumped when the manifest layout changes.
const ManifestVersion = 1

// Manifest records what one Apply changed, so Undo can reverse it. It never
// holds secret values: only ids, paths and names.
type Manifest struct {
	Version   int       `json:"version"`
	Source    string    `json:"source"`
	Profile   string    `json:"profile,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Undone    bool      `json:"undone,omitempty"`
	Applied   []string  `json:"applied"`
	// Restore lists files/dirs Apply modified that existed before; each has a
	// copy under the backup dir (Backup is relative to it).
	Restore []BackupEntry `json:"restore,omitempty"`
	// CreatedPaths are files/dirs Apply created; Undo removes them.
	CreatedPaths []string `json:"created_paths,omitempty"`
	Memories     []string `json:"memories,omitempty"`
	CronJobs     []string `json:"cron_jobs,omitempty"`
	RAGDocs      []RAGRef `json:"rag_docs,omitempty"`
}

// BackupEntry maps a live path to its copy in the backup dir.
type BackupEntry struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
}

// RAGRef is one imported RAG document.
type RAGRef struct {
	Collection string `json:"collection"`
	DocID      string `json:"doc_id"`
}

// catOrder is the order Apply walks categories: providers before the model
// that may refer to a renamed one.
var catOrder = map[Category]int{
	CatProvider: 0, CatModel: 1, CatSoul: 2, CatAgentsMD: 3, CatUserMD: 4, CatMemory: 5,
	CatKnowledge: 6, CatSkill: 7, CatMCP: 8, CatCron: 9, CatChannel: 10, CatRole: 11,
}

// BackupsDir is where migration backups live.
func BackupsDir() string { return config.Path("backups") }

// RAGCollection is the RAG collection a source's knowledge goes into.
func RAGCollection(sourceID string) string { return "import-" + sourceID }

// errSkip marks an item that was deliberately not applied.
var errSkip = errors.New("skipped")

type applier struct {
	ctx      context.Context
	plan     Plan
	deps     Deps
	cfg      *config.Config
	dirty    bool
	restart  bool
	man      *Manifest
	dir      string
	renames  map[string]string // source-proposed provider id → applied id
	backedUp map[string]bool
	memIDs   map[string]bool
	roleReg  *roles.Registry
	ragDocs  []tools.RAGDoc
	ragItems []string
}

// Apply writes the chosen items of plan into Antares, after backing up what
// it will change. Only items named in choices are considered; unsupported
// items, conflicts without a resolution (or with "skip") and needs_input
// items without input are skipped. The config file is written once at the
// end. The caller reloads the server afterwards.
func Apply(ctx context.Context, plan Plan, choices []Choice, deps Deps) (Report, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	rep := Report{Source: plan.Detection.Source, Applied: []string{}, Skipped: []string{}}
	cfg, err := config.Reload()
	if err != nil {
		return rep, fmt.Errorf("load config: %w", err)
	}
	a := &applier{ctx: ctx, plan: plan, deps: deps, cfg: cfg, renames: map[string]string{},
		backedUp: map[string]bool{}, memIDs: map[string]bool{}}
	a.roleReg = roles.NewRegistry(RoleDirs(cfg))
	_ = a.roleReg.Reload()

	if err := a.startBackup(); err != nil {
		return rep, err
	}
	rep.Backup = filepath.Base(a.dir)

	byID := make(map[string]Item, len(plan.Items))
	for _, it := range plan.Items {
		byID[it.ID] = it
	}
	type chosen struct {
		it Item
		ch Choice
	}
	var work []chosen
	seen := map[string]bool{}
	for _, ch := range choices {
		if seen[ch.ID] {
			continue
		}
		seen[ch.ID] = true
		it, ok := byID[ch.ID]
		if !ok {
			rep.Failed = append(rep.Failed, ItemFailure{ID: ch.ID, Error: "not in the plan"})
			continue
		}
		work = append(work, chosen{it, ch})
	}
	sort.SliceStable(work, func(i, j int) bool { return catOrder[work[i].it.Category] < catOrder[work[j].it.Category] })

	if a.hasCat(work2items(work, func(c chosen) Item { return c.it }), CatMemory) && deps.Store != nil {
		a.loadMemoryIDs()
	}

	for _, w := range work {
		err := a.applyItem(w.it, w.ch)
		switch {
		case err == nil:
			rep.Applied = append(rep.Applied, w.it.ID)
		case errors.Is(err, errSkip):
			rep.Skipped = append(rep.Skipped, w.it.ID)
		default:
			slog.Warn("migrate: item failed", "source", plan.Detection.Source, "item", w.it.ID, "error", err)
			rep.Failed = append(rep.Failed, ItemFailure{ID: w.it.ID, Error: err.Error()})
		}
	}

	if len(a.ragDocs) > 0 {
		if err := a.flushRAG(); err != nil {
			// Move the knowledge items from applied to failed.
			failed := map[string]bool{}
			for _, id := range a.ragItems {
				failed[id] = true
				rep.Failed = append(rep.Failed, ItemFailure{ID: id, Error: "RAG indexing failed: " + err.Error()})
			}
			kept := rep.Applied[:0]
			for _, id := range rep.Applied {
				if !failed[id] {
					kept = append(kept, id)
				}
			}
			rep.Applied = kept
		}
	}

	if a.dirty {
		if err := config.Save(a.cfg); err != nil {
			return rep, fmt.Errorf("save config: %w", err)
		}
	}
	rep.NeedsRestart = a.restart
	a.man.Applied = rep.Applied
	if err := a.writeManifest(); err != nil {
		return rep, err
	}
	slog.Info("migrate: applied", "source", plan.Detection.Source, "applied", len(rep.Applied),
		"skipped", len(rep.Skipped), "failed", len(rep.Failed), "backup", rep.Backup)
	return rep, nil
}

func work2items[T any](ws []T, f func(T) Item) []Item {
	out := make([]Item, len(ws))
	for i, w := range ws {
		out[i] = f(w)
	}
	return out
}

func (a *applier) hasCat(items []Item, c Category) bool {
	for _, it := range items {
		if it.Category == c {
			return true
		}
	}
	return false
}

// resolution decides whether an item proceeds and how. It returns errSkip
// for anything that must not be applied.
func resolution(it Item, ch Choice) (Resolution, error) {
	switch it.Status {
	case StatusUnsupported:
		return "", errSkip
	case StatusNeedsInput:
		if strings.TrimSpace(ch.Input) == "" {
			return "", errSkip
		}
		return "", nil
	case StatusConflict:
		switch ch.Resolution {
		case "", ResolveSkip:
			return "", errSkip
		case ResolveReplace, ResolveRename, ResolveAppend:
			return ch.Resolution, nil
		default:
			return "", fmt.Errorf("unknown resolution %q", ch.Resolution)
		}
	}
	return "", nil
}

func (a *applier) applyItem(it Item, ch Choice) error {
	res, err := resolution(it, ch)
	if err != nil {
		return err
	}
	allowed := func(rs ...Resolution) error {
		if res == "" {
			return nil
		}
		for _, r := range rs {
			if r == res {
				return nil
			}
		}
		return fmt.Errorf("%s is not a valid resolution for %s", res, it.Category)
	}
	p := it.Payload
	input := strings.TrimSpace(ch.Input)
	switch it.Category {
	case CatProvider:
		if p.Provider == nil {
			break
		}
		if err := allowed(ResolveReplace, ResolveRename); err != nil {
			return err
		}
		return a.provider(*p.Provider, res, input, it)
	case CatModel:
		if p.Model == nil {
			break
		}
		if err := allowed(ResolveReplace); err != nil {
			return err
		}
		return a.model(*p.Model)
	case CatSoul, CatAgentsMD, CatUserMD:
		if p.Text == nil {
			break
		}
		if err := allowed(ResolveReplace, ResolveAppend); err != nil {
			return err
		}
		return a.text(it.Category, *p.Text, res)
	case CatMemory:
		if p.Memory == nil {
			break
		}
		return a.memory(*p.Memory)
	case CatKnowledge:
		if p.Knowledge == nil {
			break
		}
		if a.deps.RAG == nil {
			return errSkip
		}
		a.ragDocs = append(a.ragDocs, tools.RAGDoc{ID: p.Knowledge.Path, Path: p.Knowledge.Path, Content: p.Knowledge.Content,
			Meta: map[string]any{"imported_from": a.plan.Detection.Source}})
		a.ragItems = append(a.ragItems, it.ID)
		return nil
	case CatSkill:
		if p.Skill == nil {
			break
		}
		if err := allowed(ResolveReplace, ResolveRename); err != nil {
			return err
		}
		return a.skill(*p.Skill, res)
	case CatMCP:
		if p.MCP == nil {
			break
		}
		if err := allowed(ResolveReplace, ResolveRename); err != nil {
			return err
		}
		return a.mcp(*p.MCP, res)
	case CatCron:
		if p.Cron == nil {
			break
		}
		if err := allowed(ResolveReplace); err != nil {
			return err
		}
		return a.cronJob(it, *p.Cron)
	case CatChannel:
		if p.Channel == nil {
			break
		}
		if err := allowed(ResolveReplace); err != nil {
			return err
		}
		return a.channel(*p.Channel, it.Input, input)
	case CatRole:
		if p.Role == nil {
			break
		}
		if err := allowed(ResolveReplace, ResolveRename); err != nil {
			return err
		}
		return a.role(*p.Role, res)
	}
	return fmt.Errorf("item has no data to apply")
}

// ---- providers & model ----------------------------------------------------------

func (a *applier) provider(p ProviderPayload, res Resolution, input string, it Item) error {
	if input != "" && (it.Input == "api_key" || it.Status == StatusNeedsInput) {
		p.APIKey = input
	}
	id := p.ID
	if a.cfg.Providers == nil {
		a.cfg.Providers = map[string]config.Provider{}
	}
	existing, exists := a.cfg.Providers[id]
	if exists && res == "" && providerPlaceholder(existing) {
		res = ResolveReplace // a keyless placeholder is filled in, not a conflict
	}
	if exists && res == "" {
		if sameURL(existing.BaseURL, p.BaseURL) && strings.TrimSpace(existing.APIKey) == strings.TrimSpace(p.APIKey) {
			a.renames[p.ID] = id
			return nil // identical: idempotent no-op
		}
		return fmt.Errorf("provider %q now exists in Antares; re-plan to resolve the conflict", id)
	}
	if res == ResolveRename {
		id = NextFreeName(id, func(s string) bool { _, t := a.cfg.Providers[s]; return t || isCatalogueID(s) && s != p.ID })
		if id == p.ID {
			id = NextFreeName(p.ID+"-2", func(s string) bool { _, t := a.cfg.Providers[s]; return t })
		}
	}
	entry := config.Provider{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers,
		Models: p.Models, Label: p.Label, Enabled: true,
	}
	if res == ResolveRename && entry.Label != "" {
		entry.Label += " (" + a.plan.Detection.Name + ")"
	}
	if res == ResolveReplace && exists {
		// Keep per-model metadata the user added by hand.
		entry.ModelMeta = existing.ModelMeta
		entry.TimeoutSecs = existing.TimeoutSecs
	}
	a.cfg.Providers[id] = entry
	a.renames[p.ID] = id
	a.dirty = true
	return nil
}

func (a *applier) model(m ModelPayload) error {
	prov := m.Provider
	if r, ok := a.renames[prov]; ok {
		prov = r
	}
	if prov != "" {
		if _, ok := a.cfg.Providers[prov]; !ok {
			return fmt.Errorf("provider %q is not configured; import it first", prov)
		}
	}
	if strings.TrimSpace(m.Model) == "" {
		return fmt.Errorf("no model name")
	}
	if a.cfg.Model.Default == m.Model && a.cfg.Model.Provider == prov {
		return nil
	}
	a.cfg.Model.Default = m.Model
	a.cfg.Model.Provider = prov
	if len(m.Fallback) > 0 {
		fb := make([]string, 0, len(m.Fallback))
		for _, f := range m.Fallback {
			if pid, model, ok := strings.Cut(f, "/"); ok {
				if r, ok := a.renames[pid]; ok {
					f = r + "/" + model
				}
			}
			fb = append(fb, f)
		}
		a.cfg.Model.Fallback = fb
	}
	a.dirty = true
	return nil
}

// ---- persona text files -------------------------------------------------------

func (a *applier) text(cat Category, t TextPayload, res Resolution) error {
	path := TextPath(cat)
	current := ""
	if b, err := os.ReadFile(path); err == nil {
		current = strings.TrimSpace(string(b))
	}
	state := textFileState(cat)
	content := strings.TrimSpace(t.Content)
	var next string
	switch {
	case res == ResolveAppend && state == "custom":
		if strings.Contains(current, content) {
			return nil // already appended
		}
		heading := "## Imported from " + a.plan.Detection.Name
		if t.From != "" {
			heading += " (" + t.From + ")"
		}
		next = current + "\n\n" + heading + "\n\n" + content
	case state == "custom" && res == "":
		if current == content {
			return nil
		}
		return fmt.Errorf("%s is now custom; re-plan to resolve the conflict", filepath.Base(path))
	default: // missing, default placeholder, or replace
		if current == content {
			return nil
		}
		next = content
	}
	if err := a.backup(path); err != nil {
		return err
	}
	switch cat {
	case CatSoul:
		return config.SaveSoul(next)
	case CatAgentsMD:
		return config.SaveAgentsMD(next)
	default:
		return config.SaveUserMD(next)
	}
}

// ---- memory & knowledge ------------------------------------------------------

func (a *applier) loadMemoryIDs() {
	for _, scope := range []string{"global", "user"} {
		ms, err := a.deps.Store.ListMemories(a.ctx, scope, "", 1000)
		if err != nil {
			continue
		}
		for _, m := range ms {
			a.memIDs[m.ID] = true
		}
	}
}

// MemoryID is the deterministic store id of an imported memory, so a second
// import of the same entry is a no-op.
func MemoryID(sourceID, content string) string {
	return "import-" + sourceID + "-" + ContentHash(content)
}

func (a *applier) memory(m MemoryPayload) error {
	if a.deps.Store == nil {
		return fmt.Errorf("no database available")
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return errSkip
	}
	id := MemoryID(a.plan.Detection.Source, content)
	if a.memIDs[id] {
		return nil
	}
	scope := m.Scope
	if scope != "user" {
		scope = "global"
	}
	tags, _ := json.Marshal(append([]string{"imported", a.plan.Detection.Source}, m.Tags...))
	rec := &store.Memory{ID: id, Scope: scope, Key: m.Key, Content: content, Tags: string(tags),
		Source: "import:" + a.plan.Detection.Source}
	if err := a.deps.Store.PutMemory(a.ctx, rec); err != nil {
		return fmt.Errorf("save memory: %w", err)
	}
	a.memIDs[id] = true
	a.man.Memories = append(a.man.Memories, id)
	return nil
}

func (a *applier) flushRAG() error {
	coll := RAGCollection(a.plan.Detection.Source)
	if _, err := a.deps.RAG.Index(a.ctx, coll, a.ragDocs); err != nil {
		return err
	}
	for _, d := range a.ragDocs {
		a.man.RAGDocs = append(a.man.RAGDocs, RAGRef{Collection: coll, DocID: d.ID})
	}
	return nil
}

// ---- skills ---------------------------------------------------------------------

func (a *applier) skill(s SkillPayload, res Resolution) error {
	if !IsFile(filepath.Join(s.Dir, "SKILL.md")) {
		return fmt.Errorf("source skill folder is gone")
	}
	root := SkillDirs(a.cfg)[0]
	name := s.Name
	exists := func(n string) bool {
		for _, d := range SkillDirs(a.cfg) {
			if IsDir(filepath.Join(d, n)) || IsFile(filepath.Join(d, n+".md")) {
				return true
			}
		}
		return false
	}
	dest := filepath.Join(root, name)
	switch {
	case res == ResolveRename:
		name = NextFreeName(name, exists)
		dest = filepath.Join(root, name)
	case res == ResolveReplace:
		if IsDir(dest) {
			if err := a.backup(dest); err != nil {
				return err
			}
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
	case exists(name):
		if sameTree(s.Dir, dest) {
			return nil
		}
		return fmt.Errorf("skill %q now exists in Antares; re-plan to resolve the conflict", name)
	}
	if err := copyTree(s.Dir, dest); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("copy skill: %w", err)
	}
	if name != s.Name {
		renameSkillFrontMatter(filepath.Join(dest, "SKILL.md"), name)
	}
	if !a.backedUp[dest] {
		a.man.CreatedPaths = append(a.man.CreatedPaths, dest)
	}
	return nil
}

// renameSkillFrontMatter rewrites "name:" in SKILL.md front matter so a
// renamed copy does not shadow the skill it was renamed away from.
func renameSkillFrontMatter(path, name string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(b)
	if !strings.HasPrefix(text, "---") {
		return
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return
	}
	head := text[:3+end]
	lines := strings.Split(head, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "name:") {
			lines[i] = "name: " + name
		}
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+text[3+end:]), 0o644)
}

// ---- MCP ------------------------------------------------------------------------

func (a *applier) mcp(m MCPPayload, res Resolution) error {
	if a.cfg.MCP.Servers == nil {
		a.cfg.MCP.Servers = map[string]config.MCPServer{}
	}
	name := m.Name
	entry := config.MCPServer{Transport: NormalizeTransport(m.Transport, m.URL), Command: m.Command, Args: m.Args,
		Env: m.Env, URL: m.URL, Headers: m.Headers, Enabled: true}
	if old, exists := a.cfg.MCP.Servers[name]; exists {
		switch res {
		case ResolveRename:
			name = NextFreeName(name, func(s string) bool { _, t := a.cfg.MCP.Servers[s]; return t })
		case ResolveReplace:
		default:
			if sameMCP(old, entry) {
				return nil
			}
			return fmt.Errorf("MCP server %q now exists in Antares; re-plan to resolve the conflict", name)
		}
	}
	a.cfg.MCP.Servers[name] = entry
	a.cfg.MCP.Enabled = true
	a.dirty, a.restart = true, true
	return nil
}

func sameMCP(a, b config.MCPServer) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// ---- cron -------------------------------------------------------------------------

// CronJobID is the deterministic id of an imported schedule.
func CronJobID(sourceID, itemID string) string {
	return "import-" + sourceID + "-" + ContentHash(itemID)
}

func (a *applier) cronJob(it Item, c CronPayload) error {
	if a.deps.Store == nil {
		return fmt.Errorf("no database available")
	}
	loc := time.Local
	if tz := strings.TrimSpace(c.Timezone); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("unknown timezone %q", tz)
		}
		loc = l
	}
	next, err := cron.Validate(c.Schedule, loc)
	if err != nil {
		return fmt.Errorf("invalid schedule %q: %v", c.Schedule, err)
	}
	id := CronJobID(a.plan.Detection.Source, it.ID)
	if j, err := a.deps.Store.GetCronJob(a.ctx, id); err == nil && j != nil {
		return nil // imported before; keep whatever the user did with it since
	}
	job := &store.CronJob{ID: id, Name: c.Name, Schedule: c.Schedule, Prompt: c.Prompt, Timezone: c.Timezone,
		Enabled: false, NextRun: &next, Meta: store.Meta{"imported_from": a.plan.Detection.Source}}
	if err := a.deps.Store.PutCronJob(a.ctx, job); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	a.man.CronJobs = append(a.man.CronJobs, id)
	return nil
}

// ---- channels ---------------------------------------------------------------

func (a *applier) channel(c ChannelPayload, inputField, input string) error {
	fields := map[string]any{}
	for k, v := range c.Fields {
		fields[k] = v
	}
	if input != "" && inputField != "" {
		fields[inputField] = input
	}
	if err := setGatewayFields(&a.cfg.Gateway, c.Platform, fields); err != nil {
		return err
	}
	a.cfg.Gateway.Enabled = true
	a.dirty, a.restart = true, true
	return nil
}

// setGatewayFields overlays fields (by YAML name) onto gateway.<platform> and
// enables it. Unknown field names are an error, so a source typo cannot be
// silently dropped.
func setGatewayFields(g *config.Gateway, platform string, fields map[string]any) error {
	var target any
	switch platform {
	case "telegram":
		target = &g.Telegram
	case "discord":
		target = &g.Discord
	case "slack":
		target = &g.Slack
	case "matrix":
		target = &g.Matrix
	case "signal":
		target = &g.Signal
	case "whatsapp":
		target = &g.WhatsApp
	case "feishu":
		target = &g.Feishu
	default:
		return fmt.Errorf("Antares has no %s gateway", platform)
	}
	raw, err := yaml.Marshal(target)
	if err != nil {
		return err
	}
	cur := map[string]any{}
	if err := yaml.Unmarshal(raw, &cur); err != nil {
		return err
	}
	for k, v := range fields {
		if _, ok := cur[k]; !ok {
			return fmt.Errorf("%s has no field %q", platform, k)
		}
		cur[k] = v
	}
	cur["enabled"] = true
	out, err := yaml.Marshal(cur)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(out, target); err != nil {
		return fmt.Errorf("%s: a field has the wrong type", platform)
	}
	return nil
}

// ---- roles -------------------------------------------------------------------------

func (a *applier) role(r RolePayload, res Resolution) error {
	name := Slug(r.Name)
	existing, exists := a.roleReg.Get(name)
	switch {
	case exists && res == ResolveRename:
		name = NextFreeName(name, func(s string) bool { _, t := a.roleReg.Get(s); return t })
		exists = false
	case exists && res == ResolveReplace:
		if existing.Source == "builtin" {
			return fmt.Errorf("%q is a built-in role and cannot be replaced; use rename", name)
		}
		if err := a.backup(existing.Path); err != nil {
			return err
		}
	case exists:
		return fmt.Errorf("role %q now exists in Antares; re-plan to resolve the conflict", name)
	}
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = name
	}
	saved, err := a.roleReg.Save(roles.Role{Name: name, Title: title, Summary: r.Summary, Prompt: r.Prompt,
		Model: r.Model, Category: "imported", Tags: []string{"imported", a.plan.Detection.Source}})
	if err != nil {
		return err
	}
	if !exists && saved.Path != "" {
		a.man.CreatedPaths = append(a.man.CreatedPaths, saved.Path)
	}
	return nil
}

// ---- backup & manifest ----------------------------------------------------------

func (a *applier) startBackup() error {
	if err := config.EnsureHome(); err != nil {
		return err
	}
	stamp := a.deps.Now().UTC().Format("20060102T150405Z")
	base := filepath.Join(BackupsDir(), "migrate-"+Slug(a.plan.Detection.Source)+"-"+stamp)
	dir := base
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			break
		}
		dir = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return err
	}
	a.dir = dir
	a.man = &Manifest{Version: ManifestVersion, Source: a.plan.Detection.Source, Profile: a.plan.Detection.Profile,
		CreatedAt: a.deps.Now().UTC()}
	// The config file is always backed up: it is the file most writes touch.
	if err := a.backup(config.ConfigFile()); err != nil {
		return err
	}
	// The persona files are copied too (contract), but only restored when
	// Apply actually changed them — see backup().
	for _, cat := range []Category{CatSoul, CatAgentsMD, CatUserMD} {
		p := TextPath(cat)
		if IsFile(p) {
			if err := copyFile(p, filepath.Join(dir, "snapshot", filepath.Base(p)), 0o600); err != nil {
				return err
			}
		}
	}
	return a.writeManifest()
}

// backup records path (file or dir) for restore on undo. A path that does not
// exist yet is recorded as created instead, so undo removes it.
func (a *applier) backup(path string) error {
	if a.backedUp[path] {
		return nil
	}
	a.backedUp[path] = true
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		a.man.CreatedPaths = append(a.man.CreatedPaths, path)
		return nil
	}
	if err != nil {
		return err
	}
	rel := fmt.Sprintf("%02d-%s", len(a.man.Restore), filepath.Base(path))
	dst := filepath.Join(a.dir, "files", rel)
	if st.IsDir() {
		err = copyTree(path, dst)
	} else {
		err = copyFile(path, dst, st.Mode().Perm())
	}
	if err != nil {
		return fmt.Errorf("backup %s: %w", filepath.Base(path), err)
	}
	a.man.Restore = append(a.man.Restore, BackupEntry{Path: path, Backup: filepath.Join("files", rel)})
	return a.writeManifest()
}

func (a *applier) writeManifest() error {
	return writeManifest(a.dir, a.man)
}

func writeManifest(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
}

// ---- file helpers ---------------------------------------------------------------

func copyFile(src, dst string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyTree copies regular files and folders from src to dst. Symlinks and
// special files are skipped: a skill folder from elsewhere must not smuggle a
// link into the Antares home.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			if rel != "." && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil
	})
}

// sameTree reports whether two skill folders have an identical SKILL.md (a
// cheap check that a re-import is a no-op).
func sameTree(a, b string) bool {
	x, err1 := os.ReadFile(filepath.Join(a, "SKILL.md"))
	y, err2 := os.ReadFile(filepath.Join(b, "SKILL.md"))
	return err1 == nil && err2 == nil && string(x) == string(y)
}
