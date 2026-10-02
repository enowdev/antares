package migrate

// ZeroClaw (github.com/zeroclaw-labs/zeroclaw). Formats read from the
// project's Rust source on master (2026-10-03); B below is
// https://github.com/zeroclaw-labs/zeroclaw/blob/master and CFG is
// B/crates/zeroclaw-config/src.
//
//   - Install root (CFG/schema.rs default_config_dir /
//     resolve_runtime_config_dirs): $ZEROCLAW_CONFIG_DIR, else
//     $ZEROCLAW_DATA_DIR / $ZEROCLAW_WORKSPACE (the dir itself when it holds
//     config.toml, else ../.zeroclaw), else Homebrew <prefix>/var/zeroclaw,
//     else ~/.zeroclaw. config.toml lives at the root.
//   - Three config schemas coexist on disk because the daemon migrates in
//     memory only (CFG/migration.rs detect_version; schema_version missing =
//     V1):
//     V1 (CFG/schema/v1.rs): top-level api_key, api_url, default_provider
//     (alias model_provider, default "openrouter", "custom:<url>" allowed),
//     default_model (alias model), [model_providers.<name>] {api_key,
//     base_url, model}, [reliability] fallback_providers, channels under
//     [channels_config.<type>].
//     V2 (CFG/schema/v2.rs): [providers] fallback = "<name>",
//     [providers.models.<name>] {api_key, base_url, api_path, model};
//     [channels.<type>]; [[cron.jobs]]; [agents.<name>] {provider, model}.
//     V3 (CFG/providers.rs, CFG/schema.rs ModelProviderConfig):
//     [providers.models.<family>.<alias>] {api_key, uri, model, fallback =
//     ["family.alias"]}, [agents.<alias>] {model_provider = "family.alias"},
//     [channels.<type>.<alias>] with allowlists in [peer_groups.<type>_<alias>]
//     external_peers, [cron.<alias>] {name, job_type, prompt, schedule =
//     {kind, expr|every_ms|at, tz}, enabled}.
//   - Secrets (CFG/secrets.rs): "enc2:<hex>" = ChaCha20-Poly1305 with the
//     32-byte key hex-encoded in <root>/.secret_key, blob = nonce(12) ||
//     ciphertext || tag, no AAD; legacy "enc:<hex>" = XOR with the key;
//     "op://…" = 1Password reference (not resolved here → needs_input).
//   - MCP (CFG/schema.rs McpConfig): [[mcp.servers]] {name, transport
//     stdio|http|sse, command, args, env, url, headers} (alias mcpServers).
//   - Workspace (B/docs/book/src/architecture/runtime-state-and-persistence.md,
//     B/crates/zeroclaw-runtime/src/agent/system_prompt.rs): V3
//     <root>/agents/<alias>/workspace/, before V3 <root>/workspace/. Files
//     AGENTS.md, SOUL.md, IDENTITY.md, USER.md, MEMORY.md ("- **key**:
//     content" lines, B/crates/zeroclaw-memory/src/markdown.rs) and daily
//     memory/YYYY-MM-DD.md.
//   - SQLite memory (B/crates/zeroclaw-memory/src/sqlite.rs):
//     data/memory/brain.db (pre-V3 workspace/memory/brain.db), table
//     memories(key, content, category, superseded_by, agent_id…).
//   - Runtime cron store (B/crates/zeroclaw-runtime/src/cron/store.rs):
//     data/cron/jobs.db table cron_jobs(id, name, job_type, prompt, schedule
//     JSON, enabled, …).
//   - Skills (B/crates/zeroclaw-runtime/src/skills/mod.rs):
//     <workspace>/skills/<name>/ and <root>/shared/skills/<bundle>/<name>/;
//     SKILL.md folders are imported, SKILL.toml-only skills are listed as
//     unsupported.
//   - Running: <root>/state/daemon_state.json {pid, written_at} rewritten
//     every 5 s; launchd com.zeroclaw.daemon; process "zeroclaw".
//
// UNVERIFIED: V1 [reliability] model_fallbacks shape (ignored); whether V2
// providers.fallback names the default provider (treated so); V2 agents had
// no system prompt, so they become roles only when their workspace has
// persona files; [[model_routes]] hint routes are not imported; channel types
// beyond those listed in CFG/schema.rs are reported unsupported by name.

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	_ "modernc.org/sqlite"
)

type zeroclawSource struct{}

func init() { Register(zeroclawSource{}) }

func (zeroclawSource) ID() string   { return "zeroclaw" }
func (zeroclawSource) Name() string { return "ZeroClaw" }

// zeroclawDefaultRoot follows resolve_runtime_config_dirs.
func zeroclawDefaultRoot() string {
	if v := strings.TrimSpace(os.Getenv("ZEROCLAW_CONFIG_DIR")); v != "" {
		return ExpandHome(v)
	}
	for _, env := range []string{"ZEROCLAW_DATA_DIR", "ZEROCLAW_WORKSPACE"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			dir := ExpandHome(v)
			if IsFile(filepath.Join(dir, "config.toml")) {
				return dir
			}
			return filepath.Join(filepath.Dir(dir), ".zeroclaw")
		}
	}
	home := filepath.Join(HomeDir(), ".zeroclaw")
	if zeroclawLooksInstalled(home) {
		return home
	}
	for _, prefix := range []string{"/opt/homebrew", "/usr/local"} {
		if d := filepath.Join(prefix, "var", "zeroclaw"); zeroclawLooksInstalled(d) {
			return d
		}
	}
	return home
}

func zeroclawLooksInstalled(dir string) bool {
	return IsFile(filepath.Join(dir, "config.toml")) ||
		IsDir(filepath.Join(dir, "agents", "default", "workspace")) ||
		IsDir(filepath.Join(dir, "workspace")) && IsFile(filepath.Join(dir, ".secret_key"))
}

