package migrate

// OpenHuman (github.com/tinyhumansai/openhuman). Formats read from the
// project's source on main (2026-10-03); OH below is
// https://github.com/tinyhumansai/openhuman/blob/main and core/ is
// OH/crates/openhuman-core/src/. Memory, channels, MCP and cron live in
// submodules: TM = github.com/tinyhumansai/tinymemory, TC = …/tinychannels,
// TMCP = …/tinymcp, TF = …/tinyflows.
//
//   - Root ~/.openhuman (~/.openhuman-staging when OPENHUMAN_APP_ENV=staging),
//     core/config/schema/load/dirs.rs. Per-user config dirs
//     users/<id>/{config.toml, auth-profiles.json, workspace/}; the active one
//     from $OPENHUMAN_WORKSPACE, active_user.toml (user_id), then
//     active_workspace.toml (config_dir), else users/local
//     (core/config/schema/load_user_state.rs).
//   - config.toml (core/config/schema/types/config.rs): api_key, inference_url,
//     default_model, primary_cloud, chat_provider ("<slug>:<model>",
//     core/inference/provider/factory.rs), [[cloud_providers]] {id, slug,
//     label, endpoint, auth_style}, [mcp_client] servers (TMCP
//     crates/tinymcp-bus/src/config/types.rs: name, endpoint|command, args,
//     env, enabled, auth{kind,…}), [channels_config.<name>] (TC
//     crates/tinychannels-bus/src/config.rs; same keys as ZeroClaw V1).
//   - Secrets: config values "enc2:"/"enc:" use ZeroClaw's SecretStore scheme
//     (core/config/schema/load/secrets.rs); the master key is keyring entry
//     "<U>:secretstore.master_key" (U = config-dir basename), legacy
//     <configdir>/.secret_key. Keyring backends (core/security/keyring/*):
//     file <KD>/dev-keychain.json {"<U>:<key>": value}; encrypted_file
//     <KD>/secrets.enc (ChaCha20-Poly1305 nonce||ct||tag over the same JSON,
//     key hex in the OS keychain service "openhuman", account
//     "app:master_key"); os = OS keychain service "openhuman", account
//     "<U>:<key>". KD = $OPENHUMAN_WORKSPACE or the root.
//   - Provider keys: <configdir>/auth-profiles.json
//     (core/security/credentials/profiles.rs) profiles[<provider>:<name>]
//     {provider "provider:<slug>", kind token|o-auth, token, access_token};
//     with a keyring the secrets are at "<U>:auth:<profile id>" as JSON.
//   - Workspace files (core/agent/prompts/render_helpers/workspace_files.rs):
//     SOUL.md, IDENTITY.md, ROLE.md, STYLE.md (seeded defaults; skipped when
//     the ".<name>.builtin-hash" sidecar holds the file's SHA-256), AGENTS.md,
//     PROFILE.md (retired user profile, core/config/migrations/
//     phase_out_profile_md.rs) → USER.md, MEMORY.md → memories.
//   - Memory vault (TM): <ws>/memory_tree/content/ ([memory_tree] content_dir)
//     → knowledge; content/episodic/ is the chat archive and is skipped.
//   - Skills (core/skills/ops_discover/scan.rs): ~/.openhuman/skills and the
//     legacy <ws>/skills (SKILL.md folders).
//   - Cron (core/cron/store.rs, TF crates/tinyflows-sqlite/src/schedule/
//     schema.rs): <ws>/cron/jobs.db, table cron_jobs as in ZeroClaw.
//   - Running: the desktop app (bundle com.openhuman.app, binary OpenHuman)
//     or openhuman-core; no pid file.
//
// UNVERIFIED / NOT IMPORTED: which keyring backend shipped desktop builds use
// (all three are tried); the builtin-hash algorithm (SHA-256 assumed; on a
// mismatch the file is imported); <ws>/memory/memory.db (tinymemory
// memory_docs) is not imported because what its rows mean was not verified —
// the plan warns when it exists; OpenHuman account logins (app-session,
// openhumanjwt providers) and OAuth profiles are unsupported.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type openhumanSource struct{}

