package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/checkpoint"
	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/findings"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/plugin"
	"github.com/enowdev/antares/internal/roleperf"
	"github.com/enowdev/antares/internal/roles"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// The harness is everything around the model call that makes a long run
// survivable: catching a loop that has stopped making progress, letting a
// person redirect a run already in flight, checking the work before claiming
// it is done, and carrying a goal across turns.

// ---- steering ----------------------------------------------------------------

// steering holds notes typed while a turn was already running.
type steering struct {
	mu    sync.Mutex
	byKey map[string][]string
}

var steer = steering{byKey: map[string][]string{}}

// Steer queues a note for a run in progress. It is delivered after the current
// batch of tools finishes, which is the first moment the model can act on it
// without discarding work already underway.
//
// It reports false when nothing is running for that session, so a caller can
// fall back to sending an ordinary message.
func (a *Agent) Steer(sessionID, note string) bool {
	note = strings.TrimSpace(note)
	if note == "" || sessionID == "" {
		return false
	}
	a.mu.Lock()
	_, running := a.active[sessionID]
	a.mu.Unlock()
	if !running {
		return false
	}
	steer.mu.Lock()
	steer.byKey[sessionID] = append(steer.byKey[sessionID], note)
	steer.mu.Unlock()
	return true
}

// drainSteering takes whatever has been queued for a session.
func drainSteering(sessionID string) []string {
	steer.mu.Lock()
	defer steer.mu.Unlock()
	notes := steer.byKey[sessionID]
	delete(steer.byKey, sessionID)
	return notes
}

// ---- repetition guard --------------------------------------------------------

// repeatTracker counts identical tool calls within one run. A model that has
// lost the thread will call the same tool with the same arguments over and
// over; left alone it burns the entire turn budget on it.
type repeatTracker struct {
	seen  map[string]int
	limit int
}

func newRepeatTracker(limit int) *repeatTracker {
	if limit <= 0 {
		limit = 3
	}
	return &repeatTracker{seen: map[string]int{}, limit: limit}
}

// record fingerprints a batch of calls and returns the ones that have now been
// repeated too often.
func (r *repeatTracker) record(calls []llm.ToolCall) []string {
	var tripped []string
	for _, c := range calls {
		if processObservation(c) {
			// Polling a managed process is an observation of changing external
			// state, not a stuck model repeating an idempotent call. The process
			// tool bounds every wait and terminal jobs have explicit cancellation.
			continue
		}
		key := repeatKey(c)
		r.seen[key]++
		if r.seen[key] == r.limit {
			tripped = append(tripped, c.Name)
		}
	}
	return tripped
}

func processObservation(c llm.ToolCall) bool {
	if c.Name != "process" {
		return false
	}
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(c.Arguments), &args) != nil {
		return false
	}
	switch args.Action {
	case "poll", "wait", "log", "list":
		return true
	default:
		return false
	}
}

// exceeded reports a call repeated far past the limit, where nudging has
// already failed and the run should stop.
func (r *repeatTracker) exceeded() bool {
	for _, n := range r.seen {
		if n >= r.limit*2 {
			return true
		}
	}
	return false
}

// normaliseArgs makes two calls compare equal when only key order or spacing
// differs, so a re-serialised identical call is still recognised as a repeat.
func normaliseArgs(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return strings.TrimSpace(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return string(b)
}

// repeatKey builds the fingerprint for a tool call. For write_file and
// edit_file the key uses only the tool name and the target path — not the
// full arguments — so repeated writes to the same file with different
// content are recognised as the same stuck call. Other tools use the full
// normalised arguments as before.
func repeatKey(c llm.ToolCall) string {
	switch c.Name {
	case "write_file", "edit_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(c.Arguments), &args) == nil && args.Path != "" {
			return c.Name + "\x00" + args.Path
		}
	case "vps_upload":
		var args struct {
			RemotePath string `json:"remote_path"`
		}
		if json.Unmarshal([]byte(c.Arguments), &args) == nil && args.RemotePath != "" {
			return c.Name + "\x00" + args.RemotePath
		}
	}
	return c.Name + "\x00" + normaliseArgs(c.Arguments)
}

// ---- verification ------------------------------------------------------------

