// Package cursorrun provides the shared Cursor catalogue and remote-run
// lifecycle used by both HTTP and tool adapters.
package cursorrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/cursor"
)

// Upstream payload limits are measured in Unicode runes so truncation never
// splits UTF-8. They are intentionally generous for normal Cursor responses
// while making every cached or returned value finite.
const (
	maxIdentifierRunes   = 1024
	maxMetadataRunes     = 16 * 1024
	maxContentRunes      = 1 << 20
	maxStreamRawRunes    = 1 << 20
	maxGenericErrorRunes = 4096
	maxProgressRunes     = 2000
	maxAgentRepositories = 64
	maxGitBranches       = 256
)

type SelectionPolicy uint8

// ErrNotConfigured reports that Cursor is disabled or has no resolved API key.
var ErrNotConfigured = errors.New("connect Cursor in Providers or set CURSOR_API_KEY")

const (
	PreserveUpstreamDefault SelectionPolicy = iota
	RequireExactVariant
)

type Runner interface {
	Catalog(ctx context.Context, force bool) (*cursor.ModelCatalog, error)
	InvalidateCatalog()
	ValidateModel(ctx context.Context, selection *cursor.ModelSelection, policy SelectionPolicy) (*cursor.ModelSelection, error)
	CreateAgent(ctx context.Context, req cursor.CreateAgentRequest) (*cursor.CreateAgentResponse, error)
	CreateRun(ctx context.Context, agentID string, req cursor.CreateRunRequest) (*cursor.Run, error)
	GetAgent(ctx context.Context, agentID string) (*cursor.Agent, error)
	GetRun(ctx context.Context, agentID, runID string) (*cursor.Run, error)
	CancelRun(ctx context.Context, agentID, runID string) error
	StreamRun(ctx context.Context, agentID, runID, lastEventID string, onReset func() error, emit func(cursor.StreamEvent) error) (*cursor.Run, error)
	Progress(cursor.StreamEvent) Progress
}

type ClientResolver func() (cursor.Options, error)

type Options struct {
	ResolveClient ClientResolver
	Now           func() time.Time
	CatalogTTL    time.Duration
}

type Progress struct {
	Message string
	Chunk   string
}

type service struct {
	resolveClient ClientResolver
	now           func() time.Time
	catalogTTL    time.Duration

	catalogMu         sync.Mutex
	catalogGeneration uint64
	catalogs          map[catalogCacheKey]catalogCacheEntry
	catalogFetches    map[catalogFetchKey]*catalogFetch
}

func New(options Options) Runner {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	ttl := options.CatalogTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &service{
		resolveClient:  options.ResolveClient,
		now:            now,
		catalogTTL:     ttl,
		catalogs:       make(map[catalogCacheKey]catalogCacheEntry),
		catalogFetches: make(map[catalogFetchKey]*catalogFetch),
	}
}

func (s *service) client() (*cursor.Client, string, error) {
	client, _, secret, err := s.clientWithOptions()
	return client, secret, err
}

func (s *service) clientWithOptions() (*cursor.Client, cursor.Options, string, error) {
	if s.resolveClient == nil {
		return nil, cursor.Options{}, "", errors.New("cursor: client resolver is required")
	}
	options, err := s.resolveClient()
	secret := strings.TrimSpace(options.APIKey)
	if err != nil {
		return nil, options, secret, sanitizeError(err, secret)
	}
	client, err := cursor.New(options)
	if err != nil {
		return nil, options, secret, sanitizeError(err, secret)
	}
	return client, options, secret, nil
}

func (s *service) CreateAgent(
	ctx context.Context,
	req cursor.CreateAgentRequest,
) (*cursor.CreateAgentResponse, error) {
	client, secret, err := s.client()
	if err != nil {
		return nil, err
	}
	created, err := client.CreateAgent(ctx, req)
	if err != nil {
		return nil, sanitizeError(err, secret)
	}
	return sanitizeCreateAgentResponse(created, secret), nil
}

func (s *service) CreateRun(
	ctx context.Context,
	agentID string,
	req cursor.CreateRunRequest,
) (*cursor.Run, error) {
	client, secret, err := s.client()
	if err != nil {
		return nil, err
	}
	run, err := client.CreateRun(ctx, agentID, req)
	if err != nil {
		return nil, sanitizeError(err, secret)
	}
	return sanitizeRun(run, secret), nil
}

