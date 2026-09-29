// pause.go handles the two ways a turn stops and waits for the person: an
// ask_user question and a tool that needs approval (tools.approval_mode
// "prompt"). The agent blocks the tool call until ResolveAsk / ResolveApproval
// answers it, so a surface that ignores these events leaves the turn hanging.
//
// Both are staged inline in the transcript, in the style of the /undo
// confirmation in undo.go — no modal:
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
	if len(a.questions) > 1 {
		b.WriteString("\nAnswer each on its own line.")
	} else if len(a.questions) == 1 && len(a.questions[0].Options) > 0 {
		b.WriteString("\nType a number, or your own answer.")
	}
	b.WriteString("\nEnter sends the answer · Esc answers later with /answer.")
	return b.String()
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
