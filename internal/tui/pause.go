// pause.go handles the two ways a turn stops and waits for the person: an
// ask_user question and a tool that needs approval (tools.approval_mode
// "prompt"). The agent blocks the tool call until ResolveAsk / ResolveApproval
// answers it, so a surface that ignores these events leaves the turn hanging.
//
// Both are recorded in the transcript, and shown in a box between the chat
// and the composer (QUESTION or APPROVAL, drawn below) — no modal:
//
//   - ask: the question (and any offered choices) is shown; the next text
//     submitted from the composer is the answer. A bare number picks an
//     offered choice. Esc puts the question aside without answering; /answer
//     answers it later, and the turn stays paused until then.
//   - approval: the tool and what it wants to do are shown; "y" on an empty
//     composer allows, "n" or Esc refuses. Several requests (parallel tool
//     calls) queue and are decided oldest first.

package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/agent"
)

// pendingAsk is a question the running turn is waiting on.
type pendingAsk struct {
	id        string
	questions []agent.AskQuestion
	// aside is set when Esc put the question away: the composer sends
	// ordinary input again and only /answer resolves it.
	aside bool
}

// pendingApproval is one tool call waiting for a yes or no.
type pendingApproval struct {
	id      string
	tool    string
	summary string
	reason  string
}

// askFromEvent decodes an EventAsk. The questions travel as JSON in Content.
func askFromEvent(e agent.Event) *pendingAsk {
	var payload struct {
		ID        string              `json:"id"`
		Questions []agent.AskQuestion `json:"questions"`
	}
	_ = json.Unmarshal([]byte(e.Content), &payload)
	id := e.ID
	if id == "" {
		id = payload.ID
	}
	if id == "" {
		return nil
	}
	return &pendingAsk{id: id, questions: payload.Questions}
}

// approvalFromEvent decodes an EventApproval.
func approvalFromEvent(e agent.Event) pendingApproval {
	var payload struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal([]byte(e.Content), &payload)
	return pendingApproval{
		id:      e.ID,
		tool:    e.Name,
		summary: summarizeArgs(e.Arguments),
		reason:  payload.Reason,
	}
}