// verdict is what the critic says about a finished reply.
type verdict struct {
	Complete bool   `json:"complete"`
	Missing  string `json:"missing"`
}

const verifyPrompt = `You are checking another assistant's work before it is shown to the user.

Judge only whether the request was actually carried out. Be strict about work
that was described but not done, and about parts of a multi-part request that
were quietly dropped. Do not comment on style, and do not ask for extra work
beyond what was requested.

Reply with JSON and nothing else:
{"complete": true}
or
{"complete": false, "missing": "<one sentence naming what is still undone>"}`

// verify asks a cheap model whether the request was actually satisfied. It
// returns nil when verification is off, unavailable, or the reply passes.
func (a *Agent) verify(ctx context.Context, request, reply string, transcript []llm.Message) *verdict {
	if !a.cfg.Agent.VerifyReplies || strings.TrimSpace(reply) == "" {
		return nil
	}
	client, model, _, err := a.newAuxClient("")
	if err != nil {
		slog.Debug("verification unavailable", "error", err)
		return nil
	}

	// The critic sees the request, the answer, and what tools actually ran —
	// claims of work done are the thing most worth checking.
	var actions strings.Builder
	for _, m := range transcript {
		for _, c := range m.ToolCalls {
			fmt.Fprintf(&actions, "- %s %s\n", c.Name, truncate(normaliseArgs(c.Arguments), 200))
		}
	}
	if actions.Len() == 0 {
		actions.WriteString("(no tools were used)\n")
	}

	user := fmt.Sprintf("REQUEST\n%s\n\nTOOLS RUN\n%s\nANSWER\n%s",
		truncate(request, 4000), truncate(actions.String(), 4000), truncate(reply, 6000))

	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		System:      verifyPrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: user}},
		Temperature: 0,
		MaxTokens:   200,
	})
	if err != nil {
		slog.Debug("verification failed", "error", err)
		return nil
	}

	var v verdict
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &v); err != nil {
		// An unparseable verdict is not evidence of a problem.
		return nil
	}
	if v.Complete || strings.TrimSpace(v.Missing) == "" {
		return nil
	}
	return &v
}

// extractJSON pulls the first JSON object out of a reply that may be wrapped
// in prose or a code fence.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

// ---- standing goals ----------------------------------------------------------

// Goal is an objective that outlives a single turn.
type Goal struct {
	Text       string `json:"text"`
	Iterations int    `json:"iterations"`
	Max        int    `json:"max"`
	Paused     bool   `json:"paused"`
	Done       bool   `json:"done"`
	Note       string `json:"note,omitempty"`
}

func goalKey(sessionID string) string { return "goal:" + sessionID }

// GetGoal reads the standing goal for a session, if there is one.
func (a *Agent) GetGoal(ctx context.Context, sessionID string) (*Goal, bool) {
	if a.db == nil || sessionID == "" {
		return nil, false
	}
	raw, err := a.db.GetKV(ctx, goalKey(sessionID))
	if err != nil || raw == "" {
		return nil, false
	}
	var g Goal
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil, false
	}
	return &g, true
}

