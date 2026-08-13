package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/approval"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
)

type cursorAgentTool struct{}

func (cursorAgentTool) Name() string { return "cursor_agent" }

func (cursorAgentTool) Description() string {
	return "Start, continue, or cancel a Cursor Cloud Agent run. " +
		"Use cursor_agent_status for read-only snapshots or to wait on an existing run."
}

func (cursorAgentTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":                propEnum("Operation to perform.", "start", "follow_up", "cancel"),
		"prompt":                prop("string", "Task for start/follow_up."),
		"agent_id":              prop("string", "Cursor bc- agent id for follow_up/cancel."),
		"run_id":                prop("string", "Cursor run- id for cancel."),
		"model":                 prop("string", "Optional model id returned by Cursor."),
		"repository_url":        prop("string", "Optional HTTPS GitHub repository URL."),
		"starting_ref":          prop("string", "Optional branch or commit SHA."),
		"pull_request_url":      prop("string", "Optional GitHub pull request URL."),
		"mode":                  propEnum("Cursor conversation mode.", "agent", "plan"),
		"auto_create_pr":        propDefault("boolean", "Open a PR when the run completes.", false),
		"skip_reviewer_request": propDefault("boolean", "Do not request the key owner as reviewer.", true),
		"wait":                  propDefault("boolean", "Stream until terminal status.", true),
	}, "action")
}

func (cursorAgentTool) RequiresApproval() bool { return true }

func (cursorAgentTool) ApprovalOperation(raw json.RawMessage, sessionID string) (approval.Operation, error) {
	var args cursorAgentArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return approval.Operation{}, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	args.trim()
	if err := validateCursorAgentArgs(args); err != nil {
		return approval.Operation{}, err
	}

	projection := cursorAgentApprovalProjection{
		Action:         boundCursorApprovalField(args.Action),
		AgentID:        boundCursorApprovalField(args.AgentID),
		RunID:          boundCursorApprovalField(args.RunID),
		Model:          boundCursorApprovalField(args.Model),
		RepositoryURL:  boundCursorApprovalField(args.RepositoryURL),
		StartingRef:    boundCursorApprovalField(args.StartingRef),
		PullRequestURL: boundCursorApprovalField(args.PullRequestURL),
		Mode:           boundCursorApprovalField(args.Mode),
	}
	switch args.Action {
	case "start":
		autoCreatePR := args.AutoCreatePR
		skipReviewer := true
		if args.SkipReviewerRequest != nil {
			skipReviewer = *args.SkipReviewerRequest
		}
		wait := true
		if args.Wait != nil {
			wait = *args.Wait
		}
		projection.AutoCreatePR = &autoCreatePR
		projection.SkipReviewerRequest = &skipReviewer
		projection.Wait = &wait
	case "follow_up":
		wait := true
		if args.Wait != nil {
			wait = *args.Wait
		}
		projection.Wait = &wait
	}

	display, err := json.Marshal(projection)
	if err != nil {
		return approval.Operation{}, fmt.Errorf("build approval projection: %w", err)
	}
	return approval.Operation{
		SessionID: sessionID,
		Tool:      "cursor_agent",
		Arguments: string(display),
		Message:   cursorApprovalMessage(args.Action),
		Reason:    "Cursor operations are paid and change remote state",
	}, nil
}

type cursorAgentApprovalProjection struct {
	Action              string `json:"action"`
	AgentID             string `json:"agent_id,omitempty"`
	RunID               string `json:"run_id,omitempty"`
	Model               string `json:"model,omitempty"`
	RepositoryURL       string `json:"repository_url,omitempty"`
	StartingRef         string `json:"starting_ref,omitempty"`
	PullRequestURL      string `json:"pull_request_url,omitempty"`
	Mode                string `json:"mode,omitempty"`
	AutoCreatePR        *bool  `json:"auto_create_pr,omitempty"`
	SkipReviewerRequest *bool  `json:"skip_reviewer_request,omitempty"`
	Wait                *bool  `json:"wait,omitempty"`
}

func cursorApprovalMessage(action string) string {
	switch action {
	case "start":
		return "Start Cursor Cloud Agent run"
	case "follow_up":
		return "Continue Cursor Cloud Agent run"
	case "cancel":
		return "Cancel Cursor Cloud Agent run"
	default:
		return "Run Cursor Cloud Agent operation"
	}
}