func init() { Register(openhumanSource{}) }

func (openhumanSource) ID() string   { return "openhuman" }
func (openhumanSource) Name() string { return "OpenHuman" }

func openhumanDefaultRoot() string {
	name := ".openhuman"
	for _, k := range []string{"OPENHUMAN_APP_ENV", "VITE_OPENHUMAN_APP_ENV"} {
		if strings.EqualFold(strings.TrimSpace(os.Getenv(k)), "staging") {
			name = ".openhuman-staging"
		}
	}
	return filepath.Join(HomeDir(), name)
}

// openhumanInstall is one config dir (a user) of an OpenHuman root.
type openhumanInstall struct {
	root      string // ~/.openhuman (skills, keyring files)
	configDir string // users/<id>
	workspace string
	user      string // U: config-dir basename, the keyring namespace
}

// openhumanResolve finds the active config dir and workspace under root,
// following load_user_state.rs. ws, when set, is $OPENHUMAN_WORKSPACE.
func openhumanResolve(root, ws string) openhumanInstall {
	in := openhumanInstall{root: root}
	switch {
	case ws != "":
		switch {
		case IsFile(filepath.Join(ws, "config.toml")):
			in.configDir, in.workspace = ws, filepath.Join(ws, "workspace")
		case filepath.Base(ws) == "workspace" && filepath.Base(filepath.Dir(ws)) == ".openhuman":
			in.configDir, in.workspace = filepath.Dir(ws), ws
		case filepath.Base(ws) == "workspace":
			in.configDir, in.workspace = filepath.Join(filepath.Dir(ws), ".openhuman"), ws
		default:
			in.configDir, in.workspace = ws, filepath.Join(ws, "workspace")
		}
	default:
		var userID, cfgDir string
		if m, err := zeroclawReadTOML(filepath.Join(root, "active_user.toml")); err == nil {
			userID = str(m["user_id"])
		}
		if m, err := zeroclawReadTOML(filepath.Join(root, "active_workspace.toml")); err == nil {
			cfgDir = str(m["config_dir"])
		}
		switch {
		case userID != "" && IsDir(filepath.Join(root, "users", userID)):
			in.configDir = filepath.Join(root, "users", userID)
		case cfgDir != "":
			in.configDir = cfgDir
			if !filepath.IsAbs(in.configDir) {
				in.configDir = filepath.Join(root, in.configDir)
			}
		case IsDir(filepath.Join(root, "users", "local")):
			in.configDir = filepath.Join(root, "users", "local")
		default:
			// Builds before per-user dirs kept everything at the root.
			in.configDir = root
		}
		in.workspace = filepath.Join(in.configDir, "workspace")
	}
	in.user = filepath.Base(in.configDir)
	return in
}

func openhumanLooksInstalled(in openhumanInstall) bool {
	return IsFile(filepath.Join(in.configDir, "config.toml")) || IsFile(filepath.Join(in.configDir, "auth-profiles.json")) ||
		IsDir(in.workspace) && (IsFile(filepath.Join(in.workspace, "SOUL.md")) || IsDir(filepath.Join(in.workspace, "memory_tree")) || IsDir(filepath.Join(in.workspace, "memory")))
}

// openhumanEnvWorkspace is $OPENHUMAN_WORKSPACE; tests replace it.
var openhumanEnvWorkspace = func() string { return ExpandHome(os.Getenv("OPENHUMAN_WORKSPACE")) }

func (s openhumanSource) Detect(ctx context.Context, root string) (Detection, error) {
	all, err := s.DetectAll(ctx, root)
	if err != nil || len(all) == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Detection{}, err
	}
	return all[0], nil
}