// askPrompt renders a staged question for the transcript.
func askPrompt(a *pendingAsk) string {
	var b strings.Builder
	b.WriteString("The agent is asking:\n")
	for i, q := range a.questions {
		label := q.Question
		if q.Header != "" {
			label = q.Header + " — " + q.Question
		}
		if len(a.questions) > 1 {
			fmt.Fprintf(&b, "\n%d. %s\n", i+1, label)
		} else {
			fmt.Fprintf(&b, "\n%s\n", label)
		}
		for j, opt := range q.Options {
			fmt.Fprintf(&b, "   %d) %s\n", j+1, opt)
		}
		if q.MultiSelect && len(q.Options) > 0 {
			b.WriteString("   (pick several: 1,3)\n")
		}
	}
	// How to answer is the QUESTION box's to say, in its bottom edge; this
	// is the record. Only when there are several does it say how they go.
	if len(a.questions) > 1 {
		b.WriteString("\nAnswer each on its own line.")
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolveAskAnswer turns what was typed into the answer the tool receives.
// With a single question that offers choices, numbers pick choices ("2", or
// "1,3" for several); anything else is passed through as written.
func resolveAskAnswer(a *pendingAsk, text string) string {
	text = strings.TrimSpace(text)
	if a == nil || len(a.questions) != 1 || len(a.questions[0].Options) == 0 {
		return text
	}
	opts := a.questions[0].Options
	parts := strings.Split(text, ",")
	picked := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > len(opts) {
			return text
		}
		picked = append(picked, opts[n-1])
	}
	if len(picked) > 1 && !a.questions[0].MultiSelect {
		return text
	}
	return strings.Join(picked, ", ")
}

// answerAsk delivers an answer to the waiting question.
func (m *Model) answerAsk(text string) {
	a := m.ask
	if a == nil {
		return
	}
	m.ask = nil
	answer := resolveAskAnswer(a, text)
	m.blocks = append(m.blocks, block{kind: blockUser, text: answer})
	if m.ag == nil || !m.ag.ResolveAsk(a.id, answer) {
		m.pushSystem("That question is no longer waiting — the turn was stopped.")
		return
	}
	m.setStatus("answered")
}

// putAskAside dismisses the prompt without answering.
func (m *Model) putAskAside() {
	if m.ask == nil {
		return
	}
	m.ask.aside = true
	m.pushSystem("Question put aside — the turn stays paused. Answer it with /answer <text>, or /stop to end the turn.")
}

func (m *Model) cmdAnswer(args string) (bool, tea.Cmd) {
	if m.ask == nil {
		m.pushSystem("Nothing is waiting for an answer.")
		return false, nil
	}
	if strings.TrimSpace(args) == "" {
		m.ask.aside = false
		m.pushSystem(askPrompt(m.ask))
		return false, nil
	}
	m.answerAsk(args)
	return false, nil
}

// approvalPrompt renders the request at the head of the queue.
func approvalPrompt(p pendingApproval, queued int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Allow %s?", p.tool)
	if p.summary != "" {
		fmt.Fprintf(&b, "  %s", p.summary)
	}
	b.WriteString("\n")
	if p.reason != "" {
		fmt.Fprintf(&b, "  Careful: %s.\n", p.reason)
	}
	if queued > 1 {
		fmt.Fprintf(&b, "  (%d more waiting after this one)\n", queued-1)
	}
	b.WriteString("Press y to allow, n or Esc to refuse.")
	return b.String()
}

// queueApproval stages a request, showing it at once when nothing else is
// waiting.
func (m *Model) queueApproval(p pendingApproval) {
	if p.id == "" {
		return
	}
	m.approvals = append(m.approvals, p)
	if len(m.approvals) == 1 {
		m.pushSystem(approvalPrompt(p, 1))
	}
	m.setStatus("waiting for approval")
}

// decideApproval answers the oldest request and shows the next one.
func (m *Model) decideApproval(allow bool) {
	if len(m.approvals) == 0 {
		return
	}
	p := m.approvals[0]
	m.approvals = m.approvals[1:]
	verdict := "Refused " + p.tool + "."
	if allow {
		verdict = "Allowed " + p.tool + "."
	}
	if m.ag == nil || !m.ag.ResolveApproval(p.id, allow) {
		verdict = "That request is no longer waiting (it timed out or the turn stopped)."
	}
	m.pushSystem(verdict)
	if len(m.approvals) > 0 {
		m.pushSystem(approvalPrompt(m.approvals[0], len(m.approvals)))
	} else {
		m.setStatus("")
	}
}

// clearPauses drops staged questions and approvals once their turn is over;
// the agent has already abandoned them.
func (m *Model) clearPauses() {
	m.ask = nil
	m.approvals = nil
}

// ---- the QUESTION and APPROVAL boxes ---------------------------------------
//
// While a turn waits on the person, a box opens between the chat box and the
// composer, where the command list goes, and stays fixed there while the
// transcript scrolls. The transcript keeps the record (askPrompt,
// approvalPrompt); the box is where the answer is given.

// askOptions is the single question's choices when the box can offer them as
// rows to pick from, or nil.
func (m *Model) askOptions() []string {
	a := m.ask
	if a == nil || a.aside || len(a.questions) != 1 {
		return nil
	}
	return a.questions[0].Options
}

// askKey works the QUESTION box on an empty composer: ↑↓ move between the
// options and the "Other" row, Enter answers with the highlighted option.
// Typing still answers in the person's own words, and a typed number still
// picks an option (resolveAskAnswer).
func (m *Model) askKey(msg tea.KeyMsg) bool {
	opts := m.askOptions()
	if len(opts) == 0 || m.ta.Value() != "" || len(m.palette) > 0 {
		return false
	}
	m.syncAskSel()
	n := len(opts) + 1
	switch msg.Type {
	case tea.KeyUp:
		m.chrome.askSel = (m.chrome.askSel - 1 + n) % n
		return true
	case tea.KeyDown, tea.KeyTab:
		m.chrome.askSel = (m.chrome.askSel + 1) % n
		return true
	case tea.KeyEnter:
		if m.chrome.askSel >= len(opts) {
			m.setStatus("type your answer, then Enter")
			return true
		}
		m.answerAsk(strconv.Itoa(m.chrome.askSel + 1))
		m.chrome.askSel = 0
		m.refreshTranscript()
		m.vp.GotoBottom()
		return true
	}
	return false
}

// syncAskSel starts the highlight on the first option of each new question.
func (m *Model) syncAskSel() {
	if m.ask != nil && m.chrome.askID != m.ask.id {
		m.chrome.askID, m.chrome.askSel = m.ask.id, 0
	}
}

// pauseBox is the box the waiting turn shows: its title, the keys for its
// bottom edge, and its rows at cw columns. Empty when nothing waits.
func (m *Model) pauseBox(cw int) (title, hint string, body []string) {
	t := themeByName(m.themeName)
	label := func(s string) string { return fg(t.Muted).Render(padRight(s, 8)) }
	if len(m.approvals) > 0 {
		p := m.approvals[0]
		title = fg(t.Red).Bold(true).Render("APPROVAL")
		if n := len(m.approvals); n > 1 {
			title += fg(t.Muted).Render(fmt.Sprintf(" · 1 OF %d", n))
		}
		body = append(body, label("tool")+fg(t.Text).Bold(true).Render(p.tool))
		if p.summary != "" {
			for i, ln := range strings.Split(wrapPlain(p.summary, maxi(cw-8, 8)), "\n") {
				if i == 2 {
					break
				}
				lead := label("target")
				if i > 0 {
					lead = strings.Repeat(" ", 8)
				}
				body = append(body, lead+fg(t.Text).Render(ln))
			}
		}
		if p.reason != "" {
			body = append(body, label("risk")+fg(t.Yellow).Render(truncate(p.reason, maxi(cw-8, 8))))
		}
		return title, "y allow · n refuse · Esc refuse", body
	}
	a := m.ask
	if a == nil || a.aside {
		return "", "", nil
	}
	m.syncAskSel()
	title = fg(t.Accent2).Bold(true).Render("QUESTION")
	if n := len(a.questions); n > 1 {
		title = fg(t.Accent2).Bold(true).Render(fmt.Sprintf("%d QUESTIONS", n))
	}
	ask := func(q agent.AskQuestion, mark string) {
		text := q.Question
		if q.Header != "" {
			text = q.Header + " — " + q.Question
		}
		for i, ln := range strings.Split(wrapPlain(text, maxi(cw-2, 8)), "\n") {
			lead := "  "
			if i == 0 {
				lead = fg(t.Accent2).Bold(true).Render(mark) + " "
			}
			body = append(body, lead+fg(t.Text).Bold(true).Render(ln))
		}
	}
	if len(a.questions) == 1 {
		q := a.questions[0]
		ask(q, "?")
		if len(q.Options) == 0 {
			body = append(body, "", fg(t.Faint).Render("  Type your answer below."))
			return title, "Enter send · Esc later", body
		}
		body = append(body, "")
		numW := len(strconv.Itoa(len(q.Options) + 1))
		labelW := len("Other")
		for _, o := range q.Options {
			labelW = maxi(labelW, lipgloss.Width(o))
		}
		labelW = lesser(labelW, maxi(cw-numW-6, 8))
		row := func(i int, text, note string) {
			sel := i == m.chrome.askSel
			mark, num := " ", fg(t.Muted).Render(padLeft(strconv.Itoa(i+1), numW))
			name := fg(t.Text).Render(padRight(truncate(text, labelW), labelW))
			if sel {
				mark = fg(t.Accent2).Bold(true).Render("›")
				num = fg(t.Accent2).Bold(true).Render(padLeft(strconv.Itoa(i+1), numW))
				name = fg(t.Text).Bold(true).Render(padRight(truncate(text, labelW), labelW))
			}
			line := mark + " " + num + "  " + name
			if note != "" {
				line += "   " + fg(t.Faint).Render(note)
			}
			if sel {
				line = paint(line, cw, t.Band)
			}
			body = append(body, line)
		}
		for i, o := range q.Options {
			row(i, o, "")
		}
		row(len(q.Options), "Other", "type your own answer")
		hint = "↑↓ choose · Enter answer · Esc later"
		if q.MultiSelect {
			hint = "↑↓ choose · type 1,3 for several · Enter answer · Esc later"
		}
		return title, hint, body
	}
	for i, q := range a.questions {
		if i > 0 {
			body = append(body, "")
		}
		ask(q, strconv.Itoa(i+1)+".")
		for j, o := range q.Options {
			body = append(body, "   "+fg(t.Muted).Render(strconv.Itoa(j+1)+")")+" "+fg(t.Text).Render(o))
		}
	}
	return title, "one answer per line · Ctrl+J newline · Enter send · Esc later", body
}

// pauseHeight is the rows the waiting turn's box wants at width w.
func (m *Model) pauseHeight(w int) int {
	cw, _, _, _ := contentSize(w, 10, true)
	_, _, body := m.pauseBox(cw)
	if len(body) == 0 {
		return 0
	}
	return len(body) + 2 + 2*padY
}

func (m *Model) pauseLines(w, h int) []string {
	t := themeByName(m.themeName)
	cw, _, _, _ := contentSize(w, h, true)
	title, hint, body := m.pauseBox(cw)
	border := t.Accent2
	if len(m.approvals) > 0 {
		border = t.Red
	}
	return drawBox(box{
		w: w, h: h, border: border, fill: t.Panel,
		title: title, hint: fg(t.Muted).Render(hint), body: body, vpad: true,
	})
}

func padLeft(s string, n int) string {
	for len([]rune(s)) < n {
		s = " " + s
	}
	return s
}