func boundCursorApprovalField(value string) string {
	const maxRunes = 128
	value = strings.ToValidUTF8(value, "\uFFFD")
	if cursorKeyLikeToken.MatchString(value) {
		return "[REDACTED]"
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
}

var cursorKeyLikeToken = regexp.MustCompile(`(?i)crsr_[a-z0-9_-]+`)

type cursorAgentArgs struct {
	Action              string `json:"action"`
	Prompt              string `json:"prompt"`
	AgentID             string `json:"agent_id"`
	RunID               string `json:"run_id"`
	Model               string `json:"model"`
	RepositoryURL       string `json:"repository_url"`
	StartingRef         string `json:"starting_ref"`
	PullRequestURL      string `json:"pull_request_url"`
	Mode                string `json:"mode"`
	AutoCreatePR        bool   `json:"auto_create_pr"`
	SkipReviewerRequest *bool  `json:"skip_reviewer_request"`
	Wait                *bool  `json:"wait"`
}

func (a *cursorAgentArgs) trim() {
	a.Action = strings.TrimSpace(a.Action)
	a.Prompt = strings.TrimSpace(a.Prompt)
	a.AgentID = strings.TrimSpace(a.AgentID)
	a.RunID = strings.TrimSpace(a.RunID)
	a.Model = strings.TrimSpace(a.Model)
	a.RepositoryURL = strings.TrimSpace(a.RepositoryURL)
	a.StartingRef = strings.TrimSpace(a.StartingRef)
	a.PullRequestURL = strings.TrimSpace(a.PullRequestURL)
	a.Mode = strings.TrimSpace(a.Mode)
}

func (cursorAgentTool) Execute(ctx context.Context, in Input) Result {
	var args cursorAgentArgs
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	args.trim()
	if err := validateCursorAgentArgs(args); err != nil {
		return Errorf("%v", err)
	}

	client, provider, err := cursorClientFromInput(in)
	if err != nil {
		return safeCursorResultError(err, args.AgentID, args.RunID, provider.APIKey)
	}

	switch args.Action {
	case "start":
		return startCursorAgent(ctx, in, client, provider, args)
	case "follow_up":
		return followUpCursorAgent(ctx, in, client, provider, args)
	case "cancel":
		err := client.CancelRun(ctx, args.AgentID, args.RunID)
		if err != nil {
			return cursorOperationError(err, "run", args.AgentID, args.RunID, provider.APIKey)
		}
		agentID := redactCursorString(args.AgentID, provider.APIKey)
		runID := redactCursorString(args.RunID, provider.APIKey)
		return Result{
			Content: fmt.Sprintf("Cursor cancellation requested.\nagent_id: %s\nrun_id: %s", agentID, runID),
			Meta: map[string]any{
				"agent_id": agentID,
				"run_id":   runID,
				"status":   "cancel_requested",
			},
		}
	default:
		return Errorf("action must be start, follow_up, or cancel")
	}
}

func startCursorAgent(
	ctx context.Context,
	in Input,
	client *cursor.Client,
	provider config.Provider,
	args cursorAgentArgs,
) Result {
	wait := true
	if args.Wait != nil {
		wait = *args.Wait
	}
	skipReviewer := true
	if args.SkipReviewerRequest != nil {
		skipReviewer = *args.SkipReviewerRequest
	}

	repos := []cursor.Repository{}
	if args.RepositoryURL != "" {
		repos = append(repos, cursor.Repository{
			URL:         args.RepositoryURL,
			StartingRef: args.StartingRef,
			PRURL:       args.PullRequestURL,
		})
	}
	var model *cursor.ModelSelection
	if args.Model != "" {
		model = &cursor.ModelSelection{ID: args.Model}
	}

	created, err := client.CreateAgent(ctx, cursor.CreateAgentRequest{
		Prompt:              cursor.Prompt{Text: args.Prompt},
		Model:               model,
		Repos:               repos,
		AutoCreatePR:        args.AutoCreatePR,
		SkipReviewerRequest: skipReviewer,
		Mode:                args.Mode,
	})
	if err != nil {
		return cursorOperationError(err, "agent", "", "", provider.APIKey)
	}
	if created == nil {
		return cursorResultError(errors.New("Cursor returned an empty create response"), "", "")
	}
	if !wait {
		return cursorRunResult(created.Agent, created.Run, provider.APIKey, true)
	}

	waitCtx, cancel := cursorWaitContext(ctx, provider)
	defer cancel()
	return waitCursorRun(waitCtx, in, client, created.Agent, created.Run)
}

func followUpCursorAgent(
	ctx context.Context,
	in Input,
	client *cursor.Client,
	provider config.Provider,
	args cursorAgentArgs,
) Result {
	agent, err := client.GetAgent(ctx, args.AgentID)
	if err != nil {
		return cursorOperationError(err, "agent", args.AgentID, "", provider.APIKey)
	}
	run, err := client.CreateRun(ctx, args.AgentID, cursor.CreateRunRequest{
		Prompt: cursor.Prompt{Text: args.Prompt},
		Mode:   args.Mode,
	})
	if err != nil {
		return cursorOperationError(err, "agent", args.AgentID, "", provider.APIKey)
	}
	if agent == nil || run == nil {
		return safeCursorResultError(
			errors.New("Cursor returned an empty follow-up response"),
			args.AgentID,
			"",
			provider.APIKey,
		)
	}

	wait := true
	if args.Wait != nil {
		wait = *args.Wait
	}
	if !wait {
		return cursorRunResult(*agent, *run, provider.APIKey, true)
	}

	waitCtx, cancel := cursorWaitContext(ctx, provider)
	defer cancel()
	return waitCursorRun(waitCtx, in, client, *agent, *run)
}

func validateCursorAgentArgs(args cursorAgentArgs) error {
	switch args.Action {
	case "":
		return errors.New("action is required")
	case "start":
		if args.Prompt == "" {
			return errors.New("prompt is required for start")
		}
		if args.AgentID != "" {
			return errors.New("agent_id is not allowed for start")
		}
		if args.RunID != "" {
			return errors.New("run_id is not allowed for start")
		}
		if err := validateCursorMode(args.Mode); err != nil {
			return err
		}
		if err := validateCursorRepository(args.RepositoryURL); err != nil {
			return err
		}
		if args.StartingRef != "" && args.RepositoryURL == "" {
			return errors.New("repository_url is required when starting_ref is set")
		}
		if args.PullRequestURL != "" {
			if args.RepositoryURL == "" {
				return errors.New("repository_url is required when pull_request_url is set")
			}
			if err := validateCursorPullRequest(args.PullRequestURL); err != nil {
				return err
			}
		}
		if args.AutoCreatePR && args.RepositoryURL == "" {
			return errors.New("repository_url is required when auto_create_pr is true")
		}
		return nil
	case "follow_up":
		if args.AgentID == "" {
			return errors.New("agent_id is required for follow_up")
		}
		if err := validateCursorID(args.AgentID, "bc-", "agent_id"); err != nil {
			return err
		}
		if args.Prompt == "" {
			return errors.New("prompt is required for follow_up")
		}
		if args.RunID != "" {
			return errors.New("run_id is not allowed for follow_up")
		}
		if err := rejectCursorStartFields(args, "follow_up"); err != nil {
			return err
		}
		return validateCursorMode(args.Mode)
	case "cancel":
		if args.AgentID == "" {
			return errors.New("agent_id is required for cancel")
		}
		if args.RunID == "" {
			return errors.New("run_id is required for cancel")
		}
		if err := validateCursorID(args.AgentID, "bc-", "agent_id"); err != nil {
			return err
		}
		if err := validateCursorID(args.RunID, "run-", "run_id"); err != nil {
			return err
		}
		if args.Prompt != "" {
			return errors.New("prompt is not allowed for cancel")
		}
		if args.Mode != "" {
			return errors.New("mode is not allowed for cancel")
		}
		if args.Wait != nil {
			return errors.New("wait is not allowed for cancel")
		}
		if err := rejectCursorStartFields(args, "cancel"); err != nil {
			return err
		}
		return nil
	default:
		return errors.New("action must be start, follow_up, or cancel")
	}
}

func rejectCursorStartFields(args cursorAgentArgs, action string) error {
	switch {
	case args.Model != "":
		return fmt.Errorf("model is not allowed for %s", action)
	case args.RepositoryURL != "":
		return fmt.Errorf("repository_url is not allowed for %s", action)
	case args.StartingRef != "":
		return fmt.Errorf("starting_ref is not allowed for %s", action)
	case args.PullRequestURL != "":
		return fmt.Errorf("pull_request_url is not allowed for %s", action)
	case args.AutoCreatePR:
		return fmt.Errorf("auto_create_pr is not allowed for %s", action)
	case args.SkipReviewerRequest != nil:
		return fmt.Errorf("skip_reviewer_request is not allowed for %s", action)
	default:
		return nil
	}
}

func validateCursorMode(mode string) error {
	if mode == "" || mode == "agent" || mode == "plan" {
		return nil
	}
	return errors.New("mode must be agent or plan")
}

func validateCursorID(value, prefix, field string) error {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return fmt.Errorf("%s must be a Cursor %s id", field, prefix)
	}
	return nil
}

