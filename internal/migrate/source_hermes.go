package migrate

// Hermes Agent (github.com/NousResearch/hermes-agent). Formats verified
// against the project's source at commit 4e3fcd5 (release 2026.9.24); paths
// below are relative to that repo.
//
//   - Home: ~/.hermes, or $HERMES_HOME (hermes_constants.py:58,111-118).
//     Profiles: <home>/profiles/<name>/ with the same layout, name matching
//     ^[a-z0-9][a-z0-9_-]{0,63}$ (hermes_cli/profiles.py:33, PROFILE_ID_RE);
//     dot-prefixed dirs are tombstones/state, not profiles.
//   - config.yaml: model{default|model, provider, base_url, api_key},
//     fallback_providers[{provider, model}] (legacy fallback_model), providers
//     map and legacy custom_providers list (hermes_cli/config_providers.py:
//     186-300, hermes_cli/config.py:1000-1060), mcp_servers map
//     (website/docs/reference/mcp-config-reference.md), timezone
//     (hermes_cli/config_defaults.py:1530), agent.personalities.
//     Gateway platform blocks under gateway.platforms.<p>, platforms.<p> or
//     gateway.<p> (gateway/config_loader.py:208-224).
//   - .env: provider keys (plugins/model-providers/*/__init__.py,
//     hermes_cli/auth.py:172-200) and channel credentials
//     (gateway/config.py:217, gateway/authz_mixin.py:32-36).
//   - SOUL.md (agent/prompt_builder.py:1627); memories/MEMORY.md and
//     memories/USER.md split by ENTRY_DELIMITER "\n§\n"
//     (tools/memory_tool_store.py:23).
//   - skills/<category>/<name>/SKILL.md; bundled skills recorded in
//     skills/.bundled_manifest as "name:md5" with the md5 of _dir_hash
//     (tools/skills_sync.py:120-200).
//   - cron/jobs.json {"jobs":[…]} (also a bare list or id-keyed map), schedule
//     kinds cron{expr} | interval{minutes} | once{run_at} (cron/jobs.py:767-815,
//     1389, 1554). No per-job timezone: the config timezone applies.
//   - auth.json {providers, credential_pool} (hermes_cli/auth.py:740-787):
//     OAuth logins (nous, openai-codex, copilot, …) are listed as unsupported.
//   - Running: launchd ai.hermes.gateway[-<profile>]
//     (hermes_cli/gateway_launchd.py:25), process "hermes_cli.main gateway
//     run", gateway.pid JSON {pid,…} (gateway/status.py:33,898).
//   - Version: <home>/hermes-agent/install-stamp.json "baseVersion"
//     (pm/paths.py:21-31).
//
// UNVERIFIED: the key field of api_key entries in auth.json credential_pool
// (read as "access_token", then "api_key"); the exact runtime-cache files
// _dir_hash skips (we skip __pycache__ and *.pyc); Kimi/Nous key-based
// providers have no Antares equivalent and are listed as unsupported.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type hermesSource struct{}

func init() { Register(hermesSource{}) }

func (hermesSource) ID() string   { return "hermes" }
func (hermesSource) Name() string { return "Hermes Agent" }

var hermesProfileRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func hermesDefaultRoot() string {
	if v := strings.TrimSpace(os.Getenv("HERMES_HOME")); v != "" {
		return ExpandHome(os.ExpandEnv(v))
	}
	return filepath.Join(HomeDir(), ".hermes")
}

func hermesLooksInstalled(dir string) bool {
	if !IsDir(dir) {
		return false
	}
	for _, f := range []string{"config.yaml", ".env", "SOUL.md", "auth.json", "state.db", "profile.yaml"} {
		if IsFile(filepath.Join(dir, f)) {
			return true
		}
	}
	return IsDir(filepath.Join(dir, "memories")) || IsDir(filepath.Join(dir, "skills"))
}

func (s hermesSource) Detect(ctx context.Context, root string) (Detection, error) {
	all, err := s.DetectAll(ctx, root)
	if err != nil || len(all) == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Detection{}, err
	}
	return all[0], nil
}

