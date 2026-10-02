package migrate

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/roles"
)

// StateEnv is the real Env: it answers from the Antares config, the skill and
// role directories and the persona files under the Antares home. Build it
// with NewEnv right before planning so conflicts reflect the current state.
type StateEnv struct {
	cfg       *config.Config
	skillDirs []string
	roles     *roles.Registry
}

// NewEnv snapshots the Antares state from cfg (nil means load it).
func NewEnv(cfg *config.Config) *StateEnv {
	if cfg == nil {
		cfg = config.Get()
	}
	roleReg := roles.NewRegistry(RoleDirs(cfg))
	_ = roleReg.Reload()
	return &StateEnv{cfg: cfg, skillDirs: SkillDirs(cfg), roles: roleReg}
}

// SkillDirs are the configured user skill directories, expanded; the first is
// where imports are written. Falls back to <home>/skills.
func SkillDirs(cfg *config.Config) []string {
	var out []string
	for _, d := range cfg.Skills.Dirs {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, config.Expand(d))
		}
	}
	if len(out) == 0 {
		out = []string{config.Path("skills")}
	}
	return out
}

// RoleDirs are the configured role directories, expanded; the first is where
// imported roles are written. Falls back to <home>/roles.
func RoleDirs(cfg *config.Config) []string {
	var out []string
	for _, d := range cfg.Roles.Dirs {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, config.Expand(d))
		}
	}
	if len(out) == 0 {
		out = []string{config.Path("roles")}
	}
	return out
}

// ProviderExists reports a configured provider. A keyless placeholder entry
// (the defaults ship catalogue providers without keys) does not count: the
// import simply fills it in.
func (e *StateEnv) ProviderExists(id string) bool {
	p, ok := e.cfg.Providers[id]
	return ok && !providerPlaceholder(p)
}

func providerPlaceholder(p config.Provider) bool {
	envKey := ""
	if p.APIKeyEnv != "" {
		envKey = os.Getenv(strings.TrimSpace(p.APIKeyEnv))
	}
	return strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(envKey) == "" && len(p.Headers) == 0
}

// ProviderMatches implements ProviderMatcher.
func (e *StateEnv) ProviderMatches(id, baseURL, apiKey string) bool {
	p, ok := e.cfg.Providers[id]
	if !ok {
		return false
	}
	return sameURL(p.BaseURL, baseURL) && strings.TrimSpace(p.APIKey) == strings.TrimSpace(apiKey)
}

func sameURL(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

func (e *StateEnv) SkillExists(name string) bool {
	for _, d := range e.skillDirs {
		if IsDir(filepath.Join(d, name)) || IsFile(filepath.Join(d, name+".md")) {
			return true
		}
	}
	return false
}

func (e *StateEnv) MCPServerExists(name string) bool {
	_, ok := e.cfg.MCP.Servers[name]
	return ok
}

func (e *StateEnv) RoleExists(name string) bool {
	_, ok := e.roles.Get(name)
	return ok
}

func (e *StateEnv) ChannelConfigured(platform string) bool {
	return channelConfigured(e.cfg, platform)
}

func channelConfigured(cfg *config.Config, platform string) bool {
	g := cfg.Gateway
	nz := func(s ...string) bool {
		for _, v := range s {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
		return false
	}
	switch platform {
	case "telegram":
		return nz(g.Telegram.BotToken)
	case "discord":
		return nz(g.Discord.BotToken)
	case "slack":
		return nz(g.Slack.BotToken, g.Slack.AppToken)
	case "matrix":
		return nz(g.Matrix.AccessToken)
	case "signal":
		return nz(g.Signal.Number)
	case "whatsapp":
		return nz(g.WhatsApp.Token)
	case "feishu":
		return nz(g.Feishu.AppSecret)
	}
	return false
}

func (e *StateEnv) TextFileState(cat Category) string {
	return textFileState(cat)
}

func textFileState(cat Category) string {
	path := TextPath(cat)
	if path == "" {
		return "missing"
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "missing"
	}
	if cat == CatSoul && config.SoulIsUnset() {
		return "default"
	}
	return "custom"
}

// TextPath is the Antares file a text category writes.
func TextPath(cat Category) string {
	switch cat {
	case CatSoul:
		return config.SoulPath()
	case CatAgentsMD:
		return config.AgentsMDPath()
	case CatUserMD:
		return config.UserMDPath()
	}
	return ""
}

// HasDefaultModel implements DefaultModelChecker: a default model whose
// provider has a key, is local, or takes credentials from the environment.
func (e *StateEnv) HasDefaultModel() bool {
	return hasWorkingDefault(e.cfg)
}

func hasWorkingDefault(cfg *config.Config) bool {
	if strings.TrimSpace(cfg.Model.Default) == "" {
		return false
	}
	_, p := cfg.ResolveProvider(cfg.Model.Provider)
	return strings.TrimSpace(p.APIKey) != "" || IsLocalURL(p.BaseURL) || p.Kind == "bedrock" || len(p.Headers) > 0
}

// RAGEnabled implements RAGChecker.
func (e *StateEnv) RAGEnabled() bool { return e.cfg.RAG.Enabled }