func (s *service) GetAgent(ctx context.Context, agentID string) (*cursor.Agent, error) {
	client, secret, err := s.client()
	if err != nil {
		return nil, err
	}
	agent, err := client.GetAgent(ctx, agentID)
	if err != nil {
		return nil, sanitizeError(err, secret)
	}
	return sanitizeAgent(agent, secret), nil
}

func (s *service) GetRun(ctx context.Context, agentID, runID string) (*cursor.Run, error) {
	client, secret, err := s.client()
	if err != nil {
		return nil, err
	}
	run, err := client.GetRun(ctx, agentID, runID)
	if err != nil {
		return nil, sanitizeError(err, secret)
	}
	return sanitizeRun(run, secret), nil
}

func (s *service) CancelRun(ctx context.Context, agentID, runID string) error {
	client, secret, err := s.client()
	if err != nil {
		return err
	}
	return sanitizeError(client.CancelRun(ctx, agentID, runID), secret)
}

func (s *service) StreamRun(
	ctx context.Context,
	agentID, runID, lastEventID string,
	onReset func() error,
	emit func(cursor.StreamEvent) error,
) (*cursor.Run, error) {
	client, secret, err := s.client()
	if err != nil {
		return nil, err
	}
	if emit == nil {
		emit = func(cursor.StreamEvent) error { return nil }
	}
	run, err := client.StreamRunWithOptions(
		ctx,
		agentID,
		runID,
		cursor.StreamOptions{LastEventID: lastEventID, OnReset: onReset},
		func(event cursor.StreamEvent) error {
			return emit(sanitizeStreamEvent(event, secret))
		},
	)
	if err != nil {
		return nil, sanitizeError(err, secret)
	}
	return sanitizeRun(run, secret), nil
}

func (s *service) Progress(event cursor.StreamEvent) Progress {
	secret := ""
	if s.resolveClient != nil {
		options, _ := s.resolveClient()
		secret = strings.TrimSpace(options.APIKey)
	}
	message := "Cursor " + sanitizeString(event.Type, secret, maxProgressRunes)
	if event.ToolName != "" {
		message = "Cursor tool " +
			sanitizeString(event.ToolName, secret, maxProgressRunes) + " " +
			sanitizeString(event.Status, secret, maxProgressRunes)
	}
	return Progress{
		Message: boundProgress(message),
		Chunk:   boundProgress(redact(event.Text, secret)),
	}
}

func boundProgress(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if utf8.RuneCountInString(value) <= maxProgressRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxProgressRunes]) + "…"
}

func sanitizeCreateAgentResponse(
	response *cursor.CreateAgentResponse,
	secret string,
) *cursor.CreateAgentResponse {
	if response == nil {
		return nil
	}
	safe := *response
	safe.Agent = *sanitizeAgent(&response.Agent, secret)
	safe.Run = *sanitizeRun(&response.Run, secret)
	return &safe
}

func sanitizeAgent(agent *cursor.Agent, secret string) *cursor.Agent {
	if agent == nil {
		return nil
	}
	safe := *agent
	safe.ID = sanitizeString(agent.ID, secret, maxIdentifierRunes)
	safe.Name = sanitizeString(agent.Name, secret, maxMetadataRunes)
	safe.Status = sanitizeString(agent.Status, secret, maxMetadataRunes)
	safe.URL = sanitizeString(agent.URL, secret, maxMetadataRunes)
	safe.LatestRunID = sanitizeString(agent.LatestRunID, secret, maxIdentifierRunes)
	safe.Git = sanitizeGit(agent.Git, secret)
	repositoryCount := min(len(agent.Repos), maxAgentRepositories)
	safe.Repos = make([]cursor.Repository, repositoryCount)
	for i, repo := range agent.Repos[:repositoryCount] {
		safe.Repos[i] = cursor.Repository{
			URL:         sanitizeString(repo.URL, secret, maxMetadataRunes),
			StartingRef: sanitizeString(repo.StartingRef, secret, maxMetadataRunes),
			PRURL:       sanitizeString(repo.PRURL, secret, maxMetadataRunes),
		}
	}
	return &safe
}

