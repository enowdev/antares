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

type SelectionPolicy uint8

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
	message := "Cursor " + event.Type
	if event.ToolName != "" {
		message = "Cursor tool " + event.ToolName + " " + event.Status
	}
	return Progress{
		Message: boundProgress(message),
		Chunk:   boundProgress(event.Text),
	}
}

func boundProgress(value string) string {
	const maxRunes = 2000
	value = strings.ToValidUTF8(value, "\uFFFD")
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "…"
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
	safe.ID = redact(agent.ID, secret)
	safe.Name = redact(agent.Name, secret)
	safe.Status = redact(agent.Status, secret)
	safe.URL = redact(agent.URL, secret)
	safe.LatestRunID = redact(agent.LatestRunID, secret)
	safe.Git = sanitizeGit(agent.Git, secret)
	safe.Repos = make([]cursor.Repository, len(agent.Repos))
	for i, repo := range agent.Repos {
		safe.Repos[i] = cursor.Repository{
			URL:         redact(repo.URL, secret),
			StartingRef: redact(repo.StartingRef, secret),
			PRURL:       redact(repo.PRURL, secret),
		}
	}
	return &safe
}

func sanitizeRun(run *cursor.Run, secret string) *cursor.Run {
	if run == nil {
		return nil
	}
	safe := *run
	safe.ID = redact(run.ID, secret)
	safe.AgentID = redact(run.AgentID, secret)
	safe.Status = redact(run.Status, secret)
	safe.CreatedAt = redact(run.CreatedAt, secret)
	safe.UpdatedAt = redact(run.UpdatedAt, secret)
	safe.Result = redact(run.Result, secret)
	safe.Git = sanitizeGit(run.Git, secret)
	return &safe
}

func sanitizeGit(git *cursor.GitState, secret string) *cursor.GitState {
	if git == nil {
		return nil
	}
	safe := &cursor.GitState{Branches: make([]cursor.GitBranch, len(git.Branches))}
	for i, branch := range git.Branches {
		safe.Branches[i] = cursor.GitBranch{
			RepoURL: redact(branch.RepoURL, secret),
			Branch:  redact(branch.Branch, secret),
			PRURL:   redact(branch.PRURL, secret),
		}
	}
	return safe
}

func sanitizeStreamEvent(event cursor.StreamEvent, secret string) cursor.StreamEvent {
	event.ID = redact(event.ID, secret)
	event.Type = redact(event.Type, secret)
	event.Status = redact(event.Status, secret)
	event.Text = redact(event.Text, secret)
	event.RunID = redact(event.RunID, secret)
	event.Raw = redactRaw(event.Raw, secret)
	event.ToolName = redact(event.ToolName, secret)
	event.CallID = redact(event.CallID, secret)
	event.ToolArgs = redactRaw(event.ToolArgs, secret)
	event.ToolResult = redactRaw(event.ToolResult, secret)
	return event
}

func redactRaw(value json.RawMessage, secret string) json.RawMessage {
	if value == nil {
		return nil
	}
	safe := redact(string(value), secret)
	if secret != "" {
		quoted, err := json.Marshal(secret)
		if err == nil && len(quoted) >= 2 {
			escaped := string(quoted[1 : len(quoted)-1])
			safe = strings.ReplaceAll(safe, escaped, "[REDACTED]")
		}
	}
	return json.RawMessage(safe)
}

func sanitizeError(err error, secret string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiErr *cursor.APIError
	if errors.As(err, &apiErr) {
		safe := *apiErr
		safe.Code = truncate(redact(apiErr.Code, secret), 120)
		safe.Message = truncate(redact(apiErr.Message, secret), 240)
		return &safe
	}
	safe := redact(err.Error(), secret)
	if safe == err.Error() {
		return err
	}
	return errors.New(safe)
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