// SetGoal stores or replaces the standing goal.
func (a *Agent) SetGoal(ctx context.Context, sessionID string, g *Goal) error {
	if a.db == nil || sessionID == "" {
		return fmt.Errorf("no session to attach a goal to")
	}
	if g == nil {
		return a.db.DeleteKV(ctx, goalKey(sessionID))
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return a.db.SetKV(ctx, goalKey(sessionID), string(raw))
}

const judgePrompt = `You are judging whether a standing goal has been met.

You will see the goal and what the assistant just did and said. Decide whether
the goal is now complete, or whether there is a concrete next step left.

Be honest: a plan is not completion, and an intention to do something is not
doing it. But do not invent extra work — if the goal is met, say so.

Reply with JSON and nothing else:
{"done": true}
or
{"done": false, "next": "<one specific instruction for the next step>"}`

type judgement struct {
	Done bool   `json:"done"`
	Next string `json:"next"`
}

// judgeGoal decides whether a standing goal is finished, and what to do next
// when it is not.
func (a *Agent) judgeGoal(ctx context.Context, g *Goal, reply string, transcript []llm.Message) judgement {
	client, model, _, err := a.newAuxClient("")
	if err != nil {
		// Without a judge the loop must not run forever.
		return judgement{Done: true}
	}

	var actions strings.Builder
	for _, m := range transcript {
		for _, c := range m.ToolCalls {
			fmt.Fprintf(&actions, "- %s %s\n", c.Name, truncate(normaliseArgs(c.Arguments), 160))
		}
	}
	user := fmt.Sprintf("GOAL\n%s\n\nWHAT WAS DONE\n%s\nWHAT WAS SAID\n%s",
		truncate(g.Text, 2000), truncate(actions.String(), 4000), truncate(reply, 4000))

	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		System:      judgePrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: user}},
		Temperature: 0,
		MaxTokens:   300,
	})
	if err != nil {
		return judgement{Done: true}
	}
	var j judgement
	if json.Unmarshal([]byte(extractJSON(resp.Content)), &j) != nil {
		return judgement{Done: true}
	}
	if !j.Done && strings.TrimSpace(j.Next) == "" {
		// "Not done" with nothing to do next is the same as done.
		return judgement{Done: true}
	}
	return j
}

// ---- learning ----------------------------------------------------------------

const distilPrompt = `Turn this conversation into a reusable skill: a written procedure the
assistant can follow next time a similar task comes up.

Write Markdown with YAML front matter:
---
name: <short-kebab-case-name>
description: <one sentence saying what it does and when to use it>
tags: [<one or two words>]
---

Then the procedure itself. Write what to do, in order, with the specific
commands, paths, and gotchas that actually came up. Skip anything that was
particular to this one conversation — a skill is for next time, not a diary of
this time.

If nothing general was learned, reply with exactly: NOTHING`

// Distil writes a skill from a session's transcript. It returns the Markdown,
// or an empty string when there was nothing worth keeping.
func (a *Agent) Distil(ctx context.Context, sessionID, hint string) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("no database to read the session from")
	}
	messages, err := a.db.ListMessages(ctx, sessionID, 200, 0)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("this session has nothing in it yet")
	}

	var b strings.Builder
	for _, m := range messages {
		switch m.Role {
		case store.RoleUser:
			fmt.Fprintf(&b, "USER: %s\n", truncate(m.Content, 1500))
		case store.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", truncate(m.Content, 1500))
			}
		case store.RoleTool:
			fmt.Fprintf(&b, "TOOL %s: %s\n", m.ToolName, truncate(m.Content, 400))
		}
	}
	if hint != "" {
		fmt.Fprintf(&b, "\nThe user asked that the skill focus on: %s\n", hint)
	}

	client, model, _, err := a.newAuxClient("")
	if err != nil {
		return "", err
	}
	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		System:      distilPrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: truncate(b.String(), 40000)}},
		Temperature: 0.2,
		MaxTokens:   2000,
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" || strings.HasPrefix(out, "NOTHING") {
		return "", nil
	}
	// Models like to wrap the whole file in a fence.
	out = strings.TrimPrefix(out, "```markdown")
	out = strings.TrimPrefix(out, "```md")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(strings.TrimSpace(out), "```")
	return strings.TrimSpace(out), nil
}

// followUp decides whether a run that looks finished should keep going. It
// returns the instruction to append, or an empty string to stop.
//
// Two checks in order: did the reply actually do what was asked, and is a
// standing goal satisfied. Each is bounded, because a critic that is never
// satisfied would otherwise loop forever.
// maxTodoNudges bounds how many times a single run may be pushed to finish its
// open tasks, so a task list the model refuses to complete cannot loop forever.
const maxTodoNudges = 8

// incompleteTodos counts task-list items not yet marked completed for a session,
// read from the same KV the todo tool writes to.
func (a *Agent) incompleteTodos(ctx context.Context, sessionID string) int {
	if a.db == nil {
		return 0
	}
	raw, err := a.db.GetKV(ctx, "todo:"+sessionID)
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0
	}
	var items []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if json.Unmarshal([]byte(raw), &items) != nil {
		return 0
	}
	open := 0
	for _, it := range items {
		if strings.TrimSpace(it.Content) != "" && !strings.EqualFold(it.Status, "completed") {
			open++
		}
	}
	return open
}

