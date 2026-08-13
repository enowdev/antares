package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/approval"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

// Approval gates tools that change something. Ordinary tools follow
// `tools.approval_mode`: run, ask, or refuse. Explicit operations such as
// mutating Cursor calls override auto and still ask, while deny remains an
// immediate refusal and never creates a pending approval.
//
// Asking only works where a person is watching. A cron job or a messaging
// thread has nobody to answer, so a request that goes unanswered is refused
// when its deadline passes — the failure mode has to be "did not happen",
// never "happened without being asked".

// ApprovalRequest preserves the public agent API while the gate itself lives
// in the approval package.
type ApprovalRequest = approval.Request

// PendingApprovals lists what is waiting, oldest first.
func (a *Agent) PendingApprovals() []ApprovalRequest {
	return a.approvalGate().Pending()
}

// ResolveApproval answers a pending request. It reports false when the id is
// unknown, which usually means it already timed out.
func (a *Agent) ResolveApproval(id string, allow bool) bool {
	return a.approvalGate().Resolve(id, allow)
}

func (a *Agent) approvalGate() *approval.Gate {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.approvals == nil {
		a.approvals = approval.NewGate(approvalTimeout)
	}
	return a.approvals
}

// approvalTimeout is how long a request waits before being refused.
const approvalTimeout = 5 * time.Minute

// checkApproval decides whether a call may proceed. It returns an error result
// to hand back to the model when it may not.
func (a *Agent) checkApproval(ctx context.Context, call llm.ToolCall, tool tools.Tool, sessionID string, emit Emit) *tools.Result {
	mode := strings.ToLower(strings.TrimSpace(a.config().Tools.ApprovalMode))
	if mode == "" {
		mode = "auto"
	}
	danger := dangerIn(call.Name, call.Arguments)
	explicit, explicitApproval := tool.(tools.OperationApproval)

	if mode == "deny" {
		if explicitApproval || tools.NeedsApproval(tool) || danger != "" {
			return denyApproval(call.Name)
		}
		return nil
	}

	if explicitApproval {
		op, err := explicit.ApprovalOperation(json.RawMessage(call.Arguments), sessionID)
		if err != nil {
			res := tools.Errorf("%v", err)
			return &res
		}
		if op.Message == "" {
			op.Message = approvalMessage(op)
		}
		return a.awaitApproval(ctx, op, emit)
	}

	switch mode {
	case "auto":
		// The mode says run it, so it runs. A destructive command is still
		// worth naming in the transcript — silently doing it is the thing
		// nobody wants to discover afterwards.
		if danger != "" {
			_ = emit(Event{Type: EventNotice, Message: "running a command that " + danger})
		}
		return nil

	case "prompt":
		if !tools.NeedsApproval(tool) && danger == "" {
			return nil
		}

	default:
		// An unrecognised mode is treated as the safest one that still works.
		if !tools.NeedsApproval(tool) && danger == "" {
			return nil
		}
	}

	op := approval.Operation{
		SessionID: sessionID,
		Tool:      call.Name,
		Arguments: call.Arguments,
		Reason:    danger,
	}
	op.Message = approvalMessage(op)
	return a.awaitApproval(ctx, op, emit)
}

func denyApproval(toolName string) *tools.Result {
	result := tools.Errorf("refused: %s changes state and tools.approval_mode is \"deny\". "+
		"Report what you would have done instead.", toolName)
	return &result
}

func (a *Agent) awaitApproval(ctx context.Context, op approval.Operation, emit Emit) *tools.Result {
	allow, err := a.approvalGate().Await(ctx, op, func(req approval.Request) error {
		payload, marshalErr := json.Marshal(req)
		if marshalErr != nil {
			return marshalErr
		}
		return emit(Event{
			Type:      EventApproval,
			ID:        req.ID,
			Name:      req.Tool,
			Arguments: req.Arguments,
			Message:   req.Message,
			Content:   string(payload),
		})
	})
	if err == nil {
		if allow {
			_ = emit(Event{Type: EventNotice, Message: "approved " + op.Tool})
			return nil
		}
		res := tools.Errorf("the user refused this %s call. Do not retry it; "+
			"ask what to do instead, or continue without it.", op.Tool)
		return &res
	}
	if errors.Is(err, approval.ErrTimeout) {
		res := tools.Errorf("no one approved this %s call within %s, so it did not run. "+
			"Say what you were about to do and stop.", op.Tool, approvalTimeout)
		return &res
	}
	res := tools.Errorf("interrupted before %s was approved", op.Tool)
	return &res
}

func approvalMessage(op approval.Operation) string {
	if op.Message != "" {
		return op.Message
	}
	if op.Reason != "" {
		return fmt.Sprintf("%s wants to run, and %s", op.Tool, op.Reason)
	}
	return op.Tool + " wants to change something"
}

// dangerous names commands that are worth stopping for even when approval is
// otherwise off. Each entry says, in words that finish "…and it", what the
// command does — the message is only useful if it explains the risk.
var dangerous = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/|~|\$HOME|\*)(\s|$)`), "it deletes a whole tree from your home or root"},
	{regexp.MustCompile(`\bmkfs(\.\w+)?\b`), "it formats a filesystem"},
	{regexp.MustCompile(`\bdd\b[^\n]*\bof=/dev/`), "it writes directly to a device"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|hd)`), "it writes directly to a disk"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\|\s*:&\s*\}\s*;\s*:`), "it is a fork bomb"},
	{regexp.MustCompile(`\bchmod\s+(-R\s+)?777\s+/`), "it makes system paths world-writable"},
	{regexp.MustCompile(`\bgit\s+push\b[^\n]*(--force|-f)\b`), "it force-pushes, which can destroy history others have"},
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "it discards uncommitted work"},
	{regexp.MustCompile(`\b(DROP|TRUNCATE)\s+(TABLE|DATABASE|SCHEMA)\b`), "it drops a database object"},
	{regexp.MustCompile(`\bcurl\b[^\n|]*\|\s*(sudo\s+)?(ba)?sh`), "it pipes a download straight into a shell"},
	{regexp.MustCompile(`\bwget\b[^\n|]*\|\s*(sudo\s+)?(ba)?sh`), "it pipes a download straight into a shell"},
	{regexp.MustCompile(`\bsudo\b`), "it runs as root"},
}

// dangerIn reports why a call is destructive, or an empty string when it is
// ordinary. Only the terminal is scanned: it is the tool that can do anything.
func dangerIn(toolName, arguments string) string {
	if toolName != "terminal" {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return ""
	}
	cmd := args.Command
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	for _, d := range dangerous {
		if d.re.MatchString(cmd) {
			return d.why
		}
	}
	return ""
}
