package agent

import (
	"strings"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

// antigravity renames tools that collide with Google built-ins on Sub2API's
// Antigravity transformer. The wire name is what the model sees; byName keeps
// a lookup for the real Antares tool so Execute still works.
const (
	wireWebSearchAlias = "search_web"
	nativeWebSearch    = "web_search"
)

// isAntigravityRoute reports whether this provider routes through Sub2API
// Antigravity (Claude or Gemini under /antigravity/…).
func isAntigravityRoute(providerID, baseURL string) bool {
	s := strings.ToLower(providerID + " " + baseURL)
	return strings.Contains(s, "antigravity")
}

// sanitizeToolsForProvider rewrites tool specs that upstream gateways mishandle.
// For Antigravity, a tool literally named web_search is treated as Google's
// built-in search and cannot be mixed with functionDeclarations — Sub2API
// returns 400. We rename it on the wire to search_web and map the alias back
// into byName so tool execution still hits the Antares web_search tool.
func sanitizeToolsForProvider(specs []llm.Tool, byName map[string]tools.Tool, providerID, baseURL string) ([]llm.Tool, map[string]tools.Tool) {
	if !isAntigravityRoute(providerID, baseURL) {
		return specs, byName
	}
	out := make([]llm.Tool, len(specs))
	copy(out, specs)
	// Copy byName so we do not mutate the caller's map unexpectedly.
	mapped := make(map[string]tools.Tool, len(byName)+1)
	for k, v := range byName {
		mapped[k] = v
	}
	for i := range out {
		if out[i].Name == nativeWebSearch {
			out[i].Name = wireWebSearchAlias
			if t, ok := byName[nativeWebSearch]; ok {
				mapped[wireWebSearchAlias] = t
			}
		}
	}
	return out, mapped
}