// todoContinueMessage is injected when a turn would otherwise end with tasks
// still open — the harness half of "finish everything before stopping".
func todoContinueMessage(open int) string {
	return fmt.Sprintf("You still have %d task(s) in your todo list that are not marked completed. "+
		"Do not stop, and do not ask whether to continue.\n"+
		"IMPORTANT: the todo tool REPLACES the whole list on each write. So call todo(write) with the ENTIRE list — "+
		"every item — keeping already-finished items as \"completed\" and setting the ones you have now finished to \"completed\". "+
		"Do not drop items or reset a completed item back to pending.\n"+
		"Then continue the remaining work. If a task truly cannot be done, mark it \"completed\" with a short note in its content saying why "+
		"(or, if it needs a secret/decision only the user can give, say exactly what you need). "+
		"Keep going until every item is completed.", open)
}

// maxGuardrailContinues bounds how many times a single run may extend past the
// tool-call guardrail because tasks are still open. It is a higher ceiling than
// a single segment's budget — enough for a long multi-step task — while still
// preventing a genuine runaway tool loop from running unbounded. agent.max_turns
// remains the absolute backstop.
const maxGuardrailContinues = 4

// guardrailContinueMessage is injected when the per-segment tool-call budget is
// reached but the todo list still has open items: keep working rather than stop.
func guardrailContinueMessage(open int) string {
	return fmt.Sprintf("You have made many tool calls, but %d task(s) on your todo list are still open. "+
		"This is a normal checkpoint, not a limit to stop at — keep going and finish them. "+
		"Work efficiently: prefer fewer, higher-value tool calls, and avoid repeating steps that did not help. "+
		"Continue until every task is completed.", open)
}

func (a *Agent) followUp(
	ctx context.Context,
	req Request,
	sess *store.Session,
	history []llm.Message,
	reply string,
	goal *Goal,
	hasGoal bool,
	verified, judged *int,
	emit Emit,
) string {
	// A sub-agent's work is checked by whoever delegated it, and a quiet run
	// has no one to report to.
	if req.Quiet || req.Depth > 0 {
		return ""
	}

	maxChecks := a.cfg.Agent.VerifyMax
	if maxChecks <= 0 {
		maxChecks = 2
	}
	if *verified < maxChecks {
		if v := a.verify(ctx, req.Message, reply, history); v != nil {
			*verified++
			_ = emit(Event{Type: EventNotice, Message: "checking: " + v.Missing})
			return "Before finishing: " + v.Missing +
				" Do that now, or explain why it cannot be done."
		}
	}

	if !hasGoal || goal == nil {
		return ""
	}
	maxIter := goal.Max
	if maxIter <= 0 {
		maxIter = a.cfg.Agent.GoalMaxIterations
	}
	if maxIter <= 0 {
		maxIter = 10
	}
	if goal.Iterations >= maxIter || *judged >= maxIter {
		_ = emit(Event{Type: EventNotice, Message: "goal paused: iteration limit reached"})
		goal.Paused = true
		goal.Note = "Paused after reaching the iteration limit."
		_ = a.SetGoal(ctx, sess.ID, goal)
		return ""
	}

	j := a.judgeGoal(ctx, goal, reply, history)
	*judged++
	goal.Iterations++
	if j.Done {
		goal.Done = true
		goal.Note = "Met."
		_ = a.SetGoal(ctx, sess.ID, goal)
		_ = emit(Event{Type: EventNotice, Message: "goal met"})
		return ""
	}
	_ = a.SetGoal(ctx, sess.ID, goal)
	_ = emit(Event{Type: EventNotice, Message: "goal: " + j.Next})
	return "The standing goal is not met yet. " + j.Next
}

// ---- checkpoints -------------------------------------------------------------

// saveCheckpoint keeps a copy of a file before a tool changes it. A failure
// here must not stop the edit — losing the ability to undo is bad, refusing to
// work is worse.
func (a *Agent) saveCheckpoint(sessionID, path, tool, marker string) {
	if a.checks == nil {
		return
	}
	if err := a.checks.Save(sessionID, path, tool, marker); err != nil {
		slog.Debug("could not checkpoint a file", "path", path, "error", err)
	}
}

