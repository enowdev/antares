package config

import (
	"os"
	"strings"
)

// AGENTS.md and USER.md sit beside SOUL.md and, like it, are global: one set
// for web, TUI and every gateway. AGENTS.md holds standing instructions
// ("always answer in Indonesian", "never push without asking"); USER.md holds
// facts about the user. Both start absent, with no placeholder.

// AgentsMDPath is the global instructions file.
func AgentsMDPath() string { return Path("AGENTS.md") }

// UserMDPath is the file of facts about the user.
func UserMDPath() string { return Path("USER.md") }

// LoadAgentsMD returns the global instructions, or "" when there are none.
func LoadAgentsMD() string { return readText(AgentsMDPath()) }

// LoadUserMD returns what Antares knows about the user, or "" when nothing.
func LoadUserMD() string { return readText(UserMDPath()) }

// SaveAgentsMD writes the global instructions; empty content removes the file.
func SaveAgentsMD(content string) error { return writeText(AgentsMDPath(), content) }

// SaveUserMD writes the facts about the user; empty content removes the file.
func SaveUserMD(content string) error { return writeText(UserMDPath(), content) }

func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeText(path, content string) error {
	body := strings.TrimSpace(content)
	if body == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := EnsureHome(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body+"\n"), 0o600)
}