func cursorClientFromInput(in Input) (*cursor.Client, config.Provider, error) {
	if in.Deps == nil || in.Deps.Config == nil {
		return nil, config.Provider{}, errors.New("Cursor is unavailable in this runtime")
	}
	_, provider := in.Deps.Config.ResolveProvider("cursor")
	provider.APIKey = strings.TrimSpace(provider.APIKey)
	if !provider.Enabled || provider.APIKey == "" {
		return nil, provider, errors.New("connect Cursor in Providers or set CURSOR_API_KEY")
	}
	client, err := cursor.New(cursor.Options{
		BaseURL: provider.BaseURL,
		APIKey:  provider.APIKey,
	})
	return client, provider, err
}

func validateCursorRepository(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if _, err := parseCursorGitHubURL(raw); err != nil {
		return errors.New("repository_url must be an HTTPS GitHub URL")
	}
	return nil
}

func validateCursorPullRequest(raw string) error {
	parsed, err := parseCursorGitHubURL(raw)
	if err != nil {
		return errors.New("pull_request_url must be an HTTPS GitHub pull request URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return errors.New("pull_request_url must be an HTTPS GitHub pull request URL with a /pull/<number> path")
	}
	number, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || number == 0 {
		return errors.New("pull_request_url must be an HTTPS GitHub pull request URL with a /pull/<number> path")
	}
	return nil
}

func parseCursorGitHubURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil ||
		parsed.Scheme != "https" ||
		!strings.EqualFold(parsed.Host, "github.com") ||
		parsed.User != nil {
		return nil, errors.New("not an HTTPS GitHub URL")
	}
	return parsed, nil
}