// Checkpoint reports what a session has changed so far.
func (a *Agent) Checkpoint(sessionID string) (*checkpoint.Checkpoint, error) {
	if a.checks == nil {
		return nil, errors.New("checkpoints are not enabled")
	}
	return a.checks.Load(sessionID)
}

// Rollback restores the files a session changed. Passing paths restores only
// those.
func (a *Agent) Rollback(sessionID string, paths []string) (*checkpoint.RestoreResult, error) {
	if a.checks == nil {
		return nil, errors.New("checkpoints are not enabled")
	}
	return a.checks.Restore(sessionID, paths)
}

// PreviewChangesSince lists files a session changed at/after a message marker,
// for the "edit message → revert?" prompt.
func (a *Agent) PreviewChangesSince(sessionID, marker string) ([]checkpoint.ChangedSince, error) {
	if a.checks == nil {
		return nil, errors.New("checkpoints are not enabled")
	}
	return a.checks.PreviewSince(sessionID, marker)
}

// RollbackSince reverts files changed at/after a message marker to their state
// just before it, optionally skipping files edited outside the session.
func (a *Agent) RollbackSince(sessionID, marker string, skipExternal bool) (*checkpoint.RestoreResult, error) {
	if a.checks == nil {
		return nil, errors.New("checkpoints are not enabled")
	}
	return a.checks.RestoreSince(sessionID, marker, skipExternal)
}

// PruneCheckpoints drops the ones older than the given age.
func (a *Agent) PruneCheckpoints(olderThan time.Duration) (int, error) {
	if a.checks == nil {
		return 0, nil
	}
	return a.checks.Prune(olderThan)
}

// ---- plugins -----------------------------------------------------------------

// SetPlugins attaches the plugin manager. Passing nil disables hooks.
func (a *Agent) SetPlugins(m *plugin.Manager) { a.plugins = m }

// SetSocialBrowser attaches the persistent social media browser manager.
func (a *Agent) SetSocialBrowser(m tools.SocialBrowserManager) { a.socialBrowser = m }

// Plugins exposes the manager, or nil when none is attached.
func (a *Agent) Plugins() *plugin.Manager { return a.plugins }

// notifyPlugins sends an event that has no reply worth acting on.
func (a *Agent) notifyPlugins(ctx context.Context, p plugin.Payload) {
	if a.plugins == nil {
		return
	}
	a.plugins.Dispatch(ctx, p)
}

// ---- multi-model panel -------------------------------------------------------

// PanelAnswer is one model's response to a panel question.
type PanelAnswer struct {
	Model  string `json:"model"`
	Answer string `json:"answer"`
	Err    string `json:"error,omitempty"`
}

const synthesisPrompt = `Several assistants answered the same question independently. Write the
answer the user should actually get.

Where they agree, say it once and plainly. Where they disagree, say so and give
the reading best supported by the reasoning — do not average them into
something none of them said. If one of them is clearly right and the others
missed something, follow that one and say why.

Do not mention that there were several answers, and do not name them. Write the
final answer as though it were the only one.`

