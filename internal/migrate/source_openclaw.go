package migrate

// OpenClaw (github.com/openclaw/openclaw). Formats verified against the
// project's source at package.json version 2026.9.7 (commit of 2026-10-02);
// paths below are relative to that repo.
//
//   - State dir: ~/.openclaw, $OPENCLAW_STATE_DIR, legacy ~/.clawdbot when
//     ~/.openclaw is absent (src/config/state-dir.ts:8-47); profiles
//     ~/.openclaw-<name> (src/cli/profile-utils.ts — naming UNVERIFIED).
//     Config: $OPENCLAW_CONFIG_PATH, else <state>/openclaw.json, else
//     clawdbot.json (src/config/paths.ts:229-340), JSON5
//     (src/config/io.read-helpers.ts). "$include" files are not followed.
//   - ${VAR} / ${VAR:-fallback} substitution, "$${" escape
//     (src/config/env-substitution.ts); env.vars and string keys directly
//     under env (types.openclaw.ts); SecretInput is a string or
//     {source: env|file|exec|store, provider, id} (src/secrets/ref-contract.ts).
//     env and file refs are resolved; exec and store refs become needs_input.
//   - models.providers.<id>{baseUrl, apiKey, api, headers, models[{id,name}]}
//     (src/config/zod-schema.core.ts:248-415, packages/llm-core/src/model-data.ts).
//   - agents.defaults.model string | {primary, fallbacks}, refs
//     "provider/model"; agents.entries{<id>:{name, workspace, model,
//     identity, default}} and legacy agents.list[{id,…}]
//     (src/config/zod-schema.agents.ts, src/config/legacy.roster.ts).
//   - Workspace: agent workspace, else defaults.workspace (legacy owner) or
//     <defaults.workspace>/<id>, else <state>/workspace[-<id>]
//     (src/agents/agent-scope-config.ts:462-486,
//     src/agents/workspace-default-path.ts). Bootstrap files SOUL.md,
//     IDENTITY.md, USER.md, AGENTS.md, BOOT.md, MEMORY.md (or memory.md),
//     daily notes memory/YYYY-MM-DD.md
//     (src/agents/workspace-bootstrap-policy.ts:10-35,
//     src/memory/root-memory-files.ts).
//   - Skills: <workspace>/skills/<name>/SKILL.md and <state>/skills
//     (docs/tools/skills.md:38-47).
//   - mcp.servers.<name>{enabled, command, args, env, url, transport
//     stdio|sse|streamable-http, headers, auth}
//     (src/config/zod-schema.mcp-server.ts).
//   - channels.<platform> (src/config/types.channels.ts, extensions/*/src/
//     config-schema.ts): telegram{botToken, tokenFile, allowFrom, groups},
//     discord{token, guilds, allowFrom}, slack{botToken, appToken},
//     matrix{homeserver, userId, accessToken}, signal{account, httpHost,
//     httpPort}, feishu{appId, appSecret, verificationToken}; whatsapp is a
//     Baileys linked-device session (unsupported).
//   - Auth profiles: <state>/agents/<id>/agent/openclaw-agent.sqlite table
//     auth_profile_store(store_key='primary', store_json) and the shared
//     <state>/state/openclaw.sqlite config_machine_state
//     ('authProfiles.store') (src/state/openclaw-agent-schema.sql:742-752,
//     src/agents/auth-profiles/sqlite.ts); legacy <agentDir>/auth-profiles.json.
//     store_json = {version, profiles{<id>:{type: api_key|token|oauth,
//     provider, key|keyRef|token|tokenRef,…}}} (credential-schema.ts). The
//     values are stored in plain text (no encryption found in the auth
//     profile code), so nothing is decrypted; the DB is opened read-only.
//   - Cron: legacy <state>/cron/jobs.json {version, jobs[]} and the shared
//     DB table cron_jobs(job_json) (src/cron/store/paths.ts,
//     src/state/openclaw-state-schema.sql:1467). Schedule kinds cron{expr,tz},
//     every{everyMs}, at, on-exit, stream; payload agentTurn{message} |
//     systemEvent{text} | command | script | heartbeat
//     (packages/gateway-protocol/src/schema/cron.ts:156-244).
//   - Running: launchd ai.openclaw.gateway (src/daemon/constants.ts:5),
//     process title openclaw-gateway / "openclaw gateway"
//     (src/entry.ts:215), lock files <state>/tmp/openclaw-<uid>/gateway.*.lock
//     JSON {pid, role} (src/infra/gateway-lock.ts:285-296).
//   - Version: meta.lastTouchedVersion in openclaw.json.
//
// UNVERIFIED: the global .env path (read from <state>/.env), profile dir
// naming, the exact bytes of real SQLite files (no install was inspected).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type openclawSource struct{}