// DetectAll returns the active user first, then other users/<id> dirs as
// profiles named by id.
func (s openhumanSource) DetectAll(ctx context.Context, root string) ([]Detection, error) {
	ws := ""
	if root == "" {
		root = openhumanDefaultRoot()
		ws = openhumanEnvWorkspace()
	}
	root = ExpandHome(root)
	running := RunningCheck([]string{"OpenHuman.app/Contents/MacOS", "openhuman-core", "openhuman-tui"}, nil)
	var out []Detection
	active := openhumanResolve(root, ws)
	if openhumanLooksInstalled(active) {
		out = append(out, s.detectOne(active, "", running))
	}
	users, _ := os.ReadDir(filepath.Join(root, "users"))
	for _, u := range users {
		dir := filepath.Join(root, "users", u.Name())
		if !u.IsDir() || dir == active.configDir {
			continue
		}
		in := openhumanInstall{root: root, configDir: dir, workspace: filepath.Join(dir, "workspace"), user: u.Name()}
		if openhumanLooksInstalled(in) {
			out = append(out, s.detectOne(in, u.Name(), running))
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s openhumanSource) detectOne(in openhumanInstall, profile string, running bool) Detection {
	d := Detection{Source: s.ID(), Name: s.Name(), Root: in.configDir, Profile: profile, Running: running}
	p := openhumanPlan(in, d, nil, false)
	d.Summary = Summarize(p.items)
	return d
}

func (s openhumanSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	if !IsDir(det.Root) {
		return Plan{}, ErrNotFound
	}
	in := openhumanInstallFor(det.Root)
	p := openhumanPlan(in, det, env, true)
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

// openhumanInstallFor rebuilds the install from a detected config dir.
func openhumanInstallFor(configDir string) openhumanInstall {
	in := openhumanInstall{configDir: configDir, workspace: filepath.Join(configDir, "workspace"), user: filepath.Base(configDir)}
	in.root = configDir
	if filepath.Base(filepath.Dir(configDir)) == "users" {
		in.root = filepath.Dir(filepath.Dir(configDir))
	}
	if ws := openhumanEnvWorkspace(); ws != "" {
		if r := openhumanResolve(in.root, ws); r.configDir == configDir {
			in.workspace = r.workspace
		}
	}
	return in
}

type openhumanPlanner struct {
	in      openhumanInstall
	det     Detection
	env     Env
	secrets bool // false during Detect: no decryption, no keychain
	cfg     map[string]any
	items   []Item
	warn    []string

	keyring  map[string]string // logical keyring entries ("<U>:<key>")
	keyLoad  bool
	master   []byte
	planned  map[string]bool
	bySlug   map[string]string // cloud provider slug/id → Antares id
	modelFor map[string]string
}

func openhumanPlan(in openhumanInstall, det Detection, env Env, secrets bool) *openhumanPlanner {
	p := &openhumanPlanner{in: in, det: det, env: env, secrets: secrets, cfg: map[string]any{},
		planned: map[string]bool{}, bySlug: map[string]string{}, modelFor: map[string]string{}}
	if path := filepath.Join(in.configDir, "config.toml"); IsFile(path) {
		if m, err := zeroclawReadTOML(path); err != nil {
			p.warn = append(p.warn, "config.toml could not be read: "+err.Error())
		} else {
			p.cfg = m
		}
	}
	p.providers()
	p.model()
	p.persona()
	p.memory()
	p.skills()
	p.mcp()
	p.cron()
	p.channels()
	return p
}

func (p *openhumanPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// ---- secrets ---------------------------------------------------------------------

// openhumanKeychain is the OS keychain used for OpenHuman entries; tests may
// replace DefaultKeychain instead.
func openhumanKeychain() Keychain { return DefaultKeychain }

// keyringGet reads logical entry key (without the user prefix) from the
// file, encrypted-file and OS keychain backends, in that order.
func (p *openhumanPlanner) keyringGet(key string) (string, bool) {
	if !p.secrets {
		return "", false
	}
	full := p.in.user + ":" + key
	if !p.keyLoad {
		p.keyLoad = true
		p.keyring = map[string]string{}
		kd := p.in.root
		if ws := openhumanEnvWorkspace(); ws != "" {
			kd = ws
		}
		_ = ReadJSON(filepath.Join(kd, "dev-keychain.json"), &p.keyring)
		if blob, err := os.ReadFile(filepath.Join(kd, "secrets.enc")); err == nil {
			if hexKey, err := openhumanKeychain().Get("openhuman", "app:master_key"); err == nil {
				if k, err := zeroclawParseKey(hexKey); err == nil {
					if plain, err := zeroclawOpen(blob, k); err == nil {
						var m map[string]string
						if json.Unmarshal(plain, &m) == nil {
							for k, v := range m {
								if _, ok := p.keyring[k]; !ok {
									p.keyring[k] = v
								}
							}
						}
					} else {
						p.warn = append(p.warn, "secrets.enc could not be decrypted with the keychain key")
					}
				}
			}
		}
	}
	if v, ok := p.keyring[full]; ok && v != "" {
		return v, true
	}
	if v, err := openhumanKeychain().Get("openhuman", full); err == nil && v != "" {
		return v, true
	}
	return "", false
}

// masterKey is the SecretStore key for enc2:/enc: config values.
func (p *openhumanPlanner) masterKey() []byte {
	if p.master != nil || !p.secrets {
		return p.master
	}
	if v, ok := p.keyringGet("secretstore.master_key"); ok {
		if k, err := zeroclawParseKey(v); err == nil {
			p.master = k
			return k
		}
	}
	if k, err := zeroclawLoadKey(filepath.Join(p.in.configDir, ".secret_key")); err == nil {
		p.master = k
	}
	return p.master
}

// secret opens a possibly encrypted config value; ok=false when set but
// unreadable.
func (p *openhumanPlanner) secret(v any) (string, bool) {
	s := str(v)
	if s == "" || !zeroclawEncrypted(s) {
		return s, true
	}
	if !p.secrets {
		return "", false
	}
	k := p.masterKey()
	if k == nil {
		return "", false
	}
	out, err := zeroclawDecryptValue(s, k)
	return out, err == nil
}

// ---- providers and model -----------------------------------------------------------

type openhumanProfile struct {
	Provider    string `json:"provider"`
	ProfileName string `json:"profile_name"`
	Kind        string `json:"kind"`
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

type openhumanProfiles struct {
	ActiveProfiles map[string]string           `json:"active_profiles"`
	Profiles       map[string]openhumanProfile `json:"profiles"`
}

// profileKey finds the key stored for provider slug: the active profile for
// "provider:<slug>" (or bare "<slug>"), else "<…>:default". oauth reports an
// OAuth profile (not portable).
func (p *openhumanPlanner) profileKey(ap openhumanProfiles, slug string) (key string, found, oauth bool) {
	for _, prov := range []string{"provider:" + slug, slug} {
		id := ap.ActiveProfiles[prov]
		if id == "" {
			id = prov + ":default"
		}
		prof, ok := ap.Profiles[id]
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(prof.Kind), "auth") && prof.Kind != "token" {
			return "", true, true
		}
		tok := firstNonEmpty(prof.Token, prof.AccessToken)
		if tok != "" {
			if v, ok := p.secret(tok); ok {
				return v, true, false
			}
			return "", true, false
		}
		if v, ok := p.keyringGet("auth:" + id); ok {
			var sec struct {
				Token       string `json:"token"`
				AccessToken string `json:"access_token"`
			}
			if json.Unmarshal([]byte(v), &sec) == nil {
				return firstNonEmpty(sec.Token, sec.AccessToken), true, false
			}
			return v, true, false
		}
		return "", true, false
	}
	return "", false, false
}

func (p *openhumanPlanner) taken(id string) bool {
	return p.planned[id] || p.env != nil && p.env.ProviderExists(id)
}

func (p *openhumanPlanner) addProvider(ref string, pp ProviderPayload, model, detail string) {
	if p.planned[pp.ID] {
		p.bySlug[ref] = pp.ID
		return
	}
	if model != "" {
		pp.Models = []string{model}
	}
	p.planned[pp.ID] = true
	p.bySlug[ref] = pp.ID
	p.add(ProviderItem(p.env, pp, detail))
}

func (p *openhumanPlanner) providers() {
	var ap openhumanProfiles
	_ = ReadJSON(filepath.Join(p.in.configDir, "auth-profiles.json"), &ap)
	if _, ok := ap.Profiles["app-session"]; ok {
		p.add(UnsupportedItem(CatProvider, "openhuman-account", "OpenHuman account",
			"OpenHuman account login (hosted models and integrations) — Antares has no equivalent; add a provider key instead"))
	}
	seen := map[string]bool{}
	list, _ := p.cfg["cloud_providers"].([]any)
	for _, raw := range list {
		cp, _ := raw.(map[string]any)
		slug, id := str(cp["slug"]), str(cp["id"])
		if slug == "" {
			continue
		}
		seen[slug] = true
		if slug == "openhuman" || strings.Contains(strings.ToLower(str(cp["auth_style"])), "jwt") {
			if !p.hasItem("provider:openhuman-account") {
				p.add(UnsupportedItem(CatProvider, "openhuman-cloud", firstNonEmpty(str(cp["label"]), "OpenHuman"),
					"OpenHuman's hosted models need an OpenHuman account — not available in Antares"))
			}
			continue
		}
		endpoint := str(cp["endpoint"])
		key, found, oauth := p.profileKey(ap, slug)
		if oauth {
			p.add(OAuthProviderItem(Slug(slug), firstNonEmpty(str(cp["label"]), slug)))
			continue
		}
		pp, ok := openhumanPayload(slug, str(cp["label"]), endpoint, p.taken)
		if !ok {
			p.add(UnsupportedItem(CatProvider, Slug(slug), slug, "unknown provider with no endpoint"))
			continue
		}
		pp.APIKey = key
		detail := "from config.toml"
		if found {
			detail = "key from auth-profiles"
		}
		p.addProvider(slug, pp, str(cp["default_model"]), detail)
		if id != "" {
			p.bySlug[id] = pp.ID
		}
	}
	// Keys stored for providers not in cloud_providers.
	for _, pid := range sortedKeys(ap.Profiles) {
		prof := ap.Profiles[pid]
		slug := strings.TrimPrefix(prof.Provider, "provider:")
		if !strings.HasPrefix(prof.Provider, "provider:") || slug == "" || seen[slug] {
			continue
		}
		if _, known := Vendor(slug); !known {
			continue
		}
		seen[slug] = true
		key, _, oauth := p.profileKey(ap, slug)
		v, _ := Vendor(slug)
		if oauth || v.OAuth {
			p.add(OAuthProviderItem(Slug(slug), v.Label))
			continue
		}
		pp, _ := openhumanPayload(slug, "", "", p.taken)
		pp.APIKey = key
		p.addProvider(slug, pp, "", "key from auth-profiles")
	}
	// Legacy single custom endpoint.
	if u := str(p.cfg["inference_url"]); u != "" {
		key, _ := p.secret(p.cfg["api_key"])
		pp, _ := openhumanPayload("", "", u, p.taken)
		pp.APIKey = key
		p.addProvider("inference_url", pp, str(p.cfg["default_model"]), "legacy inference_url")
	}
}

func (p *openhumanPlanner) hasItem(id string) bool {
	for _, it := range p.items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// openhumanPayload maps a cloud provider slug/endpoint to a payload.
func openhumanPayload(slug, label, endpoint string, taken func(string) bool) (ProviderPayload, bool) {
	v, known := Vendor(slug)
	if !known && endpoint != "" {
		v, known = VendorByBaseURL(endpoint)
	}
	if known {
		return ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: firstNonEmpty(endpoint, v.BaseURL)}, true
	}
	if endpoint == "" {
		return ProviderPayload{}, false
	}
	name := firstNonEmpty(label, slug, hostLabel(endpoint))
	return ProviderPayload{ID: CustomProviderID(taken, name), Label: name, Kind: "openai-compatible", BaseURL: endpoint}, true
}

func (p *openhumanPlanner) model() {
	var prov, model string
	if cp := str(p.cfg["chat_provider"]); cp != "" {
		slug, m, ok := strings.Cut(cp, ":")
		if ok {
			m, _, _ = strings.Cut(m, "@") // "<model>@<temperature>"
			switch slug {
			case "ollama", "lmstudio":
				if !p.planned[slug] {
					v, _ := Vendor(slug)
					p.addProvider(slug, ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL}, m, "local model server")
				}
			}
			prov, model = p.bySlug[slug], m
		}
	}
	if prov == "" || model == "" {
		prov, model = p.bySlug[str(p.cfg["primary_cloud"])], str(p.cfg["default_model"])
	}
	if prov == "" || model == "" {
		return
	}
	for _, it := range p.items {
		if pp := it.Payload.Provider; pp != nil && pp.ID == prov && !contains(pp.Models, model) {
			pp.Models = append(pp.Models, model)
		}
	}
	p.add(ModelItem(p.env, ModelPayload{Provider: prov, Model: model}))
}

// ---- workspace --------------------------------------------------------------------

// openhumanUserFile reads a workspace file, skipping a seeded default whose
// ".<name>.builtin-hash" sidecar matches its SHA-256.
func openhumanUserFile(ws, name string) string {
	t := ReadText(filepath.Join(ws, name))
	if t == "" {
		return ""
	}
	if h := strings.ToLower(ReadText(filepath.Join(ws, "."+name+".builtin-hash"))); h != "" {
		raw, _ := os.ReadFile(filepath.Join(ws, name))
		sum := sha256.Sum256(raw)
		if h == hex.EncodeToString(sum[:]) {
			return ""
		}
	}
	return t
}

func (p *openhumanPlanner) persona() {
	ws := p.in.workspace
	var parts, from []string
	for _, f := range []string{"SOUL.md", "IDENTITY.md", "ROLE.md", "STYLE.md"} {
		if t := openhumanUserFile(ws, f); t != "" {
			parts = append(parts, t)
			from = append(from, f)
		}
	}
	if it, ok := TextItem(p.env, CatSoul, strings.Join(parts, "\n\n"), strings.Join(from, " + ")); ok {
		p.add(it)
	}
	if it, ok := TextItem(p.env, CatAgentsMD, openhumanUserFile(ws, "AGENTS.md"), "AGENTS.md"); ok {
		p.add(it)
	}
	var user, ufrom []string
	for _, f := range []string{"PROFILE.md", "USER.md"} {
		if t := ReadText(filepath.Join(ws, f)); t != "" {
			user = append(user, t)
			ufrom = append(ufrom, f)
		}
	}
	if it, ok := TextItem(p.env, CatUserMD, strings.Join(user, "\n\n"), strings.Join(ufrom, " + ")); ok {
		p.add(it)
	}
}

func (p *openhumanPlanner) memory() {
	ws := p.in.workspace
	entries := SplitMarkdownEntries(ReadText(filepath.Join(ws, "MEMORY.md")))
	for i := range entries {
		if strings.EqualFold(entries[i].Heading, "memory") {
			entries[i].Heading = ""
		}
	}
	p.add(MemoryItems(entries, "global", "import:openhuman")...)
	if IsFile(filepath.Join(ws, "memory", "memory.db")) {
		p.warn = append(p.warn, "OpenHuman's memory database (memory/memory.db) is not imported; MEMORY.md and the Markdown memory vault are.")
	}
	vault := filepath.Join(ws, "memory_tree", "content")
	if d := str(zeroclawMap(p.cfg, "memory_tree", "content_dir")); d != "" {
		vault = ExpandHome(d)
		if !filepath.IsAbs(vault) {
			vault = filepath.Join(ws, vault)
		}
	}
	_ = filepath.WalkDir(vault, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(vault, path)
		if d.IsDir() {
			// episodic/ is the conversation archive: chat history is not migrated.
			if rel == "episodic" || path != vault && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			if it, ok := KnowledgeItem(p.env, filepath.ToSlash(rel), ReadText(path)); ok {
				p.add(it)
			}
		}
		return nil
	})
}

func (p *openhumanPlanner) skills() {
	seen := map[string]bool{}
	for _, root := range []string{filepath.Join(p.in.root, "skills"), filepath.Join(p.in.workspace, "skills")} {
		for _, sd := range ScanSkills(root, 2) {
			if !seen[sd.Name] {
				seen[sd.Name] = true
				p.add(SkillItem(p.env, sd))
			}
		}
	}
}

// ---- MCP, cron, channels --------------------------------------------------------

func (p *openhumanPlanner) mcp() {
	list, _ := zeroclawMap(p.cfg, "mcp_client", "servers").([]any)
	for _, raw := range list {
		s, _ := raw.(map[string]any)
		name := Slug(str(s["name"]))
		if name == "" {
			continue
		}
		env := map[string]string{}
		for k, v := range func() map[string]any { m, _ := s["env"].(map[string]any); return m }() {
			env[k], _ = p.secret(v)
		}
		if len(env) == 0 {
			env = nil
		}
		m := MCPPayload{Name: name, Command: str(s["command"]), Args: strList(s["args"]), Env: env, URL: str(s["endpoint"])}
		if m.URL != "" {
			m.Transport = "http"
		} else {
			m.Transport = "stdio"
		}
		auth, _ := s["auth"].(map[string]any)
		hdr := map[string]string{}
		switch str(auth["kind"]) {
		case "bearer_token":
			if t, _ := p.secret(auth["token"]); t != "" {
				hdr["Authorization"] = "Bearer " + t
			}
		case "header":
			if n := str(auth["name"]); n != "" {
				hdr[n], _ = p.secret(auth["value"])
			}
		case "headers":
			hs, _ := auth["headers"].([]any)
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if n := str(hm["name"]); n != "" {
					hdr[n], _ = p.secret(hm["value"])
				}
			}
		case "basic", "query_param":
			p.warn = append(p.warn, fmt.Sprintf("MCP server %q uses %s auth, which is not migrated; add it in Antares", name, str(auth["kind"])))
		}
		if len(hdr) > 0 {
			m.Headers = hdr
		}
		it := MCPItem(p.env, m)
		if b, ok := s["enabled"].(bool); ok && !b && it.Status == StatusReady {
			it.Detail = strings.TrimSpace(it.Detail + " · disabled in OpenHuman")
		}
		p.add(it)
	}
}

func (p *openhumanPlanner) cron() {
	path := filepath.Join(p.in.workspace, "cron", "jobs.db")
	if !IsFile(path) {
		return
	}
	zeroclawCronRows(path, map[string]bool{}, &p.warn, func(key, name string, job, sched map[string]any) {
		p.add(zeroclawCronJobItem(key, name, job, sched))
	})
}

func (p *openhumanPlanner) channels() {
	sec, _ := p.cfg["channels_config"].(map[string]any)
	used := map[string]string{}
	for _, typ := range sortedKeys(sec) {
		b, ok := sec[typ].(map[string]any)
		if !ok || typ == "relay" {
			continue
		}
		title := PlatformLabel(typ)
		platform, fields, missing, reason := zeroclawChannelFields(typ, b, nil, p.secret)
		if reason != "" {
			p.add(UnsupportedItem(CatChannel, typ, title, reason))
			continue
		}
		if prev, dup := used[platform]; dup {
			p.add(UnsupportedItem(CatChannel, typ, title, "Antares has one "+PlatformLabel(platform)+" bot; "+prev+" is already being imported"))
			continue
		}
		used[platform] = title
		if !p.secrets {
			missing = ""
		}
		it := ChannelItem(p.env, platform, title, fields, missing)
		it.ID = ItemID(CatChannel, typ)
		p.add(it)
	}
}