func cursorWaitContext(ctx context.Context, provider config.Provider) (context.Context, context.CancelFunc) {
	timeout := time.Duration(provider.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return context.WithTimeout(ctx, timeout)
}

func waitCursorRun(
	ctx context.Context,
	in Input,
	client *cursor.Client,
	agent cursor.Agent,
	run cursor.Run,
) Result {
	agentID := agent.ID
	if agentID == "" {
		agentID = run.AgentID
	}
	runID := run.ID
	terminal, err := client.StreamRun(ctx, agentID, runID, func(event cursor.StreamEvent) error {
		emitCursorEvent(in, event)
		return nil
	})
	secret := cursorSecretFromInput(in)
	if err != nil {
		return cursorOperationError(err, "run", agentID, runID, secret)
	}
	if terminal == nil {
		return safeCursorResultError(errors.New("Cursor stream returned no run"), agentID, runID, secret)
	}
	if terminal.ID == "" {
		terminal.ID = runID
	}
	if terminal.AgentID == "" {
		terminal.AgentID = agentID
	}
	return cursorRunResult(agent, *terminal, secret, false)
}

func emitCursorEvent(in Input, event cursor.StreamEvent) {
	secret := cursorSecretFromInput(in)
	message := "Cursor " + redactCursorString(event.Type, secret)
	chunk := redactCursorString(event.Text, secret)
	if event.ToolName != "" {
		message = "Cursor tool " +
			redactCursorString(event.ToolName, secret) + " " +
			redactCursorString(event.Status, secret)
	}
	message = boundCursorProgress(message)
	chunk = boundCursorProgress(chunk)
	if in.Emit != nil {
		in.Emit(Progress{Tool: "cursor_agent", Message: message, Chunk: chunk})
	}
}

func boundCursorProgress(value string) string {
	const maxRunes = 2000
	value = strings.ToValidUTF8(value, "\uFFFD")
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
}

func cursorAgentStatusResult(
	ctx context.Context,
	in Input,
	client *cursor.Client,
	provider config.Provider,
	agent cursor.Agent,
	runID string,
	wait bool,
) Result {
	if wait {
		waitCtx, cancel := cursorWaitContext(ctx, provider)
		defer cancel()
		return waitCursorRun(waitCtx, in, client, agent, cursor.Run{ID: runID, AgentID: agent.ID})
	}
	run, err := client.GetRun(ctx, agent.ID, runID)
	if err != nil {
		return cursorOperationError(err, "run", agent.ID, runID, provider.APIKey)
	}
	if run == nil {
		return safeCursorResultError(
			errors.New("Cursor returned an empty run response"),
			agent.ID,
			runID,
			provider.APIKey,
		)
	}
	return cursorRunResult(agent, *run, provider.APIKey, true)
}

func cursorRunResult(agent cursor.Agent, run cursor.Run, secret string, discouragePolling bool) Result {
	agentID := agent.ID
	if agentID == "" {
		agentID = run.AgentID
	}
	status := run.Status
	if status == "" {
		status = agent.Status
	}
	git := run.Git
	if git == nil {
		git = agent.Git
	}

	agentID = redactCursorString(agentID, secret)
	runID := redactCursorString(run.ID, secret)
	status = redactCursorString(status, secret)
	cursorURL := redactCursorString(agent.URL, secret)
	resultText := redactCursorString(run.Result, secret)
	safeGit := sanitizeCursorGit(git, secret)

	meta := map[string]any{
		"agent_id":    agentID,
		"run_id":      runID,
		"status":      status,
		"cursor_url":  cursorURL,
		"duration_ms": run.DurationMS,
		"result":      resultText,
	}
	if safeGit != nil {
		meta["git"] = safeGit
	}

	var content strings.Builder
	content.WriteString("Cursor run\n")
	fmt.Fprintf(&content, "agent_id: %s\nrun_id: %s\nstatus: %s", agentID, runID, status)
	if cursorURL != "" {
		fmt.Fprintf(&content, "\ncursor_url: %s", cursorURL)
	}
	fmt.Fprintf(&content, "\nduration_ms: %d", run.DurationMS)
	if resultText != "" {
		fmt.Fprintf(&content, "\n\nResult:\n%s", resultText)
	}
	if safeGit != nil && len(safeGit.Branches) > 0 {
		content.WriteString("\n\nGit:")
		for _, branch := range safeGit.Branches {
			fmt.Fprintf(&content, "\n- %s", branch.RepoURL)
			if branch.Branch != "" {
				fmt.Fprintf(&content, " — %s", branch.Branch)
			}
			if branch.PRURL != "" {
				fmt.Fprintf(&content, " — PR: %s", branch.PRURL)
			}
		}
	}
	if discouragePolling {
		content.WriteString("\n\nDo not busy-poll this run. Call cursor_agent_status once later when you need a fresh snapshot.")
	}
	return Result{Content: content.String(), Meta: meta}
}

func sanitizeCursorGit(git *cursor.GitState, secret string) *cursor.GitState {
	if git == nil {
		return nil
	}
	safe := &cursor.GitState{Branches: make([]cursor.GitBranch, len(git.Branches))}
	for i, branch := range git.Branches {
		safe.Branches[i] = cursor.GitBranch{
			RepoURL: redactCursorString(branch.RepoURL, secret),
			Branch:  redactCursorString(branch.Branch, secret),
			PRURL:   redactCursorString(branch.PRURL, secret),
		}
	}
	return safe
}

func cursorSecretFromInput(in Input) string {
	if in.Deps == nil || in.Deps.Config == nil {
		return ""
	}
	_, provider := in.Deps.Config.ResolveProvider("cursor")
	return strings.TrimSpace(provider.APIKey)
}

func redactCursorString(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}

func sanitizeCursorError(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var apiErr *cursor.APIError
	if errors.As(err, &apiErr) {
		safe := *apiErr
		safe.Code = redactCursorString(safe.Code, secret)
		safe.Message = redactCursorString(safe.Message, secret)
		return &safe
	}
	return errors.New(redactCursorString(err.Error(), secret))
}

func cursorResultError(err error, agentID, runID string) Result {
	meta := map[string]any{"agent_id": agentID, "run_id": runID}
	switch {
	case cursor.IsAuthError(err):
		return Result{Content: "Cursor API key was rejected.", Meta: meta, IsError: true}
	case cursor.IsRateLimit(err):
		var apiErr *cursor.APIError
		_ = errors.As(err, &apiErr)
		if apiErr != nil && apiErr.RetryAfter > 0 {
			meta["retry_after_seconds"] = int(apiErr.RetryAfter.Seconds())
		}
		return Result{Content: "Cursor rate limit reached; retry later.", Meta: meta, IsError: true}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Result{
			Content: "Stopped waiting; the remote Cursor run may still be active. Use cursor_agent_status with the returned IDs.",
			Meta:    meta,
			IsError: true,
		}
	default:
		return Result{Content: "Cursor request failed: " + err.Error(), Meta: meta, IsError: true}
	}
}

func safeCursorResultError(err error, agentID, runID, secret string) Result {
	return cursorResultError(
		sanitizeCursorError(err, secret),
		redactCursorString(agentID, secret),
		redactCursorString(runID, secret),
	)
}

// cursorNotFoundLabel prefers Cursor's typed code over the caller's guess:
// cancelling a run whose agent is gone reports the agent, not the run. Only
// these fixed labels are returned, never the upstream code itself.
func cursorNotFoundLabel(err error, missingKind string) string {
	var apiErr *cursor.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.Code)) {
		case "agent_not_found":
			return "Cursor agent not found"
		case "run_not_found":
			return "Cursor run not found"
		}
	}
	if missingKind == "agent" {
		return "Cursor agent not found"
	}
	return "Cursor run not found"
}