// Panel asks several models the same question and synthesises one answer.
//
// The value is in the disagreement: where independent answers diverge is
// usually where the question was ambiguous or the problem is genuinely hard,
// and that is exactly what a single sample hides.
func (a *Agent) Panel(ctx context.Context, question string, models []string, emit Emit) (string, []PanelAnswer, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", nil, errors.New("ask something")
	}
	if len(models) == 0 {
		models = a.cfg.Model.Panel
	}
	if len(models) == 0 {
		return "", nil, errors.New("no panel is configured — set model.panel to two or more model ids")
	}
	if len(models) > 5 {
		// Past a handful the cost climbs and the answers stop diverging.
		models = models[:5]
	}
	if emit == nil {
		emit = func(Event) error { return nil }
	}

	answers := make([]PanelAnswer, len(models))
	var wg sync.WaitGroup
	for i, model := range models {
		wg.Add(1)
		go func(i int, model string) {
			defer wg.Done()
			answers[i] = PanelAnswer{Model: model}
			// Quiet: these are internal runs, not conversations of their own.
			res, err := a.Run(ctx, Request{
				Message:  question,
				Model:    model,
				Toolset:  "minimal",
				MaxTurns: 4,
				Quiet:    true,
				Depth:    1,
			}, nil)
			if err != nil {
				answers[i].Err = err.Error()
				return
			}
			answers[i].Answer = res.Reply
		}(i, model)
	}
	wg.Wait()

	var usable []PanelAnswer
	for _, ans := range answers {
		if ans.Err == "" && strings.TrimSpace(ans.Answer) != "" {
			usable = append(usable, ans)
			_ = emit(Event{Type: EventNotice, Message: ans.Model + " answered"})
		} else if ans.Err != "" {
			_ = emit(Event{Type: EventNotice, Message: ans.Model + " failed: " + ans.Err})
		}
	}
	if len(usable) == 0 {
		return "", answers, errors.New("no model in the panel answered")
	}
	// One usable answer is not a panel; synthesising it would only cost a call
	// to paraphrase it.
	if len(usable) == 1 {
		return usable[0].Answer, answers, nil
	}

	client, model, _, err := a.newAuxClient("")
	if err != nil {
		return usable[0].Answer, answers, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "QUESTION\n%s\n\n", truncate(question, 4000))
	for i, ans := range usable {
		fmt.Fprintf(&b, "ANSWER %d\n%s\n\n", i+1, truncate(ans.Answer, 6000))
	}

	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		System:      synthesisPrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: b.String()}},
		Temperature: 0.2,
		MaxTokens:   4000,
	})
	if err != nil {
		return usable[0].Answer, answers, nil
	}
	return strings.TrimSpace(resp.Content), answers, nil
}

// ---- roles -------------------------------------------------------------------

// SetRoles attaches the role registry.
func (a *Agent) SetRoles(r *roles.Registry) { a.roles = r }

// Roles exposes the registry.
func (a *Agent) Roles() *roles.Registry { return a.roles }

// applyRole folds a named role's prompt, toolset, and model into the request.
// The role's values fill only what the request left blank, so an explicit
// override on the request wins.
func (a *Agent) applyRole(req *Request) {
	if a.roles == nil || strings.TrimSpace(req.Role) == "" {
		return
	}
	role, ok := a.roles.Get(req.Role)
	if !ok {
		return
	}
	if role.Prompt != "" {
		if req.SystemExtra != "" {
			req.SystemExtra = role.Prompt + "\n\n" + req.SystemExtra
		} else {
			req.SystemExtra = role.Prompt
		}
	}
	if req.Toolset == "" && role.Toolset != "" {
		req.Toolset = role.Toolset
	}
	if req.Model == "" && role.Model != "" {
		req.Model = role.Model
	}
	if req.ReasoningEffort == "" && role.Effort != "" {
		req.ReasoningEffort = role.Effort
	}
	if req.MaxTurns == 0 && role.MaxTurns > 0 {
		req.MaxTurns = role.MaxTurns
	}
}

// roleInfos exposes the roles to the tools layer.
func (a *Agent) roleInfos() []tools.RoleInfo {
	if a.roles == nil {
		return nil
	}
	list := a.roles.List()
	out := make([]tools.RoleInfo, 0, len(list))
	for _, r := range list {
		out = append(out, tools.RoleInfo{
			Name: r.Name, Summary: r.Summary, Category: r.Category, Danger: r.Danger,
			Subrole: r.Subrole, Parent: r.Parent,
		})
	}
	return out
}

// Findings exposes the engagement ledger.
func (a *Agent) Findings() *findings.Store { return a.findings }

// Intel exposes the engagement's fact ledger.
func (a *Agent) Intel() *engagement.Store { return a.intel }

// RolePerformance exposes the per-role performance tracker.
func (a *Agent) RolePerformance() *roleperf.Tracker { return a.roleperf }

// ---- swarm visibility --------------------------------------------------------

// ActiveAgent is one sub-agent running right now.
type ActiveAgent struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Task      string    `json:"task"`
	Parent    string    `json:"parent"`
	StartedAt time.Time `json:"started_at"`
}

var swarm = struct {
	mu     sync.Mutex
	agents map[string]ActiveAgent
	seq    int
}{agents: map[string]ActiveAgent{}}

