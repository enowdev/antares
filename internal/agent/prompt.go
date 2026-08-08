package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/version"
)

// buildSystemPrompt assembles identity, environment, memory, and tool guidance.
func (a *Agent) buildSystemPrompt(ctx context.Context, req Request, sess *store.Session, active []tools.Tool) string {
	cfg := a.cfg
	var b strings.Builder

	b.WriteString("You are ")
	b.WriteString(version.Display)
	b.WriteString(", an autonomous AI agent that works on the user's behalf on their own machine.\n\n")

	// The soul is the agent's chosen identity — name, persona, voice — set by the
	// user (often via the first-conversation interview). It comes right after the
	// base line so it colours everything below. When it is still unset, the agent
	// is told to run that interview before getting to work.
	soul := config.Soul()
	b.WriteString("## Who you are\n\n")
	b.WriteString(soul)
	b.WriteString("\n")
	if config.SoulIsUnset() && req.Platform != "subagent" {
		b.WriteString(`
You have not been given an identity yet. Before anything else this conversation,
introduce yourself warmly and briefly — you have just "woken up" here and are
curious. Ask the user, in a light and friendly way (a few questions, not an
interrogation): what should they call you? who are they / what should you call
them? how do they want you to talk (concise, detailed, formal, casual)? any
personality, quirks, or principles they'd like you to have? Keep it short and
cheerful. Once they have answered enough, call the set_soul tool to record it,
then confirm your new name and carry on with whatever they actually asked. If the
user clearly just wants to get straight to a task, offer to set this up later and
help them now — do not block them.
`)
	}

	b.WriteString(`## How you work

- Act on the request as asked. Do the whole task, not just the easy part.
- Prefer using your tools to find out rather than guessing or asking. Read the file, run the command, search the web.
- When a task needs several steps, keep a task list with the todo tool so progress stays visible. Every todo write REPLACES the whole list, so send the full list each time and keep already-finished items marked completed — never drop items or reset their status.
- Work the task list to the end in one turn: mark an item in_progress, finish it, mark it completed, then go straight to the next — without stopping. Do NOT end your turn or ask "should I continue?", "shall I proceed?", or "let me know if you want me to go on" between steps. Keep working until every item is completed. Stop only when the whole list is done, or when you are genuinely blocked on something only the user can resolve (a missing secret, an ambiguous requirement, an irreversible/destructive choice) — and then say exactly what you need. Needing confirmation to continue is not a real blocker; finishing the tasks is the job.
- Report outcomes honestly. If a command failed, say so and show the output. Never claim work you did not verify.
- Be concise. Skip preamble and restating the question; lead with the answer or the result.
- Match the user's language. If they write Indonesian, answer in Indonesian.
`)

	// A standing goal is the whole frame for the turn, so it goes near the top.
	if g, ok := a.GetGoal(ctx, sess.ID); ok && !g.Done && !g.Paused {
		b.WriteString("\n## Standing goal\n\n")
		b.WriteString("You are working towards this until it is met. Every turn should move it forward:\n\n")
		b.WriteString(g.Text)
		b.WriteString("\n")
	}

	b.WriteString("\n## Environment\n\n")
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format("Monday, 2 January 2006, 15:04 MST"))
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "- Workspace: %s\n", sess.Workspace)
	if host, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "- Host: %s\n", host)
	}
	fmt.Fprintf(&b, "- Channel: %s\n", firstNonEmpty(req.Platform, "web"))
	fmt.Fprintf(&b, "- Terminal backend: %s\n", cfg.Terminal.Backend)
	if req.Platform == "discord" {
		b.WriteString("- You are answering in Discord. Discord Markdown works: **bold**, *italics*, " +
			"__underline__, ~~strikethrough~~, `inline code`, ```fenced code blocks```, > quotes, " +
			"- bullet lists, and spoilers with ||spoiler text||. Use them when they help. " +
			"Keep replies reasonably short for a chat; do not emit raw HTML.\n")
	}

	if len(active) > 0 {
		b.WriteString("\n## Tool notes\n\n")
		b.WriteString("- Paths given to file tools are relative to the workspace; you cannot read outside it.\n")
		if hasTool(active, "read_file") || hasTool(active, "edit_file") {
			// Harness guidance for the read → edit loop. Without this, models
			// paste line numbers into old_string or expand tabs to spaces and
			// the exact match fails repeatedly.
			b.WriteString("- read_file returns lines as `NUMBER|CONTENT`. The `|` is metadata only. When calling edit_file, copy **only** the content after `|` into old_string/new_string — never the line number. Preserve tabs and spaces exactly (do not expand tabs to spaces). Line endings are matched automatically.\n")
		}
		if hasTool(active, "vps_upload") || hasTool(active, "vps_download") || hasTool(active, "vps_run") {
			// Without this, models fall back to terminal rsync/scp and never use
			// the saved-host SFTP tools (credentials and TOFU stay unused).
			b.WriteString("- VPS file transfer: use **vps_upload** (local → server) and **vps_download** (server → local) over SFTP on dashboard-saved hosts. Do **not** use terminal `rsync`, `scp`, or `sftp` CLI for those hosts when these tools are available — they already hold the SSH credentials. Use **vps_run** for remote shell commands (systemctl, logs, apt). Call vps_run with no command first to list server ids/labels. Single files only for upload/download (max 256 MiB); for huge trees say so and use vps_run only if the user explicitly wants remote-side pull/rsync.\n")
		}
		b.WriteString("- The terminal keeps state between calls: `cd`, exports, and activated environments persist.\n")
		if hasTool(active, "memory") && cfg.Memory.Enabled {
			b.WriteString("- Save durable facts about the user or project with the memory tool. Save only what stays true across sessions.\n")
		}
		if hasTool(active, "rag_search") && a.rag != nil {
			b.WriteString("- Before reading many files, try rag_search to locate the relevant passages first.\n")
		}
		if hasTool(active, "delegate_task") && cfg.Delegation.Enabled {
			b.WriteString("- Delegate self-contained research or parallel work with delegate_task; the sub-agent cannot see this conversation, so its prompt must stand alone.\n")
			if hasTool(active, "task") {
				b.WriteString("- For several long workstreams, start each with delegate_task(background=true) to get a task handle, keep working, then collect results with the task tool (status/output/list/stop).\n")
			}
		}
		if hasTool(active, "http_request") {
			b.WriteString("- For HTTP requests and API calls, prefer http_request: it presents a real browser's TLS and HTTP/2 fingerprint, so endpoints behind bot-detection answer, and it returns the status, headers, and body as structured output. (curl and wget in the terminal are routed through the same fingerprinted client, so they work too.)\n")
		}
		if hasTool(active, "web_fetch") || hasTool(active, "http_request") {
			b.WriteString("- When you are handed a target — a URL, host, or app to inspect or assess — go straight at it: fetch it with web_fetch, drive requests against it with http_request, and crawl, enumerate, or scan it with your terminal tools. web_search only finds background information about something you do not have; never use it to \"look up\" a target you were already given, and if a search fails, switch to probing the target directly instead of searching again.\n")
		}
		if hasTool(active, "osint_email_full") {
			// Always-on so the rule reaches every entry point (web, Discord, CLI),
			// not only when the osint role's prompt happens to be applied.
			b.WriteString("- For an OSINT lookup on an EMAIL, `osint_email_full` is the mandatory first step and the ONLY tool to call until it succeeds. It returns the richest seed (registered accounts, usernames, breaches, linked emails). The solve is flaky and may rate-limit, so on ANY error (Turnstile/token/timeout/HTTP 429) just call `osint_email_full` again — up to 5 times — before ever using osint_email, osint_username, osint_breach, osint_dorks, osint_footprint, or any other tool. Do NOT run those in parallel with it; they are the fallback only after 5 failed attempts. A stored proxy is applied automatically.\n")
		}
	}

	// When an assessment is under way, its methodology state is pushed into every
	// turn so the agent is driven towards completeness instead of having to ask.
	if hasTool(active, "methodology_status") {
		if block := a.methodologyBlock(sess.ID); block != "" {
			b.WriteString(block)
		}
	}

	if a.skills != nil && cfg.Skills.Enabled {
		if catalogue := a.skills.PromptBlock(60); catalogue != "" {
			b.WriteString("\n## Your skills\n\n")
			b.WriteString("Procedures you have learned. Read one with the skill tool before following it.\n\n")
			b.WriteString(catalogue)
		}
	}

	if cfg.Memory.Enabled {
		if mem := a.memoryBlock(ctx, req, sess); mem != "" {
			b.WriteString("\n## What you remember\n\n")
			b.WriteString(mem)
		}
		// Lessons the agent learned from its own past mistakes.
		b.WriteString(a.lessonsBlock(ctx))
	}

	if s := strings.TrimSpace(cfg.Agent.SystemPromptExtra); s != "" {
		b.WriteString("\n## Additional instructions\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(req.SystemExtra); s != "" {
		b.WriteString("\n## Task context\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if p := strings.TrimSpace(cfg.Agent.Personality); p != "" && p != "default" {
		fmt.Fprintf(&b, "\n## Persona\n\n%s\n", p)
	}

	// Project session: fold in the project's own instructions and a map of it,
	// and state the write boundary plainly so the model respects it.
	if pd, _ := sess.Meta["project_dir"].(string); strings.TrimSpace(pd) != "" {
		b.WriteString(projectBlock(pd, cfg.Agent.Workspace))
	}

	// Native RAG auto-context: relevant indexed knowledge and past conversation
	// pulled for this turn. Best-effort — empty when RAG is off or nothing hits.
	if block := a.autoContext(ctx, req, sess); block != "" {
		b.WriteString(block)
	}

	return b.String()
}

// projectBlock builds the "## Project" section for a project session: the write
// rule, the project's AGENTS.md/CLAUDE.md instructions, its README summary, and
// a shallow listing of its top level.
func projectBlock(projectDir, antaresWorkspace string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Project\n\nThis is a PROJECT session bound to %s.\n", projectDir)
	b.WriteString("- You may WRITE and edit files only inside this project")
	if antaresWorkspace != "" && antaresWorkspace != projectDir {
		fmt.Fprintf(&b, " (and the antares workspace %s)", antaresWorkspace)
	}
	b.WriteString(". Writing anywhere else is refused by the file tools.\n")
	b.WriteString("- You may READ and copy files from anywhere on the machine.\n")
	b.WriteString("- The terminal runs with its working directory in the project. Do not modify files outside the project from the shell either.\n")
	b.WriteString("- Follow the project's own conventions below over your defaults.\n")
	b.WriteString("- Keep the project sidebar current with the project_info tool: record the essential facts (summary, main languages/frameworks, a few key libraries, build/run commands) after you understand the project, and update them when the stack meaningfully changes. Keep each list short.\n")

	// AGENTS.md / CLAUDE.md — the project's instructions to an agent.
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if txt := readCapped(filepath.Join(projectDir, name), 8000); txt != "" {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", name, txt)
		}
	}
	// README — project overview.
	for _, name := range []string{"README.md", "readme.md", "README"} {
		if txt := readCapped(filepath.Join(projectDir, name), 4000); txt != "" {
			fmt.Fprintf(&b, "\n### README\n\n%s\n", txt)
			break
		}
	}
	// A shallow map of the top level so the agent orients without a list_files.
	if tree := shallowTree(projectDir); tree != "" {
		fmt.Fprintf(&b, "\n### Project layout (top level)\n\n%s\n", tree)
	}
	return b.String()
}