// DetectAll returns the default home plus each profile under profiles/.
func (s hermesSource) DetectAll(ctx context.Context, root string) ([]Detection, error) {
	if root == "" {
		root = hermesDefaultRoot()
	}
	root = ExpandHome(root)
	var out []Detection
	if hermesLooksInstalled(root) {
		out = append(out, s.detectOne(ctx, root, root, ""))
	}
	entries, _ := os.ReadDir(filepath.Join(root, "profiles"))
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !hermesProfileRE.MatchString(name) || name == "default" {
			continue
		}
		dir := filepath.Join(root, "profiles", name)
		if hermesLooksInstalled(dir) {
			out = append(out, s.detectOne(ctx, dir, root, name))
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s hermesSource) detectOne(ctx context.Context, dir, base, profile string) Detection {
	d := Detection{Source: s.ID(), Name: s.Name(), Root: dir, Profile: profile}
	var stamp struct {
		BaseVersion string `json:"baseVersion"`
	}
	if ReadJSON(filepath.Join(base, "hermes-agent", "install-stamp.json"), &stamp) == nil {
		d.Version = stamp.BaseVersion
	}
	label := "ai.hermes.gateway"
	if profile != "" {
		label += "-" + profile
	}
	d.Running = hermesPIDRunning(filepath.Join(dir, "gateway.pid")) ||
		RunningCheck([]string{"hermes_cli.main gateway", "hermes_cli/main.py gateway", "hermes gateway run"}, []string{label})
	// Detection must not prompt or decrypt; Hermes keeps nothing encrypted,
	// so a plan with no Env is a cheap, faithful summary.
	if p, err := s.plan(d, nil); err == nil {
		d.Summary = Summarize(p.Items)
	}
	return d
}

func hermesPIDRunning(path string) bool {
	var rec struct {
		PID  int    `json:"pid"`
		Kind string `json:"kind"`
	}
	if ReadJSON(path, &rec) != nil {
		// Older files hold a bare pid.
		n, err := strconv.Atoi(ReadText(path))
		if err != nil {
			return false
		}
		rec.PID = n
	}
	return PIDAlive(rec.PID)
}

func (s hermesSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	return s.plan(det, env)
}

// hermesConfig is the subset of config.yaml we read. Model may be a string or
// a map, so it is decoded loosely.
type hermesConfig struct {
	Model             any                       `yaml:"model"`
	FallbackProviders []hermesFallback          `yaml:"fallback_providers"`
	FallbackModel     any                       `yaml:"fallback_model"`
	Providers         map[string]map[string]any `yaml:"providers"`
	CustomProviders   []map[string]any          `yaml:"custom_providers"`
	MCPServers        map[string]map[string]any `yaml:"mcp_servers"`
	Timezone          string                    `yaml:"timezone"`
	Agent             struct {
		Personalities map[string]any `yaml:"personalities"`
	} `yaml:"agent"`
	Display struct {
		Personality string `yaml:"personality"`
	} `yaml:"display"`
	Platforms map[string]map[string]any `yaml:"platforms"`
	Gateway   map[string]any            `yaml:"gateway"`
}

type hermesFallback struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type hermesPlanner struct {
	det     Detection
	env     Env
	dotenv  map[string]string
	cfg     hermesConfig
	items   []Item
	warn    []string
	planned map[string]bool   // provider ids already in this plan
	byName  map[string]string // hermes provider name → Antares provider id
}

func (s hermesSource) plan(det Detection, env Env) (Plan, error) {
	root := det.Root
	if !IsDir(root) {
		return Plan{}, ErrNotFound
	}
	p := &hermesPlanner{det: det, env: env, dotenv: ReadDotEnv(filepath.Join(root, ".env")),
		planned: map[string]bool{}, byName: map[string]string{}}
	if IsFile(filepath.Join(root, "config.yaml")) {
		if err := ReadYAML(filepath.Join(root, "config.yaml"), &p.cfg); err != nil {
			p.warn = append(p.warn, "config.yaml could not be read: "+err.Error())
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
	p.roles()
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

func (p *hermesPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// ---- providers -------------------------------------------------------------------

// hermesEnvProviders maps Hermes provider ids to the .env variables holding
// their keys (first wins) and the Antares vendor they become.
var hermesEnvProviders = []struct {
	hermes, vendor string
	vars           []string
}{
	{"openrouter", "openrouter", []string{"OPENROUTER_API_KEY"}},
	{"anthropic", "anthropic", []string{"ANTHROPIC_API_KEY"}},
	{"openai", "openai", []string{"OPENAI_API_KEY"}},
	{"gemini", "gemini", []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
	{"zai", "zai", []string{"GLM_API_KEY", "ZAI_API_KEY", "Z_AI_API_KEY"}},
	{"deepseek", "deepseek", []string{"DEEPSEEK_API_KEY"}},
	{"xai", "xai", []string{"XAI_API_KEY"}},
	{"minimax", "minimax", []string{"MINIMAX_API_KEY"}},
	{"nvidia", "nvidia", []string{"NVIDIA_API_KEY"}},
	{"huggingface", "huggingface", []string{"HF_TOKEN"}},
	{"fireworks", "fireworks", []string{"FIREWORKS_API_KEY"}},
	{"alibaba", "dashscope", []string{"DASHSCOPE_API_KEY"}},
	{"opencode-go", "opencode", []string{"OPENCODE_GO_API_KEY"}},
	{"lmstudio", "lmstudio", []string{"LM_API_KEY"}},
}

// hermesNoEquivalent are key-based Hermes providers Antares has no preset for.
var hermesNoEquivalent = map[string]string{
	"KIMI_API_KEY": "Kimi Coding", "KIMI_CODING_API_KEY": "Kimi Coding", "KIMI_CN_API_KEY": "Kimi Coding (China)",
	"NOUS_API_KEY": "Nous Portal", "AI_GATEWAY_API_KEY": "Vercel AI Gateway", "KILOCODE_API_KEY": "Kilo Code",
}

// hermesProviderAlias maps the names Hermes uses in model.provider and
// fallback_providers to the vendor names Vendor() knows.
var hermesProviderAlias = map[string]string{
	"openai-api": "openai", "alibaba": "dashscope", "opencode-go": "opencode", "opencode-zen": "opencode",
	"kimi-coding": "kimi", "ollama-cloud": "ollama",
}

func (p *hermesPlanner) lookup(name string) string {
	if v := p.dotenv[name]; v != "" {
		return v
	}
	return ""
}

func (p *hermesPlanner) addProvider(pp ProviderPayload, hermesName, detail string) {
	if p.planned[pp.ID] {
		if hermesName != "" {
			p.byName[hermesName] = pp.ID
		}
		return
	}
	p.planned[pp.ID] = true
	if hermesName != "" {
		p.byName[hermesName] = pp.ID
	}
	p.add(ProviderItem(p.env, pp, detail))
}

func (p *hermesPlanner) providers() {
	for _, ep := range hermesEnvProviders {
		key, from := "", ""
		for _, v := range ep.vars {
			if k := p.lookup(v); k != "" {
				key, from = k, v
				break
			}
		}
		vendor := ep.vendor
		if ep.hermes == "openai" && strings.HasPrefix(key, "sk-or-") {
			vendor = "openrouter" // a legacy OpenRouter key kept in OPENAI_API_KEY
		}
		if key == "" && !(ep.hermes == "lmstudio" && p.lookup("LM_BASE_URL") != "") {
			continue
		}
		v, _ := Vendor(vendor)
		pp := ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL, APIKey: key}
		switch ep.hermes {
		case "openai":
			if u := p.lookup("OPENAI_BASE_URL"); u != "" && !strings.Contains(u, "api.openai.com") {
				pp = ProviderPayload{ID: CustomProviderID(p.taken, hostLabel(u)), Label: hostLabel(u),
					Kind: "openai-compatible", BaseURL: u, APIKey: key}
			}
		case "gemini":
			if u := p.lookup("GEMINI_BASE_URL"); u != "" {
				pp.BaseURL = u
			}
		case "lmstudio":
			if u := p.lookup("LM_BASE_URL"); u != "" {
				pp.BaseURL = u
			}
			from = "LM_BASE_URL"
		}
		if _, dup := p.byName[ep.hermes]; dup {
			continue
		}
		p.addProvider(pp, ep.hermes, "key from .env ("+from+")")
	}
	for _, v := range sortedKeys(hermesNoEquivalent) {
		if p.lookup(v) != "" {
			p.add(UnsupportedItem(CatProvider, Slug(hermesNoEquivalent[v]), hermesNoEquivalent[v],
				"no matching Antares provider — add it as a custom provider"))
		}
	}
	if p.lookup("ANTHROPIC_API_KEY") == "" && (p.lookup("ANTHROPIC_TOKEN") != "" || p.lookup("CLAUDE_CODE_OAUTH_TOKEN") != "") {
		p.add(OAuthProviderItem("anthropic-oauth", "Claude subscription"))
	}

	// config.yaml providers map (modern) and custom_providers list (legacy).
	for _, name := range sortedKeys(p.cfg.Providers) {
		p.configProvider(name, p.cfg.Providers[name])
	}
	for _, e := range p.cfg.CustomProviders {
		name := str(e["name"])
		if name == "" {
			name = hostLabel(firstStr(e, "base_url", "url", "api"))
		}
		p.configProvider(name, e)
	}
	// model.base_url / model.api_key: an inline custom endpoint.
	if m, ok := p.cfg.Model.(map[string]any); ok {
		if u := str(m["base_url"]); u != "" {
			if _, known := p.byName["custom"]; !known {
				key, _ := p.secret(str(m["api_key"]))
				if v, ok := VendorByBaseURL(u); ok && !v.OAuth {
					p.addProvider(ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: u, APIKey: firstNonEmpty(key, p.keyFor(v.ID))}, "custom", "from model.base_url")
				} else {
					p.addProvider(ProviderPayload{ID: CustomProviderID(p.taken, hostLabel(u)), Label: hostLabel(u),
						Kind: "openai-compatible", BaseURL: u, APIKey: key}, "custom", "from model.base_url")
				}
			}
		}
	}

	// auth.json: API keys in the credential pool; OAuth logins listed.
	var auth struct {
		Providers      map[string]any              `json:"providers"`
		CredentialPool map[string][]map[string]any `json:"credential_pool"`
	}
	if ReadJSON(filepath.Join(p.det.Root, "auth.json"), &auth) == nil {
		oauth := map[string]bool{}
		for _, prov := range sortedKeys(auth.CredentialPool) {
			for _, c := range auth.CredentialPool[prov] {
				at := str(c["auth_type"])
				if at == "api_key" {
					key := firstStr(c, "access_token", "api_key", "key")
					vname := prov
					if a, ok := hermesProviderAlias[prov]; ok {
						vname = a
					}
					v, ok := Vendor(vname)
					if key == "" || !ok || v.OAuth {
						continue
					}
					if _, have := p.byName[prov]; have {
						continue
					}
					pp := ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: firstNonEmpty(str(c["base_url"]), v.BaseURL), APIKey: key}
					p.addProvider(pp, prov, "key from auth.json")
					continue
				}
				if at != "" {
					oauth[prov] = true
				}
			}
		}
		for _, prov := range sortedKeys(auth.Providers) {
			if _, have := p.byName[prov]; !have {
				oauth[prov] = true
			}
		}
		for _, prov := range sortedKeys(oauth) {
			label := prov
			if v, ok := Vendor(prov); ok {
				label = v.Label
			}
			p.add(OAuthProviderItem(Slug(prov)+"-login", label+" login"))
		}
	}
}

func (p *hermesPlanner) keyFor(id string) string {
	for _, it := range p.items {
		if it.Payload.Provider != nil && it.Payload.Provider.ID == id {
			return it.Payload.Provider.APIKey
		}
	}
	return ""
}

func (p *hermesPlanner) taken(id string) bool { return p.planned[id] }

// secret resolves a config value that may reference ${VAR}; ok=false when a
// reference could not be resolved.
func (p *hermesPlanner) secret(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || !strings.Contains(v, "$") {
		return v, true
	}
	out, ok := ExpandVars(v, p.dotenv)
	if !ok {
		return "", false
	}
	return out, true
}

func (p *hermesPlanner) configProvider(name string, e map[string]any) {
	if e == nil {
		return
	}
	if b, ok := e["enabled"].(bool); ok && !b {
		return
	}
	baseURL := firstStr(e, "base_url", "url", "api")
	if !strings.Contains(baseURL, "://") {
		baseURL = ""
	}
	key, ok := p.secret(str(e["api_key"]))
	detail := "from config.yaml"
	if !ok {
		key = ""
		detail = "key reference in config.yaml could not be resolved"
	}
	if key == "" {
		if ke := firstStr(e, "key_env", "api_key_env"); ke != "" {
			key, _ = ExpandVars("${"+ke+"}", p.dotenv)
			if key != "" {
				detail = "key from " + ke
			}
		}
	}
	if key == "" && str(e["key_cmd"]) != "" {
		detail = "key comes from a command (key_cmd); enter it here"
	}
	headers := map[string]string{}
	if h, ok := e["extra_headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := p.secret(fmt.Sprint(v)); ok {
				headers[k] = s
			}
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	models := hermesModels(e["models"])
	if m := firstStr(e, "model", "default_model"); m != "" && !contains(models, m) {
		models = append([]string{m}, models...)
	}
	// A block named after a known vendor without its own endpoint overlays
	// that vendor (e.g. a key for openrouter).
	if v, ok := Vendor(name); ok && !v.OAuth && (baseURL == "" || sameURL(baseURL, v.BaseURL)) {
		if _, have := p.byName[name]; have {
			return
		}
		p.addProvider(ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL,
			APIKey: firstNonEmpty(key, p.keyFor(v.ID)), Headers: headers, Models: models}, name, detail)
		return
	}
	if baseURL == "" {
		return
	}
	kind := "openai-compatible"
	if strings.Contains(strings.ToLower(firstStr(e, "api_mode", "transport")), "anthropic") {
		kind = "anthropic"
	}
	label := firstNonEmpty(str(e["name"]), name)
	p.addProvider(ProviderPayload{ID: CustomProviderID(p.taken, label), Label: label, Kind: kind, BaseURL: baseURL,
		APIKey: key, Headers: headers, Models: models}, name, detail)
}

// hermesModels reads a providers[].models value: a map keyed by model id, a
// list of ids, or a list of {id|name}.
func hermesModels(v any) []string {
	var out []string
	switch m := v.(type) {
	case map[string]any:
		out = sortedKeys(m)
	case []any:
		for _, x := range m {
			switch e := x.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				if id := firstStr(e, "id", "name"); id != "" {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// ---- model --------------------------------------------------------------------

func (p *hermesPlanner) resolveProvider(name, model string) string {
	name = strings.TrimSpace(name)
	if id, ok := p.byName[name]; ok {
		return id
	}
	switch name {
	case "ollama", "vllm", "llamacpp", "local", "custom":
		return p.byName["custom"]
	case "", "auto":
		if strings.Contains(model, "/") && p.planned["openrouter"] {
			return "openrouter"
		}
		for _, it := range p.items {
			if it.Category == CatProvider && it.Payload.Provider != nil && it.Status != StatusUnsupported {
				return it.Payload.Provider.ID
			}
		}
		return ""
	}
	if a, ok := hermesProviderAlias[name]; ok {
		name = a
	}
	if v, ok := Vendor(name); ok && !v.OAuth {
		return v.ID
	}
	return ""
}

func (p *hermesPlanner) model() {
	var model, prov string
	switch m := p.cfg.Model.(type) {
	case string:
		model = m
	case map[string]any:
		model = firstStr(m, "default", "model")
		prov = str(m["provider"])
	}
	if strings.TrimSpace(model) == "" {
		return
	}
	mp := ModelPayload{Provider: p.resolveProvider(prov, model), Model: model}
	fbs := p.cfg.FallbackProviders
	switch f := p.cfg.FallbackModel.(type) {
	case map[string]any:
		fbs = append(fbs, hermesFallback{Provider: str(f["provider"]), Model: str(f["model"])})
	case []any:
		for _, x := range f {
			if m, ok := x.(map[string]any); ok {
				fbs = append(fbs, hermesFallback{Provider: str(m["provider"]), Model: str(m["model"])})
			}
		}
	}
	for _, f := range fbs {
		if f.Model == "" {
			continue
		}
		if id := p.resolveProvider(f.Provider, f.Model); id != "" {
			mp.Fallback = append(mp.Fallback, id+"/"+f.Model)
		} else {
			mp.Fallback = append(mp.Fallback, f.Model)
		}
	}
	it := ModelItem(p.env, mp)
	if mp.Provider == "" && it.Status == StatusReady {
		it.Detail = strings.TrimSpace(it.Detail + " · provider " + prov + " has no Antares equivalent")
	}
	p.add(it)
}

// ---- persona & memory -----------------------------------------------------

func (p *hermesPlanner) persona() {
	if it, ok := TextItem(p.env, CatSoul, ReadText(filepath.Join(p.det.Root, "SOUL.md")), "SOUL.md"); ok {
		p.add(it)
	}
	user := SplitMemory(ReadText(filepath.Join(p.det.Root, "memories", "USER.md")), HermesMemorySeparator)
	if len(user) > 0 {
		lines := make([]string, 0, len(user))
		for _, e := range user {
			lines = append(lines, "- "+strings.ReplaceAll(e.Content, "\n", "\n  "))
		}
		if it, ok := TextItem(p.env, CatUserMD, strings.Join(lines, "\n"), "memories/USER.md"); ok {
			p.add(it)
		}
	}
}

func (p *hermesPlanner) memory() {
	entries := SplitMemory(ReadText(filepath.Join(p.det.Root, "memories", "MEMORY.md")), HermesMemorySeparator)
	p.add(MemoryItems(entries, "global", "hermes")...)
}

// ---- skills -----------------------------------------------------------------------

func (p *hermesPlanner) skills() {
	root := filepath.Join(p.det.Root, "skills")
	bundled := map[string]string{}
	for _, line := range strings.Split(ReadText(filepath.Join(root, ".bundled_manifest")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, hash, _ := strings.Cut(line, ":")
		bundled[Slug(name)] = strings.TrimSpace(hash)
	}
	skipped := 0
	for _, sd := range ScanSkills(root, 2) {
		if h, ok := bundled[sd.Name]; ok && (h == "" || hermesDirHash(sd.Dir) == h) {
			skipped++ // a stock bundled skill, unchanged: not the user's own
			continue
		}
		p.add(SkillItem(p.env, sd))
	}
	if skipped > 0 {
		p.warn = append(p.warn, fmt.Sprintf("%d unchanged bundled Hermes skills were left out.", skipped))
	}
}

// hermesDirHash mirrors tools/skills_sync.py _dir_hash: md5 over every file
// under dir in sorted path order, each contributing its relative path (as
// Python's str(Path)) then its bytes, skipping runtime caches.
func hermesDirHash(dir string) string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "__pycache__" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() && !strings.HasSuffix(d.Name(), ".pyc") {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	// pathlib sorts by path components, not by the joined string.
	sort.Slice(files, func(i, j int) bool {
		a, b := strings.Split(files[i], "/"), strings.Split(files[j], "/")
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	h := md5.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		h.Write([]byte(f))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- MCP ------------------------------------------------------------------------

func (p *hermesPlanner) mcp() {
	for _, name := range sortedKeys(p.cfg.MCPServers) {
		e := p.cfg.MCPServers[name]
		key := Slug(name)
		if key == "" {
			continue
		}
		if b, ok := e["enabled"].(bool); ok && !b {
			p.add(UnsupportedItem(CatMCP, key, name, "disabled in Hermes"))
			continue
		}
		if str(e["auth"]) == "oauth" {
			p.add(UnsupportedItem(CatMCP, key, name, "OAuth MCP login — connect it again in Antares"))
			continue
		}
		m := MCPPayload{Name: key, Transport: str(e["transport"]), Command: str(e["command"]),
			Args: strList(e["args"]), URL: str(e["url"]), Env: p.strMap(e["env"]), Headers: p.strMap(e["headers"])}
		if m.URL != "" && m.Transport == "" {
			m.Transport = "http" // Hermes defaults to Streamable HTTP
		}
		p.add(MCPItem(p.env, m))
	}
}

// strMap reads a string map, resolving ${VAR} references from .env; an
// unresolvable reference is kept as written so the user can see it.
func (p *hermesPlanner) strMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		s := fmt.Sprint(x)
		if r, ok := p.secret(s); ok {
			s = r
		}
		out[k] = s
	}
	return out
}

// ---- cron -------------------------------------------------------------------------

type hermesJob struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Prompt   string         `json:"prompt"`
	Skills   []string       `json:"skills"`
	Skill    string         `json:"skill"`
	Script   *string        `json:"script"`
	NoAgent  bool           `json:"no_agent"`
	Schedule map[string]any `json:"schedule"`
	Enabled  *bool          `json:"enabled"`
	State    string         `json:"state"`
}

func (p *hermesPlanner) cron() {
	raw, err := readLimited(filepath.Join(p.det.Root, "cron", "jobs.json"))
	if err != nil {
		return
	}
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	var jobs []hermesJob
	var wrapped struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	if json.Unmarshal(raw, &jobs) != nil {
		if json.Unmarshal(raw, &wrapped) != nil {
			p.warn = append(p.warn, "cron/jobs.json could not be read")
			return
		}
		if json.Unmarshal(wrapped.Jobs, &jobs) != nil {
			byID := map[string]hermesJob{}
			if json.Unmarshal(wrapped.Jobs, &byID) != nil {
				p.warn = append(p.warn, "cron/jobs.json could not be read")
				return
			}
			for _, id := range sortedKeys(byID) {
				j := byID[id]
				if j.ID == "" {
					j.ID = id
				}
				jobs = append(jobs, j)
			}
		}
	}
	tz := firstNonEmpty(p.dotenv["HERMES_TIMEZONE"], p.cfg.Timezone)
	for i, j := range jobs {
		key := Slug(j.ID)
		if key == "" {
			key = strconv.Itoa(i + 1)
		}
		name := firstNonEmpty(j.Name, Truncate(firstLine(j.Prompt), 60), "Hermes job "+key)
		if (j.Script != nil && *j.Script != "") || j.NoAgent {
			p.add(UnsupportedItem(CatCron, key, name, "script job — Antares schedules run prompts"))
			continue
		}
		prompt := strings.TrimSpace(j.Prompt)
		skills := append([]string(nil), j.Skills...)
		if j.Skill != "" && !contains(skills, j.Skill) {
			skills = append(skills, j.Skill)
		}
		if len(skills) > 0 {
			use := "Use the " + strings.Join(skills, ", ") + " skill"
			if len(skills) > 1 {
				use += "s"
			}
			if prompt == "" {
				prompt = use + "."
			} else {
				prompt += "\n\n(" + use + ".)"
			}
		}
		sched := ""
		switch str(j.Schedule["kind"]) {
		case "cron":
			sched = str(j.Schedule["expr"])
		case "interval":
			mins, _ := strconv.ParseFloat(fmt.Sprint(j.Schedule["minutes"]), 64)
			if mins <= 0 {
				p.add(UnsupportedItem(CatCron, key, name, "interval has no length"))
				continue
			}
			sched = EverySchedule(time.Duration(mins * float64(time.Minute)))
		case "once":
			p.add(UnsupportedItem(CatCron, key, name, "one-shot schedule"))
			continue
		default:
			p.add(UnsupportedItem(CatCron, key, name, "unknown schedule kind"))
			continue
		}
		enabled := (j.Enabled == nil || *j.Enabled) && j.State != "paused"
		p.add(CronItem(key, CronPayload{Name: name, Schedule: sched, Prompt: prompt, Timezone: tz}, enabled))
	}
}

// ---- channels -------------------------------------------------------------------

// platformBlock merges the Hermes config.yaml blocks for a platform: gateway.json
// style gateway.<p>, then top-level platforms.<p>, then gateway.platforms.<p>
// (later wins, gateway/config_loader.py).
func (p *hermesPlanner) platformBlock(name string) map[string]any {
	out := map[string]any{}
	merge := func(v any) {
		if m, ok := v.(map[string]any); ok {
			for k, x := range m {
				out[k] = x
			}
		}
	}
	merge(p.cfg.Gateway[name])
	merge(p.cfg.Platforms[name])
	if gp, ok := p.cfg.Gateway["platforms"].(map[string]any); ok {
		merge(gp[name])
	}
	return out
}

func (p *hermesPlanner) channelValue(envVar, platform string, keys ...string) string {
	if v := p.lookup(envVar); v != "" {
		return v
	}
	blk := p.platformBlock(platform)
	for _, k := range keys {
		if s, ok := p.secret(str(blk[k])); ok && s != "" {
			return s
		}
	}
	return ""
}

func (p *hermesPlanner) allowList(platform string) []string {
	v := firstNonEmpty(p.lookup(strings.ToUpper(platform)+"_ALLOWED_USERS"), p.lookup("GATEWAY_ALLOWED_USERS"))
	return splitList(v)
}

func (p *hermesPlanner) channels() {
	type ch struct {
		platform string
		fields   map[string]any
		missing  string
	}
	var found []ch
	if tok := p.channelValue("TELEGRAM_BOT_TOKEN", "telegram", "token"); tok != "" {
		f := map[string]any{"bot_token": tok}
		if u := p.allowList("telegram"); len(u) > 0 {
			f["allowed_users"] = u
		}
		if c := splitList(p.lookup("TELEGRAM_GROUP_ALLOWED_CHATS")); len(c) > 0 {
			f["allowed_chats"] = c
		}
		found = append(found, ch{"telegram", f, ""})
	}
	if tok := p.channelValue("DISCORD_BOT_TOKEN", "discord", "token"); tok != "" {
		f := map[string]any{"bot_token": tok}
		if u := p.allowList("discord"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"discord", f, ""})
	}
	bot, app := p.channelValue("SLACK_BOT_TOKEN", "slack", "token"), p.channelValue("SLACK_APP_TOKEN", "slack", "app_token")
	if bot != "" || app != "" {
		f := map[string]any{}
		missing := ""
		if bot != "" {
			f["bot_token"] = bot
		} else {
			missing = "bot_token"
		}
		if app != "" {
			f["app_token"] = app
		} else {
			missing = "app_token"
		}
		if u := p.allowList("slack"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"slack", f, missing})
	}
	if hs := p.channelValue("MATRIX_HOMESERVER", "matrix", "homeserver"); hs != "" {
		f := map[string]any{"homeserver": hs}
		missing := ""
		if tok := p.channelValue("MATRIX_ACCESS_TOKEN", "matrix", "token", "access_token"); tok != "" {
			f["access_token"] = tok
		} else {
			missing = "access_token" // Hermes can log in with a password; Antares needs a token
		}
		if uid := p.lookup("MATRIX_USER_ID"); uid != "" {
			f["user_id"] = uid
		}
		if u := p.allowList("matrix"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"matrix", f, missing})
	}
	if acct := p.lookup("SIGNAL_ACCOUNT"); acct != "" {
		f := map[string]any{"number": acct}
		if u := p.lookup("SIGNAL_HTTP_URL"); u != "" {
			f["api_url"] = u
		}
		if u := p.allowList("signal"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"signal", f, ""})
	}
	if tok := p.lookup("WHATSAPP_CLOUD_ACCESS_TOKEN"); tok != "" || p.lookup("WHATSAPP_CLOUD_PHONE_NUMBER_ID") != "" {
		f := map[string]any{}
		missing := ""
		if tok != "" {
			f["token"] = tok
		} else {
			missing = "token"
		}
		if id := p.lookup("WHATSAPP_CLOUD_PHONE_NUMBER_ID"); id != "" {
			f["phone_number_id"] = id
		}
		if vt := p.lookup("WHATSAPP_CLOUD_VERIFY_TOKEN"); vt != "" {
			f["verify_token"] = vt
		}
		if u := p.allowList("whatsapp"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"whatsapp", f, missing})
	} else if strings.EqualFold(p.lookup("WHATSAPP_ENABLED"), "true") {
		p.add(UnsupportedItem(CatChannel, "whatsapp-bridge", "WhatsApp (linked device)", "device-bound WhatsApp login — Antares supports the Meta Cloud API only"))
	}
	if id, sec := p.lookup("FEISHU_APP_ID"), p.lookup("FEISHU_APP_SECRET"); id != "" {
		f := map[string]any{"app_id": id}
		missing := ""
		if sec != "" {
			f["app_secret"] = sec
		} else {
			missing = "app_secret"
		}
		if u := p.allowList("feishu"); len(u) > 0 {
			f["allowed_users"] = u
		}
		found = append(found, ch{"feishu", f, missing})
	}
	for _, c := range found {
		p.add(ChannelItem(p.env, c.platform, "", c.fields, c.missing))
	}
	for _, u := range []struct{ env, platform string }{
		{"MATTERMOST_TOKEN", "mattermost"}, {"EMAIL_ADDRESS", "email"}, {"TWILIO_ACCOUNT_SID", "sms"},
		{"DINGTALK_CLIENT_ID", "dingtalk"}, {"WECOM_BOT_ID", "wecom"}, {"WECOM_CALLBACK_CORP_ID", "wecom"},
		{"WEIXIN_TOKEN", "wechat"}, {"BLUEBUBBLES_SERVER_URL", "imessage"}, {"QQ_APP_ID", "qq"}, {"YUANBAO_APP_ID", "yuanbao"},
	} {
		if p.lookup(u.env) != "" && !p.hasItem(ItemID(CatChannel, u.platform)) {
			p.add(ChannelItem(p.env, u.platform, "", nil, ""))
		}
	}
}

func (p *hermesPlanner) hasItem(id string) bool {
	for _, it := range p.items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// ---- roles (custom personalities) ----------------------------------------------

func (p *hermesPlanner) roles() {
	for _, name := range sortedKeys(p.cfg.Agent.Personalities) {
		var prompt string
		switch v := p.cfg.Agent.Personalities[name].(type) {
		case string:
			prompt = v
		case map[string]any:
			prompt = str(v["system_prompt"])
			if t := str(v["tone"]); t != "" {
				prompt += "\n\nTone: " + t
			}
			if s := str(v["style"]); s != "" {
				prompt += "\n\nStyle: " + s
			}
		}
		summary := "Hermes personality"
		if name == p.cfg.Display.Personality {
			summary += " (active)"
		}
		p.add(RoleItem(p.env, RolePayload{Name: name, Title: humanTitle(name), Summary: summary, Prompt: strings.TrimSpace(prompt)}))
	}
}
