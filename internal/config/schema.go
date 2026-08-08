package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Field describes one editable config leaf for the dashboard form builder.
type Field struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Group string `json:"group"`
	Type  string `json:"type"` // string|number|boolean|string[]|object
	// Tier drives disclosure in the dashboard: essential fields surface on the
	// landing view, common ones sit in their group, advanced ones stay folded
	// away. Most of the tree is advanced — it exists to be tunable, not read.
	Tier    string   `json:"tier"` // essential|common|advanced
	Default any      `json:"default"`
	Secret  bool     `json:"secret"`
	Enum    []string `json:"enum,omitempty"`
	Help    string   `json:"help,omitempty"`
}

// Tiers a field can belong to.
const (
	TierEssential = "essential"
	TierCommon    = "common"
	TierAdvanced  = "advanced"
)

// essential is the short list someone needs to get Antares answering at all.
var essential = map[string]bool{
	"model.default":     true,
	"model.provider":    true,
	"agent.workspace":   true,
	"database.driver":   true,
	"database.dsn":      true,
	"server.port":       true,
	"server.auth_token": true,
	"tools.toolset":     true,
	"rag.enabled":       true,
	"display.theme":     true,
}

// common holds the settings people actually revisit. Everything not listed
// here or above is treated as advanced.
var common = map[string]bool{
	"model.temperature":           true,
	"model.max_tokens":            true,
	"model.context_window":        true,
	"model.reasoning_effort":      true,
	"model.auxiliary":             true,
	"agent.max_turns":             true,
	"agent.personality":           true,
	"agent.system_prompt_extra":   true,
	"agent.timezone":              true,
	"tools.approval_mode":         true,
	"tools.web_search.provider":   true,
	"tools.web_search.api_key":    true,
	"terminal.backend":            true,
	"terminal.cwd":                true,
	"terminal.timeout":            true,
	"memory.memory_enabled":       true,
	"memory.user_profile_enabled": true,
	"rag.embed_model":             true,
	"rag.embed_provider":          true,
	"rag.rerank_mode":             true,
	"skills.enabled":              true,
	"skills.auto_create":          true,
	"cron.enabled":                true,
	"cron.timezone":               true,
	"gateway.enabled":             true,
	"gateway.telegram.enabled":    true,
	"gateway.discord.enabled":     true,
	"mcp.enabled":                 true,
	"compression.enabled":         true,
	"streaming.enabled":           true,
	"delegation.enabled":          true,
	"display.show_reasoning":            true,
	"display.tool_progress":             true,
	"display.max_live_reasoning_chars":  true,
	"logging.level":               true,
	"server.host":                 true,
}

func tierFor(path string) string {
	switch {
	case essential[path]:
		return TierEssential
	case common[path]:
		return TierCommon
	default:
		return TierAdvanced
	}
}

// hidden lists paths that must never appear in the raw config editor. The
// dashboard password is a bcrypt HASH, not a value to type: it has a dedicated
// "set password" card that hashes the plaintext. Exposing the raw field invites
// someone to paste a plaintext password straight into the hash, which then can
// never match a login (a non-bcrypt string fails every comparison).
var hidden = map[string]bool{
	"server.dashboard_password_hash": true,
}

var enums = map[string][]string{
	"database.driver":           {"sqlite", "postgres", "memory"},
	"rag.rerank_mode":           {"llm", "api", "off"},
	"terminal.backend":          {"local", "docker", "ssh"},
	"tools.approval_mode":       {"auto", "prompt", "deny"},
	"session_reset.mode":        {"never", "idle", "daily"},
	"display.theme":             {"system", "light", "dark"},
	"logging.level":             {"debug", "info", "warn", "error"},
	"agent.reasoning_effort":    {"none", "low", "medium", "high"},
	"model.reasoning_effort":    {"none", "low", "medium", "high"},
	"tools.web_search.provider": {"browser", "brave", "tavily", "searxng", "none"},
}

var help = map[string]string{
	"model.default":             "Model id as your provider spells it, e.g. anthropic/claude-sonnet-4.5.",
	"model.provider":            "Which entry under providers to call.",
	"model.auxiliary":           "Cheaper model used for summarising and other background work.",
	"model.context_window":      "Used to decide when to compact; set it to match your model.",
	"database.driver":           "sqlite for a single node, postgres when you share state.",
	"database.dsn":              "sqlite: a file path. postgres: postgres://user:pass@host:5432/db?sslmode=disable",
	"server.auth_token":         "Leave empty to keep the dashboard open — sensible behind a private network.",
	"server.host":               "0.0.0.0 exposes it on every interface; 127.0.0.1 keeps it local.",
	"agent.workspace":           "The only directory file tools may read or write.",
	"agent.system_prompt_extra": "Appended to the system prompt on every turn.",
	"tools.toolset":             "Preset deciding which tools reach the model.",
	"tools.approval_mode":       "auto runs mutating tools directly; deny blocks them.",
	"rag.rerank_mode":           "How to reorder results: llm (an auxiliary model scores them), api (an external reranker), or off.",
	"rag.embed_model":           "The embedding model for indexing and search, e.g. text-embedding-3-small.",
	"compression.threshold":     "Fraction of the context window that triggers automatic compaction.",
	"terminal.backend":          "local runs on this machine; docker and ssh sandbox it elsewhere.",
	"memory.memory_enabled":     "Lets the agent store durable facts between sessions.",
	"skills.auto_create":        "Allows the agent to write new skills on its own.",
	"osint.google_cookie":       "Optional. A logged-in Google Cookie header enables osint_google to resolve an email to its public profile. ToS-sensitive; uses your own session. Leave empty to disable.",
	"display.show_reasoning": "Stream and show model reasoning/thinking in the dashboard (and TUI). Off skips emitting reasoning events so long thinking traces never hit the UI.",
	"display.tool_progress":  "Show live tool progress lines while a tool runs.",
	"display.max_live_reasoning_chars": "Max characters of reasoning kept in the browser while a turn streams (trailing window). Prevents tab freezes on long thinking. Default 48000. 0 = unlimited. Full text is still saved server-side and restored after the turn.",
}