func cursorOperationError(err error, missingKind, agentID, runID, secret string) Result {
	err = sanitizeCursorError(err, secret)
	agentID = redactCursorString(agentID, secret)
	runID = redactCursorString(runID, secret)
	meta := map[string]any{"agent_id": agentID, "run_id": runID}
	if cursor.IsStatus(err, http.StatusNotFound) {
		return Result{Content: cursorNotFoundLabel(err, missingKind) + ": " + err.Error(), Meta: meta, IsError: true}
	}
	if cursor.IsStatus(err, http.StatusConflict) {
		return Result{Content: err.Error(), Meta: meta, IsError: true}
	}
	return cursorResultError(err, agentID, runID)
}

type cursorAgentStatusTool struct{}

func (cursorAgentStatusTool) Name() string { return "cursor_agent_status" }

func (cursorAgentStatusTool) Description() string {
	return "Read a Cursor Cloud Agent run snapshot, or stream an existing run until it reaches a terminal status."
}

func (cursorAgentStatusTool) Schema() map[string]any {
	return schema(map[string]any{
		"agent_id": prop("string", "Cursor bc- agent id."),
		"run_id":   prop("string", "Optional run- id; latest run is used when omitted."),
		"wait":     propDefault("boolean", "Stream until terminal status.", false),
	}, "agent_id")
}