func init() { Register(openclawSource{}) }

func (openclawSource) ID() string   { return "openclaw" }
func (openclawSource) Name() string { return "OpenClaw" }

func openclawDefaultRoot() string {
	if v := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")); v != "" {
		return ExpandHome(v)
	}
	def := filepath.Join(HomeDir(), ".openclaw")
	if !IsDir(def) && IsDir(filepath.Join(HomeDir(), ".clawdbot")) {
		return filepath.Join(HomeDir(), ".clawdbot")
	}
	return def
}

func openclawConfigPath(root string, useEnv bool) string {
	if useEnv {
		if v := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG_PATH")); v != "" {
			return ExpandHome(v)
		}
	}
	for _, n := range []string{"openclaw.json", "clawdbot.json"} {
		if p := filepath.Join(root, n); IsFile(p) {
			return p
		}
	}
	return filepath.Join(root, "openclaw.json")
}

func openclawInstalled(root string) bool {
	return IsDir(root) && (IsFile(filepath.Join(root, "openclaw.json")) || IsFile(filepath.Join(root, "clawdbot.json")) ||
		IsDir(filepath.Join(root, "workspace")) || IsDir(filepath.Join(root, "agents")))
}

func (s openclawSource) Detect(ctx context.Context, root string) (Detection, error) {
	all, err := s.DetectAll(ctx, root)
	if err != nil || len(all) == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Detection{}, err
	}
	return all[0], nil
}