func secretKey(path string) bool {
	l := strings.ToLower(path)
	for _, s := range []string{"api_key", "token", "secret", "password", "dsn", "cookie", "proxy"} {
		if strings.Contains(l, s) {
			return l != "database.dsn" || strings.Contains(l, "password")
		}
	}
	return false
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	parts := strings.Fields(s)
	for i, p := range parts {
		switch strings.ToLower(p) {
		case "url", "dsn", "api", "ttl", "id", "ws", "mcp", "rag", "cwd", "ssh", "cors", "wal":
			parts[i] = strings.ToUpper(p)
		default:
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// Schema walks the default config and produces a flat, ordered field list.
func Schema() []Field {
	var out []Field
	walk(reflect.ValueOf(*Default()), "", "", &out)
	return out
}

func walk(v reflect.Value, prefix, group string, out *[]Field) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("yaml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		path := name
		grp := group
		fv := v.Field(i)
		if prefix != "" {
			path = prefix + "." + name
			if hidden[path] {
				continue
			}
		} else if fv.Kind() == reflect.Struct {
			// A struct at the root names its own group; a bare scalar does not.
			grp = name
		} else {
			grp = "general"
		}

		switch fv.Kind() {
		case reflect.Struct:
			walk(fv, path, grp, out)
			continue
		case reflect.Map:
			*out = append(*out, Field{
				Path: path, Label: humanize(name), Group: grp, Type: "object",
				Tier: tierFor(path), Default: fv.Interface(),
			})
			continue
		case reflect.Slice:
			*out = append(*out, Field{
				Path: path, Label: humanize(name), Group: grp, Type: "string[]",
				Tier: tierFor(path), Default: fv.Interface(), Help: help[path],
			})
			continue
		}

		f := Field{
			Path:    path,
			Label:   humanize(name),
			Group:   grp,
			Tier:    tierFor(path),
			Default: fv.Interface(),
			Secret:  secretKey(path),
			Enum:    enums[path],
			Help:    help[path],
		}
		switch fv.Kind() {
		case reflect.Bool:
			f.Type = "boolean"
		case reflect.Int, reflect.Int64, reflect.Float64:
			f.Type = "number"
		default:
			f.Type = "string"
		}
		*out = append(*out, f)
	}
}

// GetPath reads a dotted config path, e.g. "model.default".
func (c *Config) GetPath(path string) (any, error) {
	v, err := resolve(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return nil, err
	}
	return v.Interface(), nil
}

// SetPath writes a dotted config path, coercing the string form when needed.
func (c *Config) SetPath(path string, value any) error {
	v, err := resolve(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return err
	}
	if !v.CanSet() {
		return fmt.Errorf("config path %q is not writable", path)
	}
	return assign(v, value)
}

func resolve(v reflect.Value, path string) (reflect.Value, error) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		if cur.Kind() == reflect.Map {
			key := reflect.ValueOf(seg)
			got := cur.MapIndex(key)
			if !got.IsValid() {
				return reflect.Value{}, fmt.Errorf("unknown config key %q", seg)
			}
			// Maps are not addressable; callers use SetMapPath for these.
			cur = got
			continue
		}
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("config path %q is not a struct", seg)
		}
		found := false
		t := cur.Type()
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
			if name == seg {
				cur = cur.Field(i)
				found = true
				break
			}
		}
		if !found {
			return reflect.Value{}, fmt.Errorf("unknown config key %q", seg)
		}
	}
	return cur, nil
}

func assign(dst reflect.Value, value any) error {
	if value == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}
	rv := reflect.ValueOf(value)
	if rv.Type().AssignableTo(dst.Type()) {
		dst.Set(rv)
		return nil
	}
	if rv.Type().ConvertibleTo(dst.Type()) && dst.Kind() != reflect.Slice && dst.Kind() != reflect.Map {
		dst.Set(rv.Convert(dst.Type()))
		return nil
	}

	s := fmt.Sprint(value)
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("expected boolean, got %q", s)
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("expected number, got %q", s)
		}
		dst.SetInt(int64(n))
	case reflect.Float64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("expected number, got %q", s)
		}
		dst.SetFloat(n)
	case reflect.Slice, reflect.Map:
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out := reflect.New(dst.Type())
		if err := json.Unmarshal(b, out.Interface()); err != nil {
			return fmt.Errorf("expected %s: %w", dst.Type(), err)
		}
		dst.Set(out.Elem())
	default:
		return fmt.Errorf("unsupported config type %s", dst.Type())
	}
	return nil
}

// Redacted returns a deep copy with secret values masked, for API responses.
func (c *Config) Redacted() map[string]any {
	b, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	redactMap(m, "")
	return m
}

func redactMap(m map[string]any, prefix string) {
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch t := v.(type) {
		case map[string]any:
			redactMap(t, path)
		case []any:
			// Recurse into arrays of objects (e.g. proxies.entries) so nested
			// secrets like a proxy password are masked too. Elements inherit the
			// array's path, so secretKey("proxies.entries.password") still
			// matches on the "password" leaf.
			for _, el := range t {
				if mm, ok := el.(map[string]any); ok {
					redactMap(mm, path)
				}
			}
		case string:
			if t != "" && secretKey(path) {
				m[k] = maskSecret(t)
			}
		}
	}
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "••••••••"
	}
	return s[:4] + strings.Repeat("•", 8) + s[len(s)-4:]
}