// readCapped returns a file's text truncated to max bytes, or "" if unreadable.
func readCapped(path string, max int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > max {
		return strings.TrimSpace(string(data[:max])) + "\n… (truncated)"
	}
	return strings.TrimSpace(string(data))
}

// shallowTree lists the immediate entries of dir (dirs marked with a trailing
// slash), skipping the noisy ones, so the prompt shows the project's shape.
func shallowTree(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	skip := map[string]bool{".git": true, "node_modules": true, ".DS_Store": true}
	var out []string
	for _, e := range entries {
		if skip[e.Name()] {
			continue
		}
		if e.IsDir() {
			out = append(out, e.Name()+"/")
		} else {
			out = append(out, e.Name())
		}
		if len(out) >= 60 {
			out = append(out, "…")
			break
		}
	}
	return strings.Join(out, "\n")
}

// methodologyBlock renders the live assessment state — which phases have
// evidence, which are open, and the single next step — so the agent is pushed
// towards completeness. It is empty until an engagement has started (the first
// intel is recorded), so ordinary sessions never see it.
func (a *Agent) methodologyBlock(sessionID string) string {
	if a.intel == nil {
		return ""
	}
	list, err := a.intel.List(sessionID)
	if err != nil || len(list) == 0 {
		return ""
	}

	// Scope is no longer a gate (the /scope command and scope_check tool were
	// removed). The engagement state machine still takes a hasScope flag for
	// its scope-gathering phase; pass true so that phase is always considered
	// satisfied.
	hasScope := true
	hasReport := false
	if a.findings != nil {
		if f, err := a.findings.List(sessionID); err == nil && len(f) > 0 {
			hasReport = true
		}
	}
	states, err := a.intel.State(sessionID, hasScope, hasReport)
	if err != nil || len(states) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Assessment in progress\n\n")
	b.WriteString("Keep testing until every phase has evidence. Do not write the report while services sit untested.\n\n")
	for _, st := range states {
		mark := map[engagement.PhaseStatus]string{
			engagement.Complete: "[x]", engagement.InProgress: "[~]",
			engagement.Blocked: "[!]", engagement.NotStarted: "[ ]",
		}[st.Status]
		fmt.Fprintf(&b, "%s %s", mark, st.Title)
		if st.Evidence > 0 {
			fmt.Fprintf(&b, " (%d recorded)", st.Evidence)
		}
		b.WriteString("\n")
	}
	// Testing coverage: which vulnerability classes have evidence, and which are
	// still untested. This is what stops the agent from testing one class and
	// declaring the phase done.
	evidence := make([]string, 0, len(list))
	for _, in := range list {
		evidence = append(evidence, in.Value, in.Detail)
	}
	if a.findings != nil {
		if fs, err := a.findings.List(sessionID); err == nil {
			for _, f := range fs {
				evidence = append(evidence, f.Title, f.CWE, f.Description)
			}
		}
	}
	cov := engagement.Coverage(evidence)
	fmt.Fprintf(&b, "\nTesting coverage (%d%%):\n", engagement.CoveragePercent(cov))
	for _, c := range cov {
		mark := "[ ]"
		if c.Covered {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, c.Area.Title)
	}

	// Dangerous combinations across the findings: report the real, compounded
	// impact rather than a list of separate medium-severity issues.
	if chains := engagement.DetectChains(evidence); len(chains) > 0 {
		b.WriteString("\nPotential chains (verify and report the combined impact):\n")
		for _, ch := range chains {
			fmt.Fprintf(&b, "- %s — %s\n", ch.Name, ch.Impact)
		}
	}

	if _, directive := engagement.NextStep(states); directive != "" {
		b.WriteString("\nNext: " + directive + "\n")
	}
	if !hasScope {
		b.WriteString("No scope is set — add authorized targets with the scope tools before active testing.\n")
	}
	return b.String()
}

