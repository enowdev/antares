package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/plugin"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

type toolOutcome struct {
	message llm.Message
	isError bool
}

// appendTurnMessages assembles the tail of one turn: every tool result first,
// then the repetition nudge, then any steering note. The order is the whole
// point. A user message sitting between an assistant's tool_calls and their
// results is not a valid transcript, and ensureToolResults repairing it at send
// time is no reason to write it — the repair is silent, so a nudge that drifts
// back above the results would leave every test green while the transcript we
// build is wrong. Keeping the order in one pure function is what makes it
// assertable without a client, a store or a server.
func appendTurnMessages(history []llm.Message, results []toolOutcome, nudge string, notes []string) []llm.Message {
	for _, r := range results {
		history = append(history, r.message)
	}
	if nudge != "" {
		history = append(history, llm.Message{Role: llm.RoleUser, Content: nudge})
	}
	for _, note := range notes {
		history = append(history, llm.Message{
			Role:    llm.RoleUser,
			Content: "A new instruction arrived while you were working: " + note,
		})
	}
	return history
}

// executeTools runs the requested calls, in parallel when the config allows.
func (a *Agent) executeTools(
	ctx context.Context,
	calls []llm.ToolCall,
	byName map[string]tools.Tool,
	req Request,
	sess *store.Session,
	emit Emit,
) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))

	// Emit all call announcements up front so the UI can render them in order.
	for _, call := range calls {
		_ = emit(Event{Type: EventToolCall, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}

	// Serialise emits: tools run concurrently but events must not interleave
	// mid-write.
	var emitMu sync.Mutex
	safeEmit := func(e Event) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		return emit(e)
	}

	run := func(i int, call llm.ToolCall) {
		tool, ok := byName[call.Name]
		if !ok {
			outcomes[i] = toolOutcome{
				message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
					Content: fmt.Sprintf("Tool %q is not available. Active tools: %s", call.Name, strings.Join(namesOf(byName), ", ")),
				},
				isError: true,
			}
			_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: outcomes[i].message.Content, IsError: true})
			return
		}
		if req.Platform == "cron" {
			mode := strings.ToLower(strings.TrimSpace(a.config().Tools.ApprovalMode))
			needsHuman := call.Name == "ask_user" || (mode != "auto" && mode != "deny" &&
				(tools.NeedsApproval(tool) || dangerInTool(tool, call.Arguments) != ""))
			if needsHuman {
				content := "Scheduled run blocked: this action requires human approval. Run it interactively or explicitly configure unattended execution."
				outcomes[i] = toolOutcome{message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: content,
				}, isError: true}
				_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: content, IsError: true})
				return
			}
		}

		// A subordinate run (delegated sub-agent, background task, continued
		// sub-session) has no user watching. ask_user is dropped from its
		// schema, but a model that hallucinates it anyway would otherwise
		// register a pending ask and block the parent forever. Refuse it
		// before checkApproval so no ask desk entry ever gets created.
		if isSubordinateRun(req) && call.Name == "ask_user" {
			content := "ask_user is not available to a subordinate run — nobody is watching this stream. " +
				"Make the most reasonable assumption a competent professional would make, act on it, and " +
				"state the assumption in your final reply. If you truly cannot proceed, end with a short " +
				"statement to the parent describing the blocker."
			outcomes[i] = toolOutcome{
				message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
					Content: content,
				},
				isError: true,
			}
			_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: content, IsError: true})
			return
		}

		// Snapshot the live-replaceable services once for this call. A reload
		// (SetPlugins / SetRAG) between the nil check and the Dispatch / Deps
		// wiring would otherwise let the pre-hook see one manager and the
		// post-hook see another, or hand tools a stale RAG provider.
		plugins := a.Plugins()
		ragProvider := a.RAG()

		// A tool that changes something may need a person to say yes first.
		if refusal := a.checkApproval(ctx, call, tool, sess.ID, safeEmit); refusal != nil {
			outcomes[i] = toolOutcome{
				message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
					Content: refusal.Content,
				},
				isError: true,
			}
			_ = safeEmit(Event{
				Type: EventToolResult, ID: call.ID, Name: call.Name,
				Content: refusal.Content, IsError: true,
			})
			return
		}

		// Plugins see the call before it runs, and may refuse it or change
		// its arguments.
		if plugins != nil {
			hook := plugins.Dispatch(ctx, plugin.Payload{
				Event: plugin.PreToolCall, SessionID: sess.ID, Platform: req.Platform,
				Tool: call.Name, Arguments: call.Arguments,
			})
			if hook.Notice != "" {
				_ = safeEmit(Event{Type: EventNotice, Message: hook.Notice})
			}
			if hook.Deny {
				content := "refused by policy: " + hook.Reason
				outcomes[i] = toolOutcome{
					message: llm.Message{
						Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
						Content: content,
					},
					isError: true,
				}
				_ = safeEmit(Event{
					Type: EventToolResult, ID: call.ID, Name: call.Name,
					Content: content, IsError: true,
				})
				return
			}
			if hook.Arguments != "" {
				call.Arguments = hook.Arguments
			}
		}

		workspace := sess.Workspace
		if workspace == "" {
			workspace = a.config().Agent.Workspace
		}
		// A project session confines writes to the project folder plus the
		// antares workspace, while allowing reads anywhere. Empty for an
		// ordinary session (reads and writes both stay in the workspace).
		var writeRoots []string
		if pd, _ := sess.Meta["project_dir"].(string); strings.TrimSpace(pd) != "" {
			writeRoots = []string{pd}
			if aw := a.config().Agent.Workspace; aw != "" && aw != pd {
				writeRoots = append(writeRoots, aw)
			}
		}
		in := tools.Input{
			Args:       json.RawMessage(call.Arguments),
			CallID:     call.ID,
			SessionID:  sess.ID,
			UserID:     req.UserID,
			Platform:   req.Platform,
			Workspace:  workspace,
			WriteRoots: writeRoots,
			Emit: func(p tools.Progress) {
				_ = safeEmit(Event{
					Type: EventToolProgress, ID: call.ID, Name: call.Name,
					Chunk: p.Chunk, Message: p.Message,
				})
			},
			AskUser: a.askBridge(sess.ID, safeEmit),
			Deps: &tools.Deps{
				Config: a.config(), Store: a.db, RAG: ragProvider, Shell: a.shell,
				Sub: a.subAgentFor(req), Tasks: a.backgroundFor(req), Skills: a.skillLibrary(sess),
				SocialBrowser: a.socialBrowser,
				Checkpoint: func(sessionID, path, tool string) {
					a.saveCheckpoint(sessionID, path, tool, req.turnMarker)
				},
				RecordResult: func(sessionID, path, resultHash string) {
					if a.checks != nil {
						_ = a.checks.RecordResult(sessionID, path, req.turnMarker, resultHash)
					}
					// Keep a RAG-indexed project's collection fresh: re-embed the
					// file the agent just wrote. No-op unless this is an indexed
					// project session and the file is inside it.
					if indexed, _ := sess.Meta["rag_indexed"].(bool); indexed {
						a.reindexFile(sess.ID, req.ProjectDir, path)
					}
				},
				Roles:      a.roleInfos,
				Vision:     a.describeImage,
				Speak:      a.speak,
				Board:      a.board,
				Transcribe: a.transcribe,
				Findings:   a.findings,
				Intel:      a.intel,
			},
		}

		// ask_user blocks on a person and has no deadline; every other tool runs
		// under its timeout. The parent ctx still cancels ask_user on stop/close.
		toolCtx, cancel := ctx, func() {}
		if call.Name != "ask_user" {
			toolCtx, cancel = context.WithTimeout(ctx, a.toolTimeout(call.Name))
		}
		defer cancel()

		start := time.Now()
		res := tool.Execute(toolCtx, in)
		content := trimForModel(res.Content, a.config().Tools.MaxOutputChars)
		if content == "" {
			content = "(tool produced no output)"
		}
		slog.Debug("tool executed", "tool", call.Name, "ms", time.Since(start).Milliseconds(), "error", res.IsError)

		// Plugins see the result and may replace what the model is shown —
		// redacting a secret out of a log, for instance.
		if plugins != nil {
			hook := plugins.Dispatch(ctx, plugin.Payload{
				Event: plugin.PostToolCall, SessionID: sess.ID, Platform: req.Platform,
				Tool: call.Name, Arguments: call.Arguments,
				Result: content, IsError: res.IsError,
			})
			if hook.Notice != "" {
				_ = safeEmit(Event{Type: EventNotice, Message: hook.Notice})
			}
			if hook.Result != "" {
				content = hook.Result
			}
		}

		// What the model sees may be fenced as untrusted; what the UI shows stays
		// raw. Errors are our own messages, so they are never fenced.
		modelContent := content
		if !res.IsError && a.config().Agent.WrapUntrustedOutput && untrustedTool(tool) {
			modelContent = wrapUntrusted(call.Name, content)
		}

		outcomes[i] = toolOutcome{
			message: llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: modelContent},
			isError: res.IsError,
		}
		_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: content, IsError: res.IsError})
	}

	// recoverRun wraps run() with panic recovery so a panicking tool cannot
	// deadlock wg.Wait() (parallel) or kill the turn without a user-visible
	// error (serial). The panic is logged, surfaced as an error tool result,
	// and the model gets a chance to recover.
	recoverRun := func(i int, call llm.ToolCall) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("tool panicked", "tool", call.Name, "panic", r, "stack", string(debug.Stack()))
				outcomes[i] = toolOutcome{
					message: llm.Message{
						Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
						Content: fmt.Sprintf("Tool %q panicked: %v", call.Name, r),
					},
					isError: true,
				}
				_ = safeEmit(Event{
					Type: EventToolResult, ID: call.ID, Name: call.Name,
					Content: outcomes[i].message.Content, IsError: true,
				})
			}
		}()
		run(i, call)
	}

	parallel := a.config().Model.ParallelToolCall && len(calls) > 1
	if parallel {
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxParallelTools)
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call llm.ToolCall) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				recoverRun(i, call)
			}(i, call)
		}
		wg.Wait()
		return outcomes
	}
	for i, call := range calls {
		recoverRun(i, call)
	}
	return outcomes
}

const maxParallelTools = 4

func (a *Agent) toolTimeout(name string) time.Duration {
	if secs, ok := a.config().Tools.Timeouts[name]; ok && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	switch name {
	case "terminal":
		return time.Duration(maxInt(a.config().Terminal.Timeout, 60)) * time.Second
	case "process":
		// process(wait) intentionally blocks for at most 30 seconds. Leave margin
		// for scheduling and JSON serialization so the tool can return its state.
		return 45 * time.Second
	case "vps_run", "vps_upload", "vps_download":
		// Tools accept timeout_seconds up to 900. The agent envelope must sit
		// above that or a long systemctl/apt/transfer is killed early with a
		// bare context deadline and looks like a flaky VPS failure.
		return 16 * time.Minute
	case "delegate_task":
		return 30 * time.Minute
	default:
		return 5 * time.Minute
	}
}