func (s zeroclawSource) Detect(ctx context.Context, root string) (Detection, error) {
	if root == "" {
		root = zeroclawDefaultRoot()
	}
	root = ExpandHome(root)
	if !zeroclawLooksInstalled(root) {
		return Detection{}, ErrNotFound
	}
	d := Detection{Source: s.ID(), Name: s.Name(), Root: root}
	d.Running = zeroclawDaemonRunning(root) ||
		RunningCheck([]string{"zeroclaw daemon", "zeroclaw service run", "zeroclaw --config-dir"}, []string{"com.zeroclaw.daemon"})
	p := s.plan(d, nil, false)
	if v := p.version; v > 0 {
		d.Version = "config schema v" + strconv.Itoa(v)
	}
	d.Summary = Summarize(p.items)
	return d, nil
}

// zeroclawPIDAlive reports whether pid is live; tests replace it.
var zeroclawPIDAlive = func(pid int) bool {
	if pid <= 0 || runtime.GOOS == "windows" {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// zeroclawNow is the clock for daemon_state freshness; tests replace it.
var zeroclawNow = time.Now

// zeroclawDaemonRunning reads state/daemon_state.json: a live pid whose state
// was written in the last minute (the daemon rewrites it every 5 s).
func zeroclawDaemonRunning(root string) bool {
	var st struct {
		PID       int    `json:"pid"`
		WrittenAt string `json:"written_at"`
		UpdatedAt string `json:"updated_at"`
	}
	if ReadJSON(filepath.Join(root, "state", "daemon_state.json"), &st) != nil || st.PID <= 0 {
		return false
	}
	if t, err := time.Parse(time.RFC3339, firstNonEmpty(st.WrittenAt, st.UpdatedAt)); err == nil && zeroclawNow().Sub(t) > time.Minute {
		return false
	}
	return zeroclawPIDAlive(st.PID)
}

func (s zeroclawSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	if !IsDir(det.Root) {
		return Plan{}, ErrNotFound
	}
	p := s.plan(det, env, true)
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

type zeroclawPlanner struct {
	det     Detection
	env     Env
	decrypt bool // false during Detect: encrypted values are not opened
	key     []byte
	keyErr  error
	cfg     map[string]any
	version int
	items   []Item
	warn    []string

	planned  map[string]bool   // provider ids already planned
	byRef    map[string]string // "family.alias" (V3) or name (V1/V2) → Antares id
	modelFor map[string]string // same refs → their model
}

func (s zeroclawSource) plan(det Detection, env Env, decrypt bool) *zeroclawPlanner {
	p := &zeroclawPlanner{det: det, env: env, decrypt: decrypt, cfg: map[string]any{},
		planned: map[string]bool{}, byRef: map[string]string{}, modelFor: map[string]string{}}
	path := filepath.Join(det.Root, "config.toml")
	if IsFile(path) {
		if m, err := zeroclawReadTOML(path); err != nil {
			p.warn = append(p.warn, "config.toml could not be read: "+err.Error())
		} else {
			p.cfg = m
		}
	}
	p.version = 1
	if v, ok := p.cfg["schema_version"].(int); ok {
		p.version = v
	}
	if !IsFile(path) {
		p.version = 0
	}
	if decrypt {
		p.key, p.keyErr = zeroclawLoadKey(filepath.Join(det.Root, ".secret_key"))
	}
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

func (p *zeroclawPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// secret opens a possibly encrypted config value. ok is false when the value
// is set but could not be read (left for the user to enter).
func (p *zeroclawPlanner) secret(v any) (string, bool) {
	s := str(v)
	if s == "" {
		return "", true
	}
	if !zeroclawEncrypted(s) {
		return s, true
	}
	if !p.decrypt {
		return "", false
	}
	if p.keyErr != nil {
		return "", false
	}
	out, err := zeroclawDecryptValue(s, p.key)
	if err != nil {
		return "", false
	}
	return out, true
}

func zeroclawEncrypted(s string) bool {
	return strings.HasPrefix(s, "enc2:") || strings.HasPrefix(s, "enc:") || strings.HasPrefix(s, "op://")
}

// zeroclawLoadKey reads a hex-encoded 32-byte key file (.secret_key).
func zeroclawLoadKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return zeroclawParseKey(string(b))
}

func zeroclawParseKey(s string) ([]byte, error) {
	k, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(k) != chacha20poly1305.KeySize {
		return nil, errors.New("secret key is not 32 hex-encoded bytes")
	}
	return k, nil
}

// zeroclawDecryptValue opens "enc2:" (ChaCha20-Poly1305, nonce||ct||tag, hex)
// and legacy "enc:" (XOR with the key) values; ZeroClaw's SecretStore and
// OpenHuman's (which inherited it) use the same scheme. Other prefixes, such
// as "op://", are an error.
func zeroclawDecryptValue(v string, key []byte) (string, error) {
	switch {
	case strings.HasPrefix(v, "enc2:"):
		blob, err := hex.DecodeString(strings.TrimPrefix(v, "enc2:"))
		if err != nil {
			return "", errors.New("enc2 value is not hex")
		}
		out, err := zeroclawOpen(blob, key)
		return string(out), err
	case strings.HasPrefix(v, "enc:"):
		ct, err := hex.DecodeString(strings.TrimPrefix(v, "enc:"))
		if err != nil || len(key) == 0 {
			return "", errors.New("enc value is not hex")
		}
		out := make([]byte, len(ct))
		for i := range ct {
			out[i] = ct[i] ^ key[i%len(key)]
		}
		return string(out), nil
	case strings.HasPrefix(v, "op://"):
		return "", errors.New("1Password reference")
	}
	return v, nil
}

// zeroclawOpen decrypts nonce(12) || ciphertext || tag(16).
func zeroclawOpen(blob, key []byte) ([]byte, error) {
	var aead cipher.AEAD
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	return aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], nil)
}

func zeroclawReadTOML(path string) (map[string]any, error) {
	b, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	return zeroclawParseTOML(string(b))
}

// ---- providers and model ---------------------------------------------------------

// zeroclawEntry is one provider definition in any schema.
type zeroclawEntry struct {
	ref     string // how other config refers to it
	family  string // vendor name
	alias   string // V3 alias ("default" otherwise)
	key     any
	baseURL string
	model   string
	fbRefs  []string
}

func (p *zeroclawPlanner) entries() []zeroclawEntry {
	var out []zeroclawEntry
	if p.version >= 3 {
		models, _ := zeroclawMap(p.cfg, "providers", "models").(map[string]any)
		for _, fam := range sortedKeys(models) {
			aliases, _ := models[fam].(map[string]any)
			for _, alias := range sortedKeys(aliases) {
				e, _ := aliases[alias].(map[string]any)
				if e == nil {
					continue
				}
				out = append(out, zeroclawEntry{ref: fam + "." + alias, family: fam, alias: alias, key: e["api_key"],
					baseURL: str(e["uri"]), model: str(e["model"]), fbRefs: strList(e["fallback"])})
			}
		}
		return out
	}
	if p.version == 2 {
		models, _ := zeroclawMap(p.cfg, "providers", "models").(map[string]any)
		for _, name := range sortedKeys(models) {
			e, _ := models[name].(map[string]any)
			if e == nil {
				continue
			}
			out = append(out, zeroclawEntry{ref: name, family: name, alias: "default", key: e["api_key"],
				baseURL: str(e["base_url"]), model: str(e["model"])})
		}
		return out
	}
	// V1: the top-level provider, then [model_providers.<name>] overrides.
	def := firstNonEmpty(str(p.cfg["default_provider"]), str(p.cfg["model_provider"]))
	if def == "" && len(p.cfg) > 0 {
		def = "openrouter"
	}
	extra, _ := p.cfg["model_providers"].(map[string]any)
	if def != "" {
		e := zeroclawEntry{ref: def, family: def, alias: "default", key: p.cfg["api_key"], baseURL: str(p.cfg["api_url"]),
			model: firstNonEmpty(str(p.cfg["default_model"]), str(p.cfg["model"]))}
		if o, _ := extra[def].(map[string]any); o != nil {
			if str(e.key) == "" {
				e.key = o["api_key"]
			}
			e.baseURL = firstNonEmpty(e.baseURL, str(o["base_url"]))
			e.model = firstNonEmpty(e.model, str(o["model"]))
		}
		out = append(out, e)
	}
	for _, name := range sortedKeys(extra) {
		if name == def {
			continue
		}
		o, _ := extra[name].(map[string]any)
		if o == nil {
			continue
		}
		out = append(out, zeroclawEntry{ref: name, family: name, alias: "default", key: o["api_key"],
			baseURL: str(o["base_url"]), model: str(o["model"])})
	}
	return out
}

func zeroclawMap(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

func (p *zeroclawPlanner) providers() {
	for _, e := range p.entries() {
		fam := e.family
		baseURL := e.baseURL
		if strings.HasPrefix(fam, "custom:") {
			baseURL = firstNonEmpty(baseURL, strings.TrimPrefix(fam, "custom:"))
			fam = "custom"
		}
		key, keyOK := p.secret(e.key)
		var pp ProviderPayload
		v, known := Vendor(fam)
		if !known && baseURL != "" {
			v, known = VendorByBaseURL(baseURL)
		}
		switch {
		case known && v.OAuth:
			p.add(OAuthProviderItem(Slug(e.ref), v.Label))
			continue
		case known:
			pp = ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: firstNonEmpty(baseURL, v.BaseURL)}
			if e.alias != "" && e.alias != "default" {
				pp.ID = Slug(v.ID + "-" + e.alias)
				pp.Label = v.Label + " (" + e.alias + ")"
			}
		case baseURL != "":
			name := e.alias
			if name == "" || name == "default" {
				name = hostLabel(baseURL)
			}
			pp = ProviderPayload{ID: CustomProviderID(p.taken, name), Label: name, Kind: "openai-compatible", BaseURL: baseURL}
		default:
			p.add(UnsupportedItem(CatProvider, Slug(e.ref), e.ref, "unknown provider \""+e.ref+"\" with no endpoint"))
			continue
		}
		if p.planned[pp.ID] {
			p.byRef[e.ref] = pp.ID
			p.modelFor[e.ref] = e.model
			continue
		}
		pp.APIKey = key
		if e.model != "" {
			pp.Models = []string{e.model}
		}
		detail := "from config.toml"
		if str(e.key) != "" && zeroclawEncrypted(str(e.key)) {
			detail = "encrypted key from config.toml"
		}
		it := ProviderItem(p.env, pp, detail)
		if !keyOK && p.decrypt && it.Status == StatusNeedsInput {
			it.Reason = zeroclawKeyReason(str(e.key), p.keyErr)
		}
		p.planned[pp.ID] = true
		p.byRef[e.ref] = pp.ID
		p.modelFor[e.ref] = e.model
		p.add(it)
	}
}

func zeroclawKeyReason(v string, keyErr error) string {
	switch {
	case strings.HasPrefix(v, "op://"):
		return "the key is a 1Password reference; enter it to import this provider"
	case keyErr != nil:
		return "the key is encrypted and .secret_key could not be read; enter it to import this provider"
	}
	return "the key could not be decrypted; enter it to import this provider"
}

func (p *zeroclawPlanner) taken(id string) bool {
	return p.planned[id] || p.env != nil && p.env.ProviderExists(id)
}

func (p *zeroclawPlanner) model() {
	var ref, model string
	switch {
	case p.version >= 3:
		ref = str(zeroclawMap(p.cfg, "agents", "default", "model_provider"))
		if ref == "" {
			agents, _ := p.cfg["agents"].(map[string]any)
			for _, a := range sortedKeys(agents) {
				if r := str(zeroclawMap(agents, a, "model_provider")); r != "" {
					ref = r
					break
				}
			}
		}
		model = p.modelFor[ref]
	case p.version == 2:
		ref = str(zeroclawMap(p.cfg, "providers", "fallback"))
		model = p.modelFor[ref]
	case p.version == 1:
		ref = firstNonEmpty(str(p.cfg["default_provider"]), str(p.cfg["model_provider"]), "openrouter")
		model = firstNonEmpty(str(p.cfg["default_model"]), str(p.cfg["model"]))
	}
	id := p.byRef[ref]
	if id == "" || model == "" {
		return
	}
	m := ModelPayload{Provider: id, Model: model}
	for _, e := range p.entries() {
		if e.ref != ref {
			continue
		}
		for _, fb := range e.fbRefs {
			if fid, fm := p.byRef[fb], p.modelFor[fb]; fid != "" && fm != "" {
				m.Fallback = append(m.Fallback, fid+"/"+fm)
			}
		}
	}
	if p.version == 1 {
		for _, fb := range strList(zeroclawMap(p.cfg, "reliability", "fallback_providers")) {
			if fid, fm := p.byRef[fb], p.modelFor[fb]; fid != "" && fm != "" && fid != id {
				m.Fallback = append(m.Fallback, fid+"/"+fm)
			}
		}
	}
	p.add(ModelItem(p.env, m))
}

// ---- workspace: persona, memory, skills ------------------------------------------

// workspace is the default agent's workspace (V3 layout first).
func (p *zeroclawPlanner) workspace() string {
	if d := str(zeroclawMap(p.cfg, "agents", "default", "workspace", "path")); d != "" && IsDir(ExpandHome(d)) {
		return ExpandHome(d)
	}
	for _, d := range []string{filepath.Join(p.det.Root, "agents", "default", "workspace"), filepath.Join(p.det.Root, "workspace")} {
		if IsDir(d) {
			return d
		}
	}
	return ""
}

func zeroclawSoul(ws string) (string, string) {
	var parts, from []string
	for _, f := range []string{"SOUL.md", "IDENTITY.md"} {
		if t := ReadText(filepath.Join(ws, f)); t != "" {
			parts = append(parts, t)
			from = append(from, f)
		}
	}
	return strings.Join(parts, "\n\n"), strings.Join(from, " + ")
}

func (p *zeroclawPlanner) persona() {
	ws := p.workspace()
	if ws == "" {
		return
	}
	if soul, from := zeroclawSoul(ws); soul != "" {
		if it, ok := TextItem(p.env, CatSoul, soul, from); ok {
			p.add(it)
		}
	}
	if it, ok := TextItem(p.env, CatAgentsMD, ReadText(filepath.Join(ws, "AGENTS.md")), "AGENTS.md"); ok {
		p.add(it)
	}
	if it, ok := TextItem(p.env, CatUserMD, ReadText(filepath.Join(ws, "USER.md")), "USER.md"); ok {
		p.add(it)
	}
}

// zeroclawKeyedLine matches markdown memory lines "**key**: content".
var zeroclawKeyedLine = regexp.MustCompile(`^\*\*([^*]+)\*\*:\s*`)

var zeroclawDailyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.md$`)

func (p *zeroclawPlanner) memory() {
	var entries []MemoryEntry
	ws := p.workspace()
	if ws != "" {
		for _, e := range SplitMarkdownEntries(ReadText(filepath.Join(ws, "MEMORY.md"))) {
			// "- **key**: content": the key is a short label; the content is
			// the fact (and matches the same entry in brain.db for dedupe).
			e.Content = zeroclawKeyedLine.ReplaceAllString(e.Content, "")
			if e.Heading == "Long-Term Memory" {
				e.Heading = ""
			}
			entries = append(entries, e)
		}
	}
	for _, db := range []string{
		filepath.Join(p.det.Root, "data", "memory", "brain.db"),
		filepath.Join(p.det.Root, "workspace", "memory", "brain.db"),
	} {
		if IsFile(db) {
			entries = append(entries, zeroclawBrainEntries(db, &p.warn)...)
			break
		}
	}
	p.add(MemoryItems(entries, "global", "import:zeroclaw")...)
	if ws == "" {
		return
	}
	dir := filepath.Join(ws, "memory")
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if f.IsDir() || !zeroclawDailyRE.MatchString(f.Name()) {
			continue
		}
		if it, ok := KnowledgeItem(p.env, "memory/"+f.Name(), ReadText(filepath.Join(dir, f.Name()))); ok {
			p.add(it)
		}
	}
}

// zeroclawOpenDB opens a SQLite file read-only.
func zeroclawOpenDB(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
}

// zeroclawColumns lists a table's columns (empty when the table is missing).
func zeroclawColumns(db *sql.DB, table string) map[string]bool {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			cols[n] = true
		}
	}
	return cols
}

// zeroclawBrainEntries reads non-superseded rows of brain.db's memories
// table, skipping the "daily" category (daily logs, not facts). In V3 only
// the default agent's rows are read.
func zeroclawBrainEntries(path string, warn *[]string) []MemoryEntry {
	db, err := zeroclawOpenDB(path)
	if err != nil {
		return nil
	}
	defer db.Close()
	cols := zeroclawColumns(db, "memories")
	if !cols["content"] {
		return nil
	}
	q := "SELECT COALESCE(key,''), content, " + zeroclawColOr(cols, "category", "''") + " FROM memories WHERE 1=1"
	if cols["superseded_by"] {
		q += " AND (superseded_by IS NULL OR superseded_by = '')"
	}
	if cols["agent_id"] && len(zeroclawColumns(db, "agents")) > 0 {
		q += " AND agent_id IN (SELECT id FROM agents WHERE alias = 'default')"
	}
	q += " ORDER BY " + zeroclawColOr(cols, "created_at", "rowid") + ", rowid"
	rows, err := db.Query(q)
	if err != nil {
		*warn = append(*warn, "brain.db could not be read: "+err.Error())
		return nil
	}
	defer rows.Close()
	var out []MemoryEntry
	for rows.Next() {
		var key, content, cat string
		if rows.Scan(&key, &content, &cat) != nil || strings.TrimSpace(content) == "" || cat == "daily" {
			continue
		}
		e := MemoryEntry{Content: strings.TrimSpace(content)}
		if cat != "" && cat != "core" {
			e.Heading = cat
		}
		out = append(out, e)
	}
	return out
}

func zeroclawColOr(cols map[string]bool, col, def string) string {
	if cols[col] {
		return "COALESCE(" + col + ", " + def + ")"
	}
	return def
}

func (p *zeroclawPlanner) skills() {
	seen := map[string]bool{}
	var roots []string
	if ws := p.workspace(); ws != "" {
		roots = append(roots, filepath.Join(ws, "skills"))
	}
	roots = append(roots, filepath.Join(p.det.Root, "workspace", "skills"))
	for _, root := range roots {
		for _, sd := range ScanSkills(root, 1) {
			if !seen[sd.Name] {
				seen[sd.Name] = true
				p.add(SkillItem(p.env, sd))
			}
		}
		p.tomlSkills(root, seen)
	}
	shared := filepath.Join(p.det.Root, "shared", "skills")
	for _, sd := range ScanSkills(shared, 2) {
		if !seen[sd.Name] {
			seen[sd.Name] = true
			p.add(SkillItem(p.env, sd))
		}
	}
	bundles, _ := os.ReadDir(shared)
	for _, b := range bundles {
		if b.IsDir() {
			p.tomlSkills(filepath.Join(shared, b.Name()), seen)
		}
	}
}

// tomlSkills lists SKILL.toml/manifest.toml-only skills as unsupported.
func (p *zeroclawPlanner) tomlSkills(root string, seen map[string]bool) {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() || IsFile(filepath.Join(dir, "SKILL.md")) {
			continue
		}
		if !IsFile(filepath.Join(dir, "SKILL.toml")) && !IsFile(filepath.Join(dir, "manifest.toml")) {
			continue
		}
		name := Slug(e.Name())
		if seen[name] {
			continue
		}
		seen[name] = true
		p.add(UnsupportedItem(CatSkill, name, name, "SKILL.toml skills (tool manifests) are not supported; only SKILL.md skills are"))
	}
}

// ---- MCP, cron --------------------------------------------------------------------

func (p *zeroclawPlanner) secretMap(v any) (map[string]string, bool) {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil, true
	}
	out := map[string]string{}
	ok := true
	for _, k := range sortedKeys(m) {
		s, good := p.secret(m[k])
		if !good {
			ok = false
		}
		out[k] = s
	}
	return out, ok
}

func (p *zeroclawPlanner) mcp() {
	sec, _ := p.cfg["mcp"].(map[string]any)
	if sec == nil {
		sec, _ = p.cfg["mcpServers"].(map[string]any)
	}
	list, _ := sec["servers"].([]any)
	for _, raw := range list {
		s, _ := raw.(map[string]any)
		name := Slug(str(s["name"]))
		if name == "" {
			continue
		}
		env, ok1 := p.secretMap(s["env"])
		hdr, ok2 := p.secretMap(s["headers"])
		it := MCPItem(p.env, MCPPayload{Name: name, Transport: str(s["transport"]), Command: str(s["command"]),
			Args: strList(s["args"]), Env: env, URL: str(s["url"]), Headers: hdr})
		if (!ok1 || !ok2) && p.decrypt && it.Status == StatusReady {
			it.Detail = strings.TrimSpace(it.Detail + " · some encrypted values could not be read and are left empty")
		}
		p.add(it)
	}
}

// zeroclawSchedule converts a ZeroClaw Schedule ({kind, expr, tz, every_ms,
// at}) to an Antares schedule; reason is set when it cannot be.
func zeroclawSchedule(s map[string]any) (sched, tz, reason string) {
	switch str(s["kind"]) {
	case "cron":
		return str(s["expr"]), str(s["tz"]), ""
	case "every":
		ms, _ := strconv.ParseInt(str(s["every_ms"]), 10, 64)
		if ms <= 0 {
			return "", "", "interval has no length"
		}
		return EverySchedule(time.Duration(ms) * time.Millisecond), "", ""
	case "at":
		return "", "", "one-shot schedule (runs once at " + str(s["at"]) + ")"
	}
	return "", "", "unknown schedule kind \"" + str(s["kind"]) + "\""
}

func (p *zeroclawPlanner) cronJob(key, name string, job, sched map[string]any) {
	p.add(zeroclawCronJobItem(key, name, job, sched))
}

// zeroclawCronJobItem converts one ZeroClaw/OpenHuman job (TOML table or
// cron_jobs row) to a cron item.
func zeroclawCronJobItem(key, name string, job, sched map[string]any) Item {
	if name == "" {
		name = key
	}
	if sched == nil {
		// V2 jobs carried a bare cron expression.
		if e := firstStr(job, "expression", "expr", "cron"); e != "" {
			sched = map[string]any{"kind": "cron", "expr": e, "tz": str(job["tz"])}
		}
	}
	if sched == nil {
		return UnsupportedItem(CatCron, Slug(key), name, "the job has no schedule")
	}
	expr, tz, reason := zeroclawSchedule(sched)
	if reason != "" {
		return UnsupportedItem(CatCron, Slug(key), name, reason)
	}
	switch jt := str(job["job_type"]); {
	case jt == "shell" || jt == "" && str(job["prompt"]) == "" && str(job["command"]) != "":
		return UnsupportedItem(CatCron, Slug(key), name, "shell-command jobs are not migrated (Antares schedules run prompts)")
	case jt != "" && jt != "agent":
		return UnsupportedItem(CatCron, Slug(key), name, jt+" jobs are not migrated (Antares schedules run prompts)")
	}
	enabled := true
	if b, ok := job["enabled"].(bool); ok {
		enabled = b
	}
	return CronItem(Slug(key), CronPayload{Name: name, Schedule: expr, Prompt: str(job["prompt"]), Timezone: tz}, enabled)
}

func (p *zeroclawPlanner) cron() {
	seen := map[string]bool{}
	sec, _ := p.cfg["cron"].(map[string]any)
	if jobs, ok := sec["jobs"].([]any); ok { // V2
		for i, raw := range jobs {
			j, _ := raw.(map[string]any)
			key := firstNonEmpty(str(j["id"]), str(j["name"]), "job-"+strconv.Itoa(i+1))
			seen[Slug(key)] = true
			sched, _ := j["schedule"].(map[string]any)
			p.cronJob(key, str(j["name"]), j, sched)
		}
	}
	for _, alias := range sortedKeys(sec) { // V3 [cron.<alias>]
		j, ok := sec[alias].(map[string]any)
		if !ok || alias == "jobs" {
			continue
		}
		seen[Slug(alias)] = true
		if n := str(j["name"]); n != "" {
			seen[Slug(n)] = true
		}
		sched, _ := j["schedule"].(map[string]any)
		p.cronJob(alias, str(j["name"]), j, sched)
	}
	for _, path := range []string{
		filepath.Join(p.det.Root, "data", "cron", "jobs.db"),
		filepath.Join(p.det.Root, "agents", "default", "workspace", "cron", "jobs.db"),
		filepath.Join(p.det.Root, "workspace", "cron", "jobs.db"),
	} {
		if IsFile(path) {
			p.cronDB(path, seen)
			break
		}
	}
}

// cronDB reads runtime-created jobs (cron_jobs table, shared by ZeroClaw and
// OpenHuman's tinyflows store).
func (p *zeroclawPlanner) cronDB(path string, seen map[string]bool) {
	zeroclawCronRows(path, seen, &p.warn, p.cronJob)
}

func zeroclawCronRows(path string, seen map[string]bool, warn *[]string, emit func(key, name string, job, sched map[string]any)) {
	db, err := zeroclawOpenDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	cols := zeroclawColumns(db, "cron_jobs")
	if !cols["id"] || !cols["schedule"] {
		return
	}
	q := "SELECT id, " + zeroclawColOr(cols, "name", "''") + ", " + zeroclawColOr(cols, "job_type", "'shell'") + ", " +
		zeroclawColOr(cols, "prompt", "''") + ", " + zeroclawColOr(cols, "command", "''") + ", " +
		zeroclawColOr(cols, "expression", "''") + ", COALESCE(schedule,''), " + zeroclawColOr(cols, "enabled", "1") +
		" FROM cron_jobs ORDER BY " + zeroclawColOr(cols, "created_at", "''") + ", id"
	rows, err := db.Query(q)
	if err != nil {
		*warn = append(*warn, filepath.Base(path)+" could not be read: "+err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, jt, prompt, command, expr, schedJSON string
		var enabled int
		if rows.Scan(&id, &name, &jt, &prompt, &command, &expr, &schedJSON, &enabled) != nil {
			continue
		}
		if seen[Slug(id)] || name != "" && seen[Slug(name)] {
			continue
		}
		seen[Slug(id)] = true
		var sched map[string]any
		if json.Unmarshal([]byte(schedJSON), &sched) != nil || sched == nil {
			if expr != "" {
				sched = map[string]any{"kind": "cron", "expr": expr}
			}
		}
		emit(id, name, map[string]any{"job_type": jt, "prompt": prompt, "command": command, "enabled": enabled != 0}, sched)
	}
}

// ---- channels ---------------------------------------------------------------------

// zeroclawChannelBlocks flattens channel sections of every schema into
// (type, alias, block). V1 [channels_config.<t>], V2 [channels.<t>], V3
// [channels.<t>.<alias>].
func zeroclawChannelBlocks(cfg map[string]any) []zeroclawChannelBlock {
	sec, _ := cfg["channels"].(map[string]any)
	if sec == nil {
		sec, _ = cfg["channels_config"].(map[string]any)
	}
	var out []zeroclawChannelBlock
	for _, typ := range sortedKeys(sec) {
		v, ok := sec[typ].(map[string]any)
		if !ok {
			continue // scalar settings: cli, session_backend, …
		}
		aliased := len(v) > 0
		for _, k := range sortedKeys(v) {
			if _, isMap := v[k].(map[string]any); !isMap {
				aliased = false
				break
			}
		}
		if aliased {
			for _, alias := range sortedKeys(v) {
				out = append(out, zeroclawChannelBlock{typ: typ, alias: alias, block: v[alias].(map[string]any)})
			}
			continue
		}
		out = append(out, zeroclawChannelBlock{typ: typ, alias: "default", block: v})
	}
	return out
}

type zeroclawChannelBlock struct {
	typ, alias string
	block      map[string]any
}

// zeroclawChannelFields maps a ZeroClaw/OpenHuman channel block to an Antares
// gateway platform and fields. secret opens encrypted values. ok=false means
// the type has no Antares gateway (reason says why); missing names a
// required field that is empty or could not be read.
func zeroclawChannelFields(typ string, b map[string]any, peers []string, secret func(any) (string, bool)) (platform string, fields map[string]any, missing, reason string) {
	get := func(k string) string {
		s, ok := secret(b[k])
		if !ok {
			return ""
		}
		return s
	}
	list := func(keys ...string) []string {
		var out []string
		for _, k := range keys {
			if l := strList(b[k]); len(l) > 0 {
				out = append(out, l...)
			} else if s := str(b[k]); s != "" {
				out = append(out, s)
			}
		}
		var keep []string
		for _, s := range out {
			if s != "*" && s != "" && !contains(keep, s) {
				keep = append(keep, s)
			}
		}
		return keep
	}
	need := func(fields map[string]any, keys ...string) string {
		for _, k := range keys {
			if s, _ := fields[k].(string); s == "" {
				return k
			}
		}
		return ""
	}
	setList := func(f map[string]any, k string, v []string) {
		if k == "allowed_users" {
			for _, p := range peers {
				if p != "*" && p != "" && !contains(v, p) {
					v = append(v, p)
				}
			}
		}
		if len(v) > 0 {
			f[k] = v
		}
	}
	switch typ {
	case "telegram":
		f := map[string]any{"bot_token": get("bot_token")}
		setList(f, "allowed_users", list("allowed_users"))
		setList(f, "allowed_chats", list("chat_id"))
		return "telegram", f, need(f, "bot_token"), ""
	case "discord":
		f := map[string]any{"bot_token": get("bot_token")}
		setList(f, "allowed_users", list("allowed_users"))
		setList(f, "allowed_guilds", list("guild_ids", "guild_id"))
		return "discord", f, need(f, "bot_token"), ""
	case "slack":
		f := map[string]any{"bot_token": get("bot_token"), "app_token": get("app_token")}
		setList(f, "allowed_users", list("allowed_users"))
		setList(f, "allowed_channels", list("channel_ids", "channel_id"))
		return "slack", f, need(f, "bot_token", "app_token"), ""
	case "matrix":
		f := map[string]any{"homeserver": str(b["homeserver"]), "access_token": get("access_token"), "user_id": str(b["user_id"])}
		setList(f, "allowed_users", list("allowed_users"))
		setList(f, "allowed_rooms", list("allowed_rooms", "room_id"))
		return "matrix", f, need(f, "access_token"), ""
	case "signal":
		f := map[string]any{"api_url": str(b["http_url"]), "number": str(b["account"])}
		setList(f, "allowed_users", list("allowed_from"))
		if f["api_url"] == "" {
			return "signal", f, "api_url", ""
		}
		return "signal", f, "", ""
	case "whatsapp":
		if str(b["phone_number_id"]) == "" && (str(b["session_path"]) != "" || str(b["pair_phone"]) != "" || str(b["ws_url"]) != "" || str(b["access_token"]) == "") {
			return "", nil, "", "WhatsApp Web (a linked-device session) cannot be moved; Antares supports the WhatsApp Cloud API only"
		}
		f := map[string]any{"token": get("access_token"), "phone_number_id": str(b["phone_number_id"]), "verify_token": get("verify_token")}
		setList(f, "allowed_users", list("allowed_numbers"))
		return "whatsapp", f, need(f, "token"), ""
	case "lark", "feishu":
		if typ == "lark" {
			if v, _ := b["use_feishu"].(bool); !v {
				return "", nil, "", "Antares' Feishu gateway uses open.feishu.cn; Lark (larksuite.com) apps are not supported"
			}
		}
		f := map[string]any{"app_id": str(b["app_id"]), "app_secret": get("app_secret"), "verify_token": get("verification_token")}
		setList(f, "allowed_users", list("allowed_users"))
		return "feishu", f, need(f, "app_id", "app_secret"), ""
	}
	return "", nil, "", "Antares has no " + PlatformLabel(typ) + " gateway"
}

func (p *zeroclawPlanner) channels() {
	used := map[string]string{} // platform → alias already imported
	for _, cb := range zeroclawChannelBlocks(p.cfg) {
		title := PlatformLabel(cb.typ)
		key := cb.typ
		if cb.alias != "default" {
			title += " (" + cb.alias + ")"
			key += "-" + Slug(cb.alias)
		}
		peers := strList(zeroclawMap(p.cfg, "peer_groups", cb.typ+"_"+cb.alias, "external_peers"))
		platform, fields, missing, reason := zeroclawChannelFields(cb.typ, cb.block, peers, p.secret)
		if reason != "" {
			p.add(UnsupportedItem(CatChannel, key, title, reason))
			continue
		}
		if prev, dup := used[platform]; dup {
			p.add(UnsupportedItem(CatChannel, key, title, "Antares has one "+PlatformLabel(platform)+" bot; "+prev+" is already being imported"))
			continue
		}
		used[platform] = title
		if !p.decrypt && missing != "" {
			// Detect does not decrypt; an encrypted token still counts.
			missing = ""
		}
		it := ChannelItem(p.env, platform, title, fields, missing)
		it.ID = ItemID(CatChannel, key)
		if b, ok := cb.block["enabled"].(bool); ok && !b {
			it.Detail = "disabled in ZeroClaw"
		}
		p.add(it)
	}
}

// ---- roles -------------------------------------------------------------------------

func (p *zeroclawPlanner) roles() {
	agents, _ := p.cfg["agents"].(map[string]any)
	for _, name := range sortedKeys(agents) {
		if name == "default" {
			continue
		}
		a, _ := agents[name].(map[string]any)
		if a == nil {
			continue
		}
		ws := filepath.Join(p.det.Root, "agents", name, "workspace")
		if d := str(zeroclawMap(a, "workspace", "path")); d != "" {
			ws = ExpandHome(d)
		}
		soul, _ := zeroclawSoul(ws)
		prompt := strings.TrimSpace(strings.Join([]string{soul, ReadText(filepath.Join(ws, "AGENTS.md"))}, "\n\n"))
		model := ""
		if ref := str(a["model_provider"]); ref != "" {
			if id, m := p.byRef[ref], p.modelFor[ref]; id != "" && m != "" {
				model = id + "/" + m
			}
		} else if m := str(a["model"]); m != "" {
			model = m
		}
		summary := "ZeroClaw agent"
		if d := strList(a["channels"]); len(d) > 0 {
			summary += " on " + strings.Join(d, ", ")
		}
		p.add(RoleItem(p.env, RolePayload{Name: name, Title: name, Summary: summary, Prompt: prompt, Model: model}))
	}
}

// ---- minimal TOML reader -------------------------------------------------
//
// ZeroClaw and OpenHuman keep their settings in config.toml and the module
// has no TOML dependency, so this is a small reader for the subset those
// files use: tables, arrays of tables, dotted and quoted keys, basic/literal
// (multi-line) strings, integers, floats, booleans, date-times (kept as
// strings), arrays and inline tables. It is read-only and lenient.

type zeroclawTOMLParser struct {
	s    string
	i    int
	line int
}

func zeroclawParseTOML(src string) (map[string]any, error) {
	p := &zeroclawTOMLParser{s: src, line: 1}
	root := map[string]any{}
	cur := root
	for {
		p.skipWSNL()
		if p.eof() {
			return root, nil
		}
		switch p.peek() {
		case '[':
			arr := strings.HasPrefix(p.s[p.i:], "[[")
			if arr {
				p.i += 2
			} else {
				p.i++
			}
			p.skipWS()
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipWS()
			closer := "]"
			if arr {
				closer = "]]"
			}
			if !strings.HasPrefix(p.s[p.i:], closer) {
				return nil, p.errf("expected %s", closer)
			}
			p.i += len(closer)
			t, err := zeroclawTOMLTable(root, keys, arr)
			if err != nil {
				return nil, p.errf("%v", err)
			}
			cur = t
		default:
			if err := p.keyValue(cur); err != nil {
				return nil, err
			}
		}
		p.skipWS()
		if !p.eof() && p.peek() != '\n' && p.peek() != '\r' {
			return nil, p.errf("unexpected %q", p.peek())
		}
	}
}

// zeroclawTOMLTable walks/creates the table at keys; for an array of tables
// it appends a new element and returns it.
func zeroclawTOMLTable(root map[string]any, keys []string, arr bool) (map[string]any, error) {
	t := root
	for n, k := range keys {
		last := n == len(keys)-1
		v, ok := t[k]
		if last && arr {
			list, _ := v.([]any)
			if ok && list == nil {
				return nil, fmt.Errorf("%s is not an array of tables", k)
			}
			m := map[string]any{}
			t[k] = append(list, m)
			return m, nil
		}
		if !ok {
			m := map[string]any{}
			t[k] = m
			t = m
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			t = x
		case []any:
			if len(x) == 0 {
				return nil, fmt.Errorf("%s is an empty array", k)
			}
			m, ok := x[len(x)-1].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not a table", k)
			}
			t = m
		default:
			return nil, fmt.Errorf("%s is not a table", k)
		}
	}
	return t, nil
}

func (p *zeroclawTOMLParser) eof() bool  { return p.i >= len(p.s) }
func (p *zeroclawTOMLParser) peek() byte { return p.s[p.i] }

func (p *zeroclawTOMLParser) errf(f string, a ...any) error {
	return fmt.Errorf("toml line %d: %s", p.line, fmt.Sprintf(f, a...))
}

// skipWS skips spaces, tabs and a trailing comment (not newlines).
func (p *zeroclawTOMLParser) skipWS() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t':
			p.i++
		case '#':
			for !p.eof() && p.peek() != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *zeroclawTOMLParser) skipWSNL() {
	for {
		p.skipWS()
		if p.eof() {
			return
		}
		switch p.peek() {
		case '\n':
			p.line++
			p.i++
		case '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *zeroclawTOMLParser) key() ([]string, error) {
	var keys []string
	for {
		p.skipWS()
		if p.eof() {
			return nil, p.errf("unexpected end in key")
		}
		var k string
		switch p.peek() {
		case '"', '\'':
			v, err := p.str()
			if err != nil {
				return nil, err
			}
			k = v
		default:
			start := p.i
			for !p.eof() {
				c := p.peek()
				if c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
					p.i++
					continue
				}
				break
			}
			if p.i == start {
				return nil, p.errf("bad key at %q", p.peek())
			}
			k = p.s[start:p.i]
		}
		keys = append(keys, k)
		p.skipWS()
		if !p.eof() && p.peek() == '.' {
			p.i++
			continue
		}
		return keys, nil
	}
}

func (p *zeroclawTOMLParser) keyValue(t map[string]any) error {
	keys, err := p.key()
	if err != nil {
		return err
	}
	p.skipWS()
	if p.eof() || p.peek() != '=' {
		return p.errf("expected = after key %s", strings.Join(keys, "."))
	}
	p.i++
	p.skipWS()
	v, err := p.value()
	if err != nil {
		return err
	}
	for _, k := range keys[:len(keys)-1] {
		m, ok := t[k].(map[string]any)
		if !ok {
			m = map[string]any{}
			t[k] = m
		}
		t = m
	}
	t[keys[len(keys)-1]] = v
	return nil
}

func (p *zeroclawTOMLParser) value() (any, error) {
	if p.eof() {
		return nil, p.errf("missing value")
	}
	switch c := p.peek(); c {
	case '"', '\'':
		return p.str()
	case '[':
		p.i++
		var out []any
		for {
			p.skipWSNL()
			if p.eof() {
				return nil, p.errf("unterminated array")
			}
			if p.peek() == ']' {
				p.i++
				return out, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			p.skipWSNL()
			if !p.eof() && p.peek() == ',' {
				p.i++
			}
		}
	case '{':
		p.i++
		m := map[string]any{}
		for {
			p.skipWS()
			if p.eof() {
				return nil, p.errf("unterminated inline table")
			}
			if p.peek() == '}' {
				p.i++
				return m, nil
			}
			if err := p.keyValue(m); err != nil {
				return nil, err
			}
			p.skipWS()
			if !p.eof() && p.peek() == ',' {
				p.i++
			}
		}
	}
	start := p.i
	for !p.eof() {
		c := p.peek()
		if c == ',' || c == ']' || c == '}' || c == '\n' || c == '\r' || c == '#' {
			break
		}
		p.i++
	}
	raw := strings.TrimSpace(p.s[start:p.i])
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		return nil, p.errf("missing value")
	}
	num := strings.ReplaceAll(raw, "_", "")
	if n, err := strconv.ParseInt(num, 0, 64); err == nil {
		return int(n), nil
	}
	if f, err := strconv.ParseFloat(num, 64); err == nil {
		return f, nil
	}
	return raw, nil // date-time or something exotic: keep the text
}

func (p *zeroclawTOMLParser) str() (string, error) {
	q := p.peek()
	multi := strings.HasPrefix(p.s[p.i:], strings.Repeat(string(q), 3))
	if multi {
		p.i += 3
		if strings.HasPrefix(p.s[p.i:], "\r\n") {
			p.i += 2
		} else if !p.eof() && p.peek() == '\n' {
			p.i++
		}
	} else {
		p.i++
	}
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.errf("unterminated string")
		}
		c := p.peek()
		if multi && strings.HasPrefix(p.s[p.i:], strings.Repeat(string(q), 3)) {
			p.i += 3
			// up to two extra quotes belong to the content
			for k := 0; k < 2 && !p.eof() && p.peek() == q; k++ {
				b.WriteByte(q)
				p.i++
			}
			return b.String(), nil
		}
		if !multi && c == q {
			p.i++
			return b.String(), nil
		}
		if c == '\n' {
			if !multi {
				return "", p.errf("newline in string")
			}
			p.line++
		}
		if c == '\\' && q == '"' {
			p.i++
			if p.eof() {
				return "", p.errf("bad escape")
			}
			e := p.peek()
			p.i++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'e':
				b.WriteByte(0x1b)
			case '"', '\\':
				b.WriteByte(e)
			case 'u', 'U':
				n := 4
				if e == 'U' {
					n = 8
				}
				if p.i+n > len(p.s) {
					return "", p.errf("bad unicode escape")
				}
				r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
				if err != nil {
					return "", p.errf("bad unicode escape")
				}
				b.WriteRune(rune(r))
				p.i += n
			case '\n', ' ', '\t', '\r':
				if !multi {
					return "", p.errf("bad escape")
				}
				if e == '\n' {
					p.line++
				}
				// line-ending backslash: trim whitespace up to next content
				for !p.eof() && strings.IndexByte(" \t\r\n", p.peek()) >= 0 {
					if p.peek() == '\n' {
						p.line++
					}
					p.i++
				}
			default:
				return "", p.errf("bad escape \\%c", e)
			}
			continue
		}
		b.WriteByte(c)
		p.i++
	}
}