// DetectAll finds the default state dir and, when looking at the default
// location, profile state dirs ~/.openclaw-<name>.
func (s openclawSource) DetectAll(ctx context.Context, root string) ([]Detection, error) {
	explicit := root != ""
	if !explicit {
		root = openclawDefaultRoot()
	}
	root = ExpandHome(root)
	var out []Detection
	if openclawInstalled(root) {
		out = append(out, s.detectOne(root, "", !explicit))
	}
	if !explicit {
		matches, _ := filepath.Glob(filepath.Join(HomeDir(), ".openclaw-*"))
		sort.Strings(matches)
		for _, m := range matches {
			name := strings.TrimPrefix(filepath.Base(m), ".openclaw-")
			if name != "" && openclawInstalled(m) {
				out = append(out, s.detectOne(m, name, false))
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s openclawSource) detectOne(root, profile string, useEnv bool) Detection {
	d := Detection{Source: s.ID(), Name: s.Name(), Root: root, Profile: profile}
	var meta struct {
		Meta struct {
			LastTouchedVersion string `json:"lastTouchedVersion"`
		} `json:"meta"`
	}
	if ReadJSON5(openclawConfigPath(root, useEnv), &meta) == nil {
		d.Version = meta.Meta.LastTouchedVersion
	}
	label := "ai.openclaw.gateway"
	if profile != "" {
		label = "ai.openclaw." + profile
	}
	d.Running = openclawLockAlive(root) ||
		RunningCheck([]string{"openclaw-gateway", "openclaw gateway"}, []string{label})
	// Summary from a plan without secrets resolution side effects: the plan
	// only reads files and opens the SQLite DBs read-only.
	if p, err := s.plan(d, nil, useEnv); err == nil {
		d.Summary = Summarize(p.Items)
	}
	return d
}

func openclawLockAlive(root string) bool {
	locks, _ := filepath.Glob(filepath.Join(root, "tmp", "openclaw-*", "gateway.*.lock"))
	for _, l := range locks {
		var rec struct {
			PID  int    `json:"pid"`
			Role string `json:"role"`
		}
		if ReadJSON(l, &rec) == nil && (rec.Role == "" || rec.Role == "gateway") && PIDAlive(rec.PID) {
			return true
		}
	}
	return false
}

func (s openclawSource) Plan(ctx context.Context, det Detection, env Env) (Plan, error) {
	return s.plan(det, env, det.Profile == "" && det.Root == openclawDefaultRoot())
}

// ---- config shape ---------------------------------------------------------------

type openclawConfig struct {
	Env     map[string]any `json:"env"`
	Secrets struct {
		Providers map[string]map[string]any `json:"providers"`
	} `json:"secrets"`
	Models struct {
		Providers map[string]map[string]any `json:"providers"`
	} `json:"models"`
	Agents struct {
		Defaults struct {
			Model     any    `json:"model"`
			Workspace string `json:"workspace"`
		} `json:"defaults"`
		Entries map[string]map[string]any `json:"entries"`
		List    []map[string]any          `json:"list"`
	} `json:"agents"`
	MCP struct {
		Servers map[string]map[string]any `json:"servers"`
	} `json:"mcp"`
	Channels map[string]map[string]any `json:"channels"`
	Include  any                       `json:"$include"`
}

type openclawAgent struct {
	ID        string
	Name      string
	Workspace string
	Model     any
	Default   bool
	Identity  map[string]any
}

type openclawPlanner struct {
	root    string
	det     Detection
	env     Env
	vars    map[string]string
	cfg     openclawConfig
	items   []Item
	warn    []string
	planned map[string]bool
	byName  map[string]string // OpenClaw provider id → Antares provider id
	auth    map[string]openclawCred
	oauth   map[string]bool
}

// openclawCred is one usable credential from an auth profile.
type openclawCred struct {
	value string
	from  string
	ok    bool
}

func (s openclawSource) plan(det Detection, env Env, useEnv bool) (Plan, error) {
	if !IsDir(det.Root) {
		return Plan{}, ErrNotFound
	}
	p := &openclawPlanner{root: det.Root, det: det, env: env, planned: map[string]bool{}, byName: map[string]string{},
		auth: map[string]openclawCred{}, oauth: map[string]bool{}}
	cfgPath := openclawConfigPath(det.Root, useEnv)
	if IsFile(cfgPath) {
		if err := ReadJSON5(cfgPath, &p.cfg); err != nil {
			p.warn = append(p.warn, filepath.Base(cfgPath)+" could not be read: "+err.Error())
		}
	}
	if p.cfg.Include != nil {
		p.warn = append(p.warn, "openclaw.json uses $include; settings in included files are not imported.")
	}
	p.vars = ReadDotEnv(filepath.Join(det.Root, ".env"))
	for k, v := range p.cfg.Env {
		switch x := v.(type) {
		case string:
			p.vars[k] = x
		case map[string]any:
			if k == "vars" {
				for vk, vv := range x {
					if s, ok := vv.(string); ok {
						p.vars[vk] = s
					}
				}
			}
		}
	}
	p.readAuth()
	p.providers()
	p.model()
	agents := p.agents()
	main := openclawMainAgent(agents)
	ws := p.workspace(main)
	p.persona(ws)
	p.memory(ws)
	p.skills(ws)
	p.mcp()
	p.cron()
	p.channels()
	p.roles(agents, main)
	return Plan{Detection: det, Items: p.items, Warnings: p.warn}, nil
}

func (p *openclawPlanner) add(it ...Item) { p.items = append(p.items, it...) }

// ---- secrets ----------------------------------------------------------------------

var openclawVarRE = regexp.MustCompile(`\$\$\{|\$\{([A-Z_][A-Z0-9_]*)(:-([^}]*))?\}`)

// subst applies OpenClaw's ${VAR} / ${VAR:-fallback} substitution; ok=false
// when a referenced variable has no value and no fallback.
func (p *openclawPlanner) subst(s string) (string, bool) {
	ok := true
	out := openclawVarRE.ReplaceAllStringFunc(s, func(m string) string {
		if m == "$${" {
			return "${"
		}
		sub := openclawVarRE.FindStringSubmatch(m)
		if v := p.lookupVar(sub[1]); v != "" {
			return v
		}
		if sub[2] != "" {
			return sub[3]
		}
		ok = false
		return ""
	})
	return out, ok
}

func (p *openclawPlanner) lookupVar(name string) string {
	if v := p.vars[name]; v != "" {
		return v
	}
	return os.Getenv(name)
}

var openclawBareVarRE = regexp.MustCompile(`^\$([A-Z_][A-Z0-9_]*)$`)

// secret resolves a SecretInput. The reason explains a failure (never the
// value).
func (p *openclawPlanner) secret(v any) (string, string) {
	switch x := v.(type) {
	case nil:
		return "", ""
	case string:
		if m := openclawBareVarRE.FindStringSubmatch(x); m != nil {
			if val := p.lookupVar(m[1]); val != "" {
				return val, ""
			}
			return "", "environment variable " + m[1] + " is not set"
		}
		val, ok := p.subst(x)
		if !ok {
			return "", "a ${…} reference could not be resolved"
		}
		return val, ""
	case float64:
		return str(x), ""
	case map[string]any:
		source, id := str(x["source"]), str(x["id"])
		switch source {
		case "env":
			if val := p.lookupVar(id); val != "" {
				return val, ""
			}
			return "", "environment variable " + id + " is not set"
		case "file":
			prov := p.cfg.Secrets.Providers[firstNonEmpty(str(x["provider"]), "default")]
			path := ExpandHome(str(prov["path"]))
			if path == "" {
				return "", "secret file provider has no path"
			}
			return openclawFileSecret(path, id)
		case "exec":
			return "", "kept by a command (exec secret); enter it here"
		case "store":
			return "", "kept in OpenClaw's secret store; enter it here"
		}
		return "", "unknown secret reference"
	}
	return "", ""
}

// openclawFileSecret reads a file secret: id "value" is the whole file,
// otherwise a JSON pointer into it.
func openclawFileSecret(path, id string) (string, string) {
	if id == "" || id == "value" {
		if v := ReadText(path); v != "" {
			return v, ""
		}
		return "", "secret file is missing or empty"
	}
	var doc any
	if err := ReadJSON(path, &doc); err != nil {
		return "", "secret file is not JSON"
	}
	for _, tok := range strings.Split(strings.TrimPrefix(id, "/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		m, ok := doc.(map[string]any)
		if !ok {
			return "", "secret not found in file"
		}
		doc = m[tok]
	}
	if s := str(doc); s != "" {
		return s, ""
	}
	return "", "secret not found in file"
}

// ---- auth profiles ----------------------------------------------------------------

type openclawAuthStore struct {
	Profiles map[string]map[string]any `json:"profiles"`
}

// readAuth gathers credentials from every agent's auth store (SQLite, then
// legacy JSON) and the shared store. It never writes; failures only mean the
// affected providers become needs_input.
func (p *openclawPlanner) readAuth() {
	var stores []openclawAuthStore
	agentDirs, _ := filepath.Glob(filepath.Join(p.root, "agents", "*", "agent"))
	sort.Strings(agentDirs)
	for _, dir := range agentDirs {
		if js := openclawSQLiteValue(filepath.Join(dir, "openclaw-agent.sqlite"),
			`SELECT store_json FROM auth_profile_store WHERE store_key = 'primary'`); js != "" {
			var st openclawAuthStore
			if json.Unmarshal([]byte(js), &st) == nil {
				stores = append(stores, st)
			}
		}
		var legacy openclawAuthStore
		if ReadJSON(filepath.Join(dir, "auth-profiles.json"), &legacy) == nil {
			stores = append(stores, legacy)
		}
	}
	if js := openclawSQLiteValue(filepath.Join(p.root, "state", "openclaw.sqlite"),
		`SELECT value_json FROM config_machine_state WHERE state_key = 'authProfiles.store'`); js != "" {
		var st openclawAuthStore
		if json.Unmarshal([]byte(js), &st) == nil {
			stores = append(stores, st)
		}
	}
	for _, st := range stores {
		for _, id := range sortedKeys(st.Profiles) {
			prof := st.Profiles[id]
			prov := firstNonEmpty(str(prof["provider"]), strings.SplitN(id, ":", 2)[0])
			if prov == "" {
				continue
			}
			if _, have := p.auth[prov]; have {
				continue
			}
			switch str(prof["type"]) {
			case "api_key":
				val, reason := p.secret(firstNonNil(prof["key"], prof["keyRef"]))
				p.auth[prov] = openclawCred{value: val, from: "auth profile " + id, ok: val != "" && reason == ""}
			case "token":
				val, reason := p.secret(firstNonNil(prof["token"], prof["tokenRef"]))
				p.auth[prov] = openclawCred{value: val, from: "auth profile " + id, ok: val != "" && reason == ""}
			case "oauth":
				p.oauth[prov] = true
			}
		}
	}
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

// openclawSQLiteValue runs a one-value query against a SQLite file opened
// read-only. Any failure (missing file, locked, schema drift) yields "".
func openclawSQLiteValue(path, query string) string {
	if !IsFile(path) {
		return ""
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return ""
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var v sql.NullString
	if err := db.QueryRowContext(ctx, query).Scan(&v); err != nil {
		return ""
	}
	return v.String
}

func openclawSQLiteRows(path, query string) []string {
	if !IsFile(path) {
		return nil
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if rows.Scan(&v) == nil && v.String != "" {
			out = append(out, v.String)
		}
	}
	return out
}

// ---- providers & model --------------------------------------------------------

// openclawKind maps an OpenClaw "api" to an Antares provider kind; oauth
// marks one that has no key-based equivalent.
func openclawKind(api string) (kind string, oauth bool) {
	switch api {
	case "anthropic-messages":
		return "anthropic", false
	case "google-generative-ai", "google-interactions":
		return "gemini", false
	case "google-vertex":
		return "vertex", false
	case "bedrock-converse-stream":
		return "bedrock", false
	case "azure-openai-responses":
		return "azure", false
	case "github-copilot", "openai-chatgpt-responses":
		return "", true
	}
	return "openai-compatible", false
}

func (p *openclawPlanner) taken(id string) bool { return p.planned[id] }

func (p *openclawPlanner) addProvider(pp ProviderPayload, ocID, detail, reason string) {
	p.byName[ocID] = pp.ID
	if p.planned[pp.ID] {
		return
	}
	p.planned[pp.ID] = true
	it := ProviderItem(p.env, pp, detail)
	if reason != "" && it.Status == StatusNeedsInput {
		it.Reason = reason
	}
	p.add(it)
}

func (p *openclawPlanner) providers() {
	for _, id := range sortedKeys(p.cfg.Models.Providers) {
		e := p.cfg.Models.Providers[id]
		baseURL, _ := p.subst(str(e["baseUrl"]))
		key, reason := p.secret(e["apiKey"])
		detail := "from openclaw.json"
		if key == "" {
			if c, ok := p.auth[id]; ok && c.ok {
				key, detail, reason = c.value, "key from "+c.from, ""
			}
		}
		var headers map[string]string
		if h, ok := e["headers"].(map[string]any); ok {
			headers = map[string]string{}
			for k, v := range h {
				if s, r := p.secret(v); r == "" {
					headers[k] = s
				}
			}
		}
		var models []string
		if ms, ok := e["models"].([]any); ok {
			for _, m := range ms {
				if mm, ok := m.(map[string]any); ok {
					if mid := str(mm["id"]); mid != "" {
						models = append(models, mid)
					}
				}
			}
		}
		api := str(e["api"])
		kind, oauth := openclawKind(api)
		if oauth || str(e["auth"]) == "oauth" {
			p.byName[id] = ""
			p.add(OAuthProviderItem(Slug(id)+"-login", id+" login"))
			continue
		}
		if api == "ollama" && baseURL != "" && !strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
			baseURL = strings.TrimRight(baseURL, "/") + "/v1" // Ollama's OpenAI-compatible path
		}
		if v, ok := Vendor(id); ok && !v.OAuth && (baseURL == "" || sameURL(baseURL, v.BaseURL) || hostLabel(baseURL) == hostLabel(v.BaseURL)) {
			p.addProvider(ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: firstNonEmpty(baseURL, v.BaseURL),
				APIKey: key, Headers: headers, Models: models}, id, detail, reason)
			continue
		}
		if baseURL == "" {
			p.add(UnsupportedItem(CatProvider, Slug(id), id, "no base URL and no matching Antares provider"))
			continue
		}
		p.addProvider(ProviderPayload{ID: CustomProviderID(p.taken, id), Label: id, Kind: kind, BaseURL: baseURL,
			APIKey: key, Headers: headers, Models: models}, id, detail, reason)
	}
	// Providers known only from auth profiles (e.g. anthropic:default).
	for _, prov := range sortedKeys(p.auth) {
		if _, have := p.byName[prov]; have {
			continue
		}
		v, ok := Vendor(prov)
		if !ok || v.OAuth {
			continue
		}
		c := p.auth[prov]
		reason := ""
		if !c.ok {
			reason = "the key in " + c.from + " could not be resolved; enter it here"
		}
		p.addProvider(ProviderPayload{ID: v.ID, Label: v.Label, Kind: v.Kind, BaseURL: v.BaseURL, APIKey: c.value},
			prov, "key from "+c.from, reason)
	}
	for _, prov := range sortedKeys(p.oauth) {
		if _, have := p.byName[prov]; have {
			continue
		}
		label := prov
		if v, ok := Vendor(prov); ok {
			label = v.Label
		}
		p.add(OAuthProviderItem(Slug(prov)+"-login", label+" login"))
	}
}

// ref maps an OpenClaw "provider/model" ref to Antares (provider id, model).
func (p *openclawPlanner) ref(r string) (string, string) {
	prov, model, ok := strings.Cut(strings.TrimSpace(r), "/")
	if !ok {
		return "", r
	}
	if id, ok := p.byName[prov]; ok {
		return id, model
	}
	if v, ok := Vendor(prov); ok && !v.OAuth {
		return v.ID, model
	}
	return "", model
}

func openclawModelRefs(v any) (string, []string) {
	switch m := v.(type) {
	case string:
		return m, nil
	case map[string]any:
		return str(m["primary"]), strList(m["fallbacks"])
	}
	return "", nil
}

func (p *openclawPlanner) model() {
	primary, fallbacks := openclawModelRefs(p.cfg.Agents.Defaults.Model)
	if primary == "" {
		return
	}
	prov, model := p.ref(primary)
	mp := ModelPayload{Provider: prov, Model: model}
	for _, f := range fallbacks {
		fp, fm := p.ref(f)
		if fp != "" {
			mp.Fallback = append(mp.Fallback, fp+"/"+fm)
		} else {
			mp.Fallback = append(mp.Fallback, fm)
		}
	}
	p.add(ModelItem(p.env, mp))
}

// ---- agents, workspace, persona -------------------------------------------------

func (p *openclawPlanner) agents() []openclawAgent {
	var out []openclawAgent
	conv := func(id string, e map[string]any) openclawAgent {
		a := openclawAgent{ID: id, Name: str(e["name"]), Workspace: str(e["workspace"]), Model: e["model"]}
		a.Default, _ = e["default"].(bool)
		a.Identity, _ = e["identity"].(map[string]any)
		return a
	}
	for _, id := range sortedKeys(p.cfg.Agents.Entries) {
		out = append(out, conv(id, p.cfg.Agents.Entries[id]))
	}
	for _, e := range p.cfg.Agents.List {
		if id := str(e["id"]); id != "" && p.cfg.Agents.Entries[id] == nil {
			out = append(out, conv(id, e))
		}
	}
	return out
}

// openclawMainAgent is the default agent: the one marked default, else
// "main", else the first; nil when no roster exists.
func openclawMainAgent(as []openclawAgent) *openclawAgent {
	for i := range as {
		if as[i].Default {
			return &as[i]
		}
	}
	for i := range as {
		if as[i].ID == "main" {
			return &as[i]
		}
	}
	if len(as) > 0 {
		return &as[0]
	}
	return nil
}

// workspace resolves an agent's workspace per agent-scope-config.ts.
func (p *openclawPlanner) workspace(a *openclawAgent) string {
	if a != nil && a.Workspace != "" {
		return ExpandHome(a.Workspace)
	}
	def := ExpandHome(p.cfg.Agents.Defaults.Workspace)
	isMain := a == nil || a.Default || a.ID == "main"
	switch {
	case isMain && def != "":
		return def
	case isMain:
		if v := strings.TrimSpace(os.Getenv("OPENCLAW_WORKSPACE_DIR")); v != "" && p.det.Profile == "" {
			return ExpandHome(v)
		}
		return filepath.Join(p.root, "workspace")
	case def != "":
		return filepath.Join(def, a.ID)
	}
	return filepath.Join(p.root, "workspace-"+a.ID)
}

func joinFiles(dir string, names ...string) (string, string) {
	var parts, from []string
	for _, n := range names {
		if t := ReadText(filepath.Join(dir, n)); t != "" {
			parts = append(parts, t)
			from = append(from, n)
		}
	}
	return strings.Join(parts, "\n\n"), strings.Join(from, " + ")
}

func (p *openclawPlanner) persona(ws string) {
	if text, from := joinFiles(ws, "SOUL.md", "IDENTITY.md"); text != "" {
		if it, ok := TextItem(p.env, CatSoul, text, from); ok {
			p.add(it)
		}
	}
	if text, from := joinFiles(ws, "AGENTS.md", "BOOT.md", "TOOLS.md"); text != "" {
		if it, ok := TextItem(p.env, CatAgentsMD, text, from); ok {
			p.add(it)
		}
	}
	if it, ok := TextItem(p.env, CatUserMD, ReadText(filepath.Join(ws, "USER.md")), "USER.md"); ok {
		p.add(it)
	}
}

var openclawDailyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}.*\.md$`)

func (p *openclawPlanner) memory(ws string) {
	text := ReadText(filepath.Join(ws, "MEMORY.md"))
	if text == "" {
		text = ReadText(filepath.Join(ws, "memory.md"))
	}
	p.add(MemoryItems(SplitMemory(text, ""), "global", "openclaw")...)
	entries, _ := os.ReadDir(filepath.Join(ws, "memory"))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		if !openclawDailyRE.MatchString(e.Name()) {
			continue
		}
		if it, ok := KnowledgeItem(p.env, "memory/"+e.Name(), ReadText(filepath.Join(ws, "memory", e.Name()))); ok {
			p.add(it)
		}
	}
}

func (p *openclawPlanner) skills(ws string) {
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Join(ws, "skills"), filepath.Join(p.root, "skills")} {
		for _, sd := range ScanSkills(dir, 2) {
			if seen[sd.Name] {
				continue // workspace skills take precedence
			}
			seen[sd.Name] = true
			p.add(SkillItem(p.env, sd))
		}
	}
}

// ---- MCP --------------------------------------------------------------------------

func (p *openclawPlanner) mcp() {
	for _, name := range sortedKeys(p.cfg.MCP.Servers) {
		e := p.cfg.MCP.Servers[name]
		key := Slug(name)
		if key == "" {
			continue
		}
		if b, ok := e["enabled"].(bool); ok && !b {
			p.add(UnsupportedItem(CatMCP, key, name, "disabled in OpenClaw"))
			continue
		}
		if str(e["auth"]) == "oauth" {
			p.add(UnsupportedItem(CatMCP, key, name, "OAuth MCP login — connect it again in Antares"))
			continue
		}
		u, _ := p.subst(str(e["url"]))
		m := MCPPayload{Name: key, Transport: str(e["transport"]), Command: str(e["command"]), URL: u,
			Env: p.anyMap(e["env"]), Headers: p.anyMap(e["headers"])}
		for _, a := range strList(e["args"]) {
			if s, ok := p.subst(a); ok {
				a = s
			}
			m.Args = append(m.Args, a)
		}
		p.add(MCPItem(p.env, m))
	}
}

func (p *openclawPlanner) anyMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		if s, reason := p.secret(x); reason == "" && s != "" {
			out[k] = s
		} else {
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}

// ---- cron -------------------------------------------------------------------------

func (p *openclawPlanner) cron() {
	var raw []json.RawMessage
	var legacy struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if ReadJSON(filepath.Join(p.root, "cron", "jobs.json"), &legacy) == nil {
		raw = append(raw, legacy.Jobs...)
	}
	for _, js := range openclawSQLiteRows(filepath.Join(p.root, "state", "openclaw.sqlite"),
		`SELECT job_json FROM cron_jobs ORDER BY sort_order, job_id`) {
		raw = append(raw, json.RawMessage(js))
	}
	seen := map[string]bool{}
	for i, r := range raw {
		var j struct {
			ID       string         `json:"id"`
			Name     string         `json:"name"`
			Enabled  *bool          `json:"enabled"`
			Schedule any            `json:"schedule"`
			Payload  map[string]any `json:"payload"`
		}
		if json.Unmarshal(r, &j) != nil {
			continue
		}
		key := Slug(j.ID)
		if key == "" {
			key = strconv.Itoa(i + 1)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := str(j.Payload["kind"])
		if kind == "heartbeat" {
			continue // gateway-owned monitor, not the user's
		}
		prompt := firstStr(j.Payload, "message", "text")
		name := firstNonEmpty(j.Name, Truncate(firstLine(prompt), 60), "OpenClaw job "+key)
		if kind == "command" || kind == "script" {
			p.add(UnsupportedItem(CatCron, key, name, kind+" job — Antares schedules run prompts"))
			continue
		}
		sched, tz, reason := openclawSchedule(j.Schedule)
		if reason != "" {
			p.add(UnsupportedItem(CatCron, key, name, reason))
			continue
		}
		enabled := j.Enabled == nil || *j.Enabled
		p.add(CronItem(key, CronPayload{Name: name, Schedule: sched, Prompt: prompt, Timezone: tz}, enabled))
	}
}

func openclawSchedule(v any) (sched, tz, reason string) {
	switch s := v.(type) {
	case string:
		return s, "", "" // legacy string form: a cron expression
	case map[string]any:
		switch str(s["kind"]) {
		case "cron":
			return str(s["expr"]), str(s["tz"]), ""
		case "every":
			ms, _ := strconv.ParseFloat(str(s["everyMs"]), 64)
			if ms <= 0 {
				return "", "", "interval has no length"
			}
			return EverySchedule(time.Duration(ms) * time.Millisecond), "", ""
		case "at":
			return "", "", "one-shot schedule"
		case "on-exit", "stream":
			return "", "", "command-triggered schedule"
		}
	}
	return "", "", "unknown schedule kind"
}

// ---- channels -------------------------------------------------------------------

func (p *openclawPlanner) channels() {
	for _, name := range sortedKeys(p.cfg.Channels) {
		c := p.cfg.Channels[name]
		switch name {
		case "telegram":
			f := map[string]any{}
			missing := ""
			tok, reason := p.secret(c["botToken"])
			if tok == "" && str(c["tokenFile"]) != "" {
				tok = ReadText(ExpandHome(str(c["tokenFile"])))
				reason = ""
			}
			if tok != "" {
				f["bot_token"] = tok
			} else {
				missing = "bot_token"
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			if g, ok := c["groups"].(map[string]any); ok && len(g) > 0 {
				f["allowed_chats"] = sortedKeys(g)
			}
			p.channel("telegram", f, missing, reason)
		case "discord":
			f := map[string]any{}
			missing := ""
			tok, reason := p.secret(c["token"])
			if tok != "" {
				f["bot_token"] = tok
			} else {
				missing = "bot_token"
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			if g, ok := c["guilds"].(map[string]any); ok && len(g) > 0 {
				f["allowed_guilds"] = sortedKeys(g)
			}
			p.channel("discord", f, missing, reason)
		case "slack":
			f := map[string]any{}
			missing, reason := "", ""
			if v, r := p.secret(c["botToken"]); v != "" {
				f["bot_token"] = v
			} else {
				missing, reason = "bot_token", r
			}
			if v, r := p.secret(c["appToken"]); v != "" {
				f["app_token"] = v
			} else if missing == "" {
				missing, reason = "app_token", r
				if reason == "" && str(c["mode"]) != "socket" && str(c["mode"]) != "" {
					reason = "OpenClaw used Slack " + str(c["mode"]) + " mode; Antares needs a Socket Mode app token"
				}
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			p.channel("slack", f, missing, reason)
		case "matrix":
			f := map[string]any{}
			missing, reason := "", ""
			if hs := str(c["homeserver"]); hs != "" {
				f["homeserver"] = hs
			}
			if uid := str(c["userId"]); uid != "" {
				f["user_id"] = uid
			}
			if v, r := p.secret(c["accessToken"]); v != "" {
				f["access_token"] = v
			} else {
				missing, reason = "access_token", r
				if reason == "" {
					reason = "OpenClaw logs in with a password; Antares needs an access token"
				}
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			p.channel("matrix", f, missing, reason)
		case "signal":
			f := map[string]any{}
			missing := ""
			if a := str(c["account"]); a != "" {
				f["number"] = a
			} else {
				missing = "number"
			}
			if h := str(c["httpHost"]); h != "" {
				port := firstNonEmpty(str(c["httpPort"]), "8080")
				f["api_url"] = "http://" + h + ":" + port
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			p.channel("signal", f, missing, "")
		case "feishu":
			f := map[string]any{}
			missing, reason := "", ""
			if id := str(c["appId"]); id != "" {
				f["app_id"] = id
			}
			if v, r := p.secret(c["appSecret"]); v != "" {
				f["app_secret"] = v
			} else {
				missing, reason = "app_secret", r
			}
			if v, _ := p.secret(c["verificationToken"]); v != "" {
				f["verify_token"] = v
			}
			if u := strList(c["allowFrom"]); len(u) > 0 {
				f["allowed_users"] = u
			}
			p.channel("feishu", f, missing, reason)
		case "whatsapp":
			p.add(UnsupportedItem(CatChannel, "whatsapp-web", "WhatsApp (linked device)", "device-bound WhatsApp login — Antares supports the Meta Cloud API only"))
		default:
			p.add(ChannelItem(p.env, openclawPlatform(name), "", nil, ""))
		}
	}
}

func openclawPlatform(name string) string {
	switch name {
	case "msteams":
		return "teams"
	}
	return name
}

func (p *openclawPlanner) channel(platform string, f map[string]any, missing, reason string) {
	it := ChannelItem(p.env, platform, "", f, missing)
	if it.Status == StatusNeedsInput && reason != "" {
		it.Reason = reason
	}
	p.add(it)
}

// ---- roles (extra agents) -----------------------------------------------------

func (p *openclawPlanner) roles(agents []openclawAgent, main *openclawAgent) {
	for i := range agents {
		a := &agents[i]
		if main != nil && a.ID == main.ID {
			continue
		}
		ws := p.workspace(a)
		prompt, _ := joinFiles(ws, "SOUL.md", "IDENTITY.md", "AGENTS.md")
		title := firstNonEmpty(a.Name, str(a.Identity["name"]), humanTitle(a.ID))
		model := ""
		if primary, _ := openclawModelRefs(a.Model); primary != "" {
			if prov, m := p.ref(primary); prov != "" {
				model = prov + "/" + m
			} else {
				model = m
			}
		}
		p.add(RoleItem(p.env, RolePayload{Name: a.ID, Title: title, Summary: "OpenClaw agent " + a.ID, Prompt: prompt, Model: model}))
	}
}