func (cursorAgentStatusTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		AgentID string `json:"agent_id"`
		RunID   string `json:"run_id"`
		Wait    *bool  `json:"wait"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	args.AgentID = strings.TrimSpace(args.AgentID)
	args.RunID = strings.TrimSpace(args.RunID)
	if args.AgentID == "" {
		return Errorf("agent_id is required")
	}
	if err := validateCursorID(args.AgentID, "bc-", "agent_id"); err != nil {
		return Errorf("%v", err)
	}
	if args.RunID != "" {
		if err := validateCursorID(args.RunID, "run-", "run_id"); err != nil {
			return Errorf("%v", err)
		}
	}

	client, provider, err := cursorClientFromInput(in)
	if err != nil {
		return safeCursorResultError(err, args.AgentID, args.RunID, provider.APIKey)
	}
	agent, err := client.GetAgent(ctx, args.AgentID)
	if err != nil {
		return cursorOperationError(err, "agent", args.AgentID, args.RunID, provider.APIKey)
	}
	if agent == nil {
		return safeCursorResultError(
			errors.New("Cursor returned an empty agent response"),
			args.AgentID,
			args.RunID,
			provider.APIKey,
		)
	}

	runID := args.RunID
	if runID == "" {
		runID = strings.TrimSpace(agent.LatestRunID)
		if runID == "" {
			return safeCursorResultError(
				errors.New("Cursor agent has no latest run"),
				args.AgentID,
				"",
				provider.APIKey,
			)
		}
		if err := validateCursorID(runID, "run-", "run_id"); err != nil {
			return safeCursorResultError(err, args.AgentID, runID, provider.APIKey)
		}
	}
	wait := false
	if args.Wait != nil {
		wait = *args.Wait
	}
	return cursorAgentStatusResult(ctx, in, client, provider, *agent, runID, wait)
}