// memoryBlock renders stored memories, bounded by the configured char limit.
func (a *Agent) memoryBlock(ctx context.Context, req Request, sess *store.Session) string {
	limit := a.cfg.Memory.MemoryCharLimit
	if limit <= 0 {
		limit = 12000
	}
	var lines []string
	used := 0

	add := func(items []store.Memory) {
		for _, m := range items {
			line := "- " + m.Content
			if used+len(line) > limit {
				return
			}
			lines = append(lines, line)
			used += len(line)
		}
	}

	if global, err := a.db.ListMemories(ctx, "global", "", 80); err == nil {
		add(global)
	}
	if req.UserID != "" {
		if user, err := a.db.ListMemories(ctx, "user", req.UserID, 40); err == nil {
			add(user)
		}
	}
	if sessMem, err := a.db.ListMemories(ctx, "session", sess.ID, 20); err == nil {
		add(sessMem)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func hasTool(list []tools.Tool, name string) bool {
	for _, t := range list {
		if t.Name() == name {
			return true
		}
	}
	return false
}

// resolveTools picks the tool set for this run, honouring platform overrides.
func (a *Agent) resolveTools(req Request) []tools.Tool {
	cfg := a.cfg
	toolset := req.Toolset
	if toolset == "" {
		if v, ok := cfg.Tools.Platform[req.Platform]; ok && v != "" {
			toolset = v
		}
	}
	if toolset == "" {
		toolset = cfg.Tools.Toolset
	}

	selected := a.reg.Resolve(toolset, cfg.Tools.Enabled, cfg.Tools.Disabled)

	// Drop tools whose backing service is unavailable so the model is not shown
	// capabilities that will always fail.
	out := make([]tools.Tool, 0, len(selected))
	for _, t := range selected {
		switch t.Name() {
		case "rag_search", "rag_index":
			if a.rag == nil {
				continue
			}
		case "memory":
			if !cfg.Memory.Enabled {
				continue
			}
		case "delegate_task":
			if !cfg.Delegation.Enabled || req.Depth > 0 {
				continue
			}
		case "web_search":
			if strings.EqualFold(cfg.Tools.WebSearch.Provider, "none") {
				continue
			}
		case "skill":
			if a.skills == nil || !cfg.Skills.Enabled {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// estimateTokens is a cheap heuristic (~4 chars per token) used for compaction
// decisions; exact counts are not needed to decide when to summarise.
func estimateTokens(msgs []llm.Message, system string) int {
	total := len(system) / 4
	for _, m := range msgs {
		total += len(m.Content)/4 + len(m.Reasoning)/4 + 8
		for _, tc := range m.ToolCalls {
			total += len(tc.Arguments)/4 + len(tc.Name)/4 + 8
		}
		for _, p := range m.Parts {
			if p.Type == "image" {
				total += 800 // rough per-image cost
				continue
			}
			total += len(p.Text) / 4
		}
	}
	return total
}