// trackSubAgent registers a running sub-agent, opens its live event stream, and
// returns the assigned id plus a function to deregister it. The registry is what
// a swarm panel reads to show who is working; the stream is what the panel taps
// to watch a sub-agent's output live. The id ties the two together, so listing
// the active agents gives a client exactly the ids it can attach to.
func trackSubAgent(role, task, parent string) (string, func()) {
	swarm.mu.Lock()
	swarm.seq++
	id := "sub-" + strconv.Itoa(swarm.seq)
	swarm.agents[id] = ActiveAgent{
		ID: id, Role: firstNonEmpty(role, "assistant"),
		Task: truncate(task, 120), Parent: parent, StartedAt: nowFunc(),
	}
	swarm.mu.Unlock()
	openSubStream(id)
	notifySwarm()
	return id, func() {
		swarm.mu.Lock()
		delete(swarm.agents, id)
		swarm.mu.Unlock()
		closeSubStream(id)
		notifySwarm()
	}
}

// ActiveAgents lists the sub-agents running now, oldest first.
func (a *Agent) ActiveAgents() []ActiveAgent {
	return a.ActiveAgentsFor("")
}

// ActiveAgentsFor lists the sub-agents running now for one delegating session,
// oldest first. An empty parent returns all of them (the process-wide view). A
// non-empty parent scopes the list so one chat only sees the sub-agents IT
// spawned — otherwise a workers running for an old session leak into a new one.
func (a *Agent) ActiveAgentsFor(parent string) []ActiveAgent {
	swarm.mu.Lock()
	defer swarm.mu.Unlock()
	out := make([]ActiveAgent, 0, len(swarm.agents))
	for _, ag := range swarm.agents {
		if parent != "" && ag.Parent != parent {
			continue
		}
		out = append(out, ag)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// nowFunc is time.Now, indirected so a test could pin it. The engine forbids a
// literal time.Now in some contexts, but this is ordinary runtime code.
var nowFunc = time.Now

// describeImage sends an image to a vision-capable model and returns its
// description. It is what the vision tool calls. The model is model.vision when
// set, otherwise the default model — which fails cleanly if that model has no
// eyes.
func (a *Agent) describeImage(ctx context.Context, data []byte, mime, question string) (string, error) {
	model := strings.TrimSpace(a.cfg.Model.Vision)
	client, resolved, _, err := a.newClient(model, "")
	if err != nil {
		return "", err
	}
	if question == "" {
		question = "Describe this image in detail. If it contains text, transcribe it."
	}
	if mime == "" {
		mime = "image/png"
	}
	resp, err := client.Chat(ctx, llm.Request{
		Model: resolved,
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: question,
			Parts: []llm.Part{{
				Type: "image", MimeType: mime,
				Data: base64.StdEncoding.EncodeToString(data),
			}},
		}},
		MaxTokens: 1500,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// audioClient builds a raw (unwrapped) client that supports the audio endpoints.
// Audio needs an OpenAI-compatible provider; anything else returns a clear error.
func (a *Agent) audioClient() (llm.AudioClient, error) {
	id, p := a.cfg.ResolveProvider(a.cfg.Model.Provider)
	client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: p.Headers, ProviderID: id, Timeout: 2 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	ac, ok := client.(llm.AudioClient)
	if !ok {
		return nil, fmt.Errorf("the %s provider has no audio support — set an OpenAI-compatible provider for voice", id)
	}
	return ac, nil
}

// speak synthesises speech from text.
func (a *Agent) speak(ctx context.Context, text, voice string) ([]byte, string, error) {
	ac, err := a.audioClient()
	if err != nil {
		return nil, "", err
	}
	if voice == "" {
		voice = firstNonEmpty(a.cfg.Model.Voice, "alloy")
	}
	return ac.Speak(ctx, a.cfg.Model.TTS, voice, "mp3", text)
}

// transcribe turns speech audio into text.
func (a *Agent) transcribe(ctx context.Context, filename string, audio []byte) (string, error) {
	ac, err := a.audioClient()
	if err != nil {
		return "", err
	}
	return ac.Transcribe(ctx, a.cfg.Model.STT, filename, audio)
}