func sanitizeRun(run *cursor.Run, secret string) *cursor.Run {
	if run == nil {
		return nil
	}
	safe := *run
	safe.ID = sanitizeString(run.ID, secret, maxIdentifierRunes)
	safe.AgentID = sanitizeString(run.AgentID, secret, maxIdentifierRunes)
	safe.Status = sanitizeString(run.Status, secret, maxMetadataRunes)
	safe.CreatedAt = sanitizeString(run.CreatedAt, secret, maxMetadataRunes)
	safe.UpdatedAt = sanitizeString(run.UpdatedAt, secret, maxMetadataRunes)
	safe.Result = sanitizeString(run.Result, secret, maxContentRunes)
	safe.Git = sanitizeGit(run.Git, secret)
	return &safe
}

func sanitizeGit(git *cursor.GitState, secret string) *cursor.GitState {
	if git == nil {
		return nil
	}
	branchCount := min(len(git.Branches), maxGitBranches)
	safe := &cursor.GitState{Branches: make([]cursor.GitBranch, branchCount)}
	for i, branch := range git.Branches[:branchCount] {
		safe.Branches[i] = cursor.GitBranch{
			RepoURL: sanitizeString(branch.RepoURL, secret, maxMetadataRunes),
			Branch:  sanitizeString(branch.Branch, secret, maxMetadataRunes),
			PRURL:   sanitizeString(branch.PRURL, secret, maxMetadataRunes),
		}
	}
	return safe
}

func sanitizeStreamEvent(event cursor.StreamEvent, secret string) cursor.StreamEvent {
	event.ID = sanitizeString(event.ID, secret, maxIdentifierRunes)
	event.Type = sanitizeString(event.Type, secret, maxMetadataRunes)
	event.Status = sanitizeString(event.Status, secret, maxMetadataRunes)
	event.Text = sanitizeString(event.Text, secret, maxContentRunes)
	event.RunID = sanitizeString(event.RunID, secret, maxIdentifierRunes)
	event.Raw, _ = sanitizeRaw(event.Raw, secret)
	event.ToolName = sanitizeString(event.ToolName, secret, maxMetadataRunes)
	event.CallID = sanitizeString(event.CallID, secret, maxIdentifierRunes)
	var argsTruncated, resultTruncated bool
	event.ToolArgs, argsTruncated = sanitizeRaw(event.ToolArgs, secret)
	event.ToolResult, resultTruncated = sanitizeRaw(event.ToolResult, secret)
	event.ArgsTruncated = event.ArgsTruncated || argsTruncated
	event.ResultTruncated = event.ResultTruncated || resultTruncated
	return event
}

func sanitizeRaw(value json.RawMessage, secret string) (json.RawMessage, bool) {
	if value == nil {
		return nil, false
	}
	safe := redact(string(value), secret)
	if secret != "" {
		quoted, err := json.Marshal(secret)
		if err == nil && len(quoted) >= 2 {
			escaped := string(quoted[1 : len(quoted)-1])
			safe = strings.ReplaceAll(safe, escaped, "[REDACTED]")
		}
	}
	if utf8.RuneCountInString(safe) > maxStreamRawRunes {
		return json.RawMessage(`{"truncated":true}`), true
	}
	return json.RawMessage(safe), false
}

func sanitizeError(err error, secret string) error {
	if err == nil {
		return nil
	}
	var apiErr *cursor.APIError
	if errors.As(err, &apiErr) {
		safe := *apiErr
		safe.Code = sanitizeString(apiErr.Code, secret, 120)
		safe.Message = sanitizeString(apiErr.Message, secret, 240)
		return &safe
	}
	safe := sanitizeString(err.Error(), secret, maxGenericErrorRunes)
	if safe == err.Error() {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &sanitizedWrappedError{message: safe, cause: context.Canceled}
	case errors.Is(err, context.DeadlineExceeded):
		return &sanitizedWrappedError{message: safe, cause: context.DeadlineExceeded}
	}
	return errors.New(safe)
}

type sanitizedWrappedError struct {
	message string
	cause   error
}

func (e *sanitizedWrappedError) Error() string { return e.message }
func (e *sanitizedWrappedError) Unwrap() error { return e.cause }

func sanitizeString(value, secret string, maxRunes int) string {
	return truncate(redact(value, secret), maxRunes)
}

func redact(value, secret string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}

func truncate(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}
