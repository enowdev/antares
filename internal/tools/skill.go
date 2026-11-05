package tools

import (
	"context"
	"fmt"
	"strings"
)

// skillTool lets the agent read a stored procedure and record new ones it
// learned, which is what closes the learning loop.
type skillTool struct{}

func (skillTool) Name() string { return "skill" }
func (skillTool) Description() string {
	return "Work with your skill library. `list` shows your everyday skills, `search` finds one by keyword " +
		"across the whole library including thousands of security testing procedures, `read` loads a procedure " +
		"before following it, `chains` shows which follow-on techniques compound with a skill, and `save` records " +
		"a reusable procedure you just worked out. Search for the technique you need — 'jwt', 'ssrf', 'kerberos' — " +
		"or filter security skills by cwe (e.g. 'CWE-89') or tech ('web', 'api', 'cloud'), then read it."
}
func (skillTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":      propEnum("What to do.", "list", "search", "read", "chains", "save"),
		"name":        prop("string", "Skill name to read, chain, or save; or the keywords to search for."),
		"cwe":         prop("string", "For search: filter to a CWE id, e.g. CWE-89 or 89."),
		"tech":        prop("string", "For search: filter by tech stack, e.g. web, api, cloud, mobile."),
		"category":    prop("string", "For search: filter by category, e.g. web-application."),
		"description": prop("string", "For save: one line describing when this skill applies."),
		"body":        prop("string", "For save: the procedure itself, in Markdown. Be concrete — exact commands, paths, and gotchas."),
		"tags":        map[string]any{"type": "array", "description": "Optional tags.", "items": map[string]any{"type": "string"}},
	}, "action")
}

func (skillTool) RequiresApproval() bool { return false }

func (skillTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Action      string   `json:"action"`
		Name        string   `json:"name"`
		CWE         string   `json:"cwe"`
		Tech        string   `json:"tech"`
		Category    string   `json:"category"`
		Description string   `json:"description"`
		Body        string   `json:"body"`
		Tags        []string `json:"tags"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Skills == nil {
		return Errorf("the skill library is not available")
	}
	lib := in.Deps.Skills

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list":
		items := lib.List()
		if len(items) == 0 {
			return Text("No skills stored yet. Use action=save once you work out something reusable.")
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d skill(s):\n\n", len(items))
		for _, s := range items {
			status := ""
			if !s.Enabled {
				status = " [disabled]"
			}
			fmt.Fprintf(&b, "- %s%s: %s\n", s.Name, status, s.Description)
		}
		return Text(b.String())

	case "search", "find":
		q := strings.TrimSpace(args.Name)
		if q == "" {
			q = strings.TrimSpace(args.Description)
		}
		cwe, tech, cat := strings.TrimSpace(args.CWE), strings.TrimSpace(args.Tech), strings.TrimSpace(args.Category)
		hits := lib.SearchFiltered(q, cwe, tech, cat, 30)
		if len(hits) == 0 {
			return Text(fmt.Sprintf("No skills match %q%s.", q, filterNote(cwe, tech, cat)))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d skill(s) matching %q%s:\n\n", len(hits), q, filterNote(cwe, tech, cat))
		for _, s := range hits {
			meta := ""
			if len(s.CWEIDs) > 0 {
				meta = " [" + strings.Join(s.CWEIDs, ", ") + "]"
			}
			fmt.Fprintf(&b, "- %s%s: %s\n", s.Name, meta, s.Description)
		}
		b.WriteString("\nLoad one with action=read, or see follow-on techniques with action=chains.")
		return Text(b.String())

	case "chains":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return Errorf("name is required to show a skill's chains")
		}
		hits := lib.Chains(name)
		if len(hits) == 0 {
			return Text(fmt.Sprintf("No follow-on techniques are recorded for %q.", name))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Techniques that chain from %q:\n\n", name)
		for _, s := range hits {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
		b.WriteString("\nRead one with action=read.")
		return Text(b.String())

	case "read", "get":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return Errorf("name is required when reading a skill")
		}
		info, body, ok := lib.Read(name)
		if !ok {
			return Errorf("skill %q not found. Use action=list to see what is available.", name)
		}
		lib.MarkUsed(name)
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n%s\n\n", info.Name, info.Description)
		if len(info.Triggers) > 0 {
			fmt.Fprintf(&b, "Use when: %s\n\n", strings.Join(info.Triggers, "; "))
		}
		b.WriteString(body)
		return Text(b.String())

	case "save", "write", "create":
		name := strings.TrimSpace(args.Name)
		body := strings.TrimSpace(args.Body)
		if name == "" || body == "" {
			return Errorf("both name and body are required when saving a skill")
		}
		if len(body) < 40 {
			return Errorf("the skill body is too thin to be useful — write the actual steps, commands, and pitfalls")
		}
		if err := lib.Write(name, args.Description, body, args.Tags); err != nil {
			return Errorf("save failed: %v", err)
		}
		return Text(fmt.Sprintf("Saved skill %q.", name))

	default:
		return Errorf("unknown action %q (want list, search, read, chains, or save)", args.Action)
	}
}

// filterNote renders the active search filters for a message, e.g. " (CWE-89, tech=web)".
func filterNote(cwe, tech, cat string) string {
	var parts []string
	if cwe != "" {
		parts = append(parts, cwe)
	}
	if tech != "" {
		parts = append(parts, "tech="+tech)
	}
	if cat != "" {
		parts = append(parts, "category="+cat)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
