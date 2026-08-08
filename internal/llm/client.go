package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enowdev/antares/internal/version"
)

// ErrUnsupported is returned by adapters that lack a capability.
var ErrUnsupported = errors.New("operation not supported by this provider")

// Options configures an adapter instance.
type Options struct {
	Kind       string
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Timeout    time.Duration
	ProviderID string
	HTTPClient *http.Client
	// Retries is how many times a transient failure (429, 5xx, connection
	// drop) is retried with backoff before giving up. Zero disables retrying.
	Retries int
	// RetryBaseDelay is the first backoff step; it doubles each attempt. Zero
	// uses a sensible default.
	RetryBaseDelay time.Duration
	// APIVersion is the Azure OpenAI api-version query parameter. Ignored by
	// other kinds.
	APIVersion string
	// Region is the AWS region for Bedrock, e.g. "us-east-1". Empty reads
	// AWS_REGION / AWS_DEFAULT_REGION.
	Region string
	// SessionID is an optional stable conversation id (e.g. Antares session).
	// Gemini adapters use it for gateway sticky routing / implicit cache
	// affinity on reverse proxies that fingerprint Gemini CLI sessions.
	SessionID string
}

// New builds the adapter matching kind. Unknown kinds fall back to the
// OpenAI-compatible adapter, which covers most self-hosted endpoints.
func New(o Options) (Client, error) {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: o.Timeout}
	}
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")

	base, err := newBase(o)
	if err != nil {
		return nil, err
	}
	if o.Retries > 0 {
		return newRetrying(base, o.Retries, o.RetryBaseDelay), nil
	}
	return base, nil
}

// newBase builds the raw adapter for a kind, without the retry wrapper.
func newBase(o Options) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(o.Kind)) {
	case "anthropic", "claude":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.anthropic.com/v1"
		}
		return &anthropicClient{opts: o}, nil
	case "gemini", "google":
		if o.BaseURL == "" {
			o.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		} else {
			// Match Gemini CLI: GOOGLE_GEMINI_BASE_URL is often the gateway root
			// (e.g. http://127.0.0.1:8080/antigravity) and the client appends /v1beta.
			o.BaseURL = normalizeGeminiBaseURL(o.BaseURL)
		}
		o.Headers = withGeminiGatewayStickyHeaders(o.Headers, o.BaseURL, o.SessionID)
		return &geminiClient{opts: o}, nil
	case "bedrock", "aws", "aws-bedrock":
		return newBedrock(o)
	case "vertex", "vertexai", "vertex-ai", "google-vertex":
		return newVertex(o)
	case "copilot", "github-copilot", "githubcopilot":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.githubcopilot.com"
		}
		return &openAIClient{opts: o, vendor: "copilot",
			copilot: &copilotTokenSource{ghToken: o.APIKey, client: o.HTTPClient}}, nil
	case "codex", "responses", "openai-responses":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.openai.com/v1"
		}
		return &codexClient{opts: o}, nil
	case "openai":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.openai.com/v1"
		}
		return &openAIClient{opts: o, vendor: "openai"}, nil
	case "azure", "azure-openai", "azureopenai":
		if o.BaseURL == "" {
			return nil, errors.New("base_url (the Azure resource endpoint, e.g. https://<resource>.openai.azure.com) is required for Azure OpenAI")
		}
		if o.APIVersion == "" {
			o.APIVersion = "2024-10-21"
		}
		return &openAIClient{opts: o, vendor: "azure"}, nil
	case "", "openai-compatible", "openai_compatible", "compat", "custom":
		if o.BaseURL == "" {
			return nil, errors.New("base_url is required for OpenAI-compatible providers")
		}
		return &openAIClient{opts: o, vendor: "compat"}, nil
	default:
		if o.BaseURL == "" {
			return nil, fmt.Errorf("unknown provider kind %q and no base_url set", o.Kind)
		}
		return &openAIClient{opts: o, vendor: "compat"}, nil
	}
}

// apiError carries an upstream HTTP failure with its body for diagnostics.
type apiError struct {
	Status int
	Body   string
	URL    string
	// RetryAfter is the provider's requested wait from a Retry-After header,
	// or zero if none was sent.
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
	if msg := extractAPIMessage(e.Body); msg != "" {
		return msg
	}
	if txt := http.StatusText(e.Status); txt != "" {
		return fmt.Sprintf("the provider returned %d %s", e.Status, txt)
	}
	return fmt.Sprintf("the provider returned %d", e.Status)
}

// extractAPIMessage pulls the human sentence out of a provider's error body.
// Vendors disagree on the shape, and none of them are worth showing raw:
//
//	{"error": {"message": "..."}}          OpenAI, Gemini, most compatibles
//	{"type": "error", "error": {...}}      Anthropic
//	{"message": "..."} / {"error": "..."}  everyone else
func extractAPIMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" || body[0] != '{' {
		return ""
	}

	var envelope struct {
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}

	if len(envelope.Error) > 0 {
		// Either a bare string or a nested object.
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil && text != "" {
			return sentence(text)
		}
		var nested struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil {
			if m := firstNonEmpty(nested.Message, nested.Detail); m != "" {
				return sentence(m)
			}
		}
	}
	return sentence(firstNonEmpty(envelope.Message, envelope.Detail))
}

// sentence trims a provider message to something a UI can show inline.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 240 {
		s = strings.TrimSpace(s[:240]) + "…"
	}
	return s
}

// IsAuthError reports whether err came back as 401/403.
func IsAuthError(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden
	}
	return false
}

// IsUnsupported reports that a provider does not expose the optional model
// catalogue endpoint. A 404/405 is different from a timeout, malformed body,
// or server error: the credential may still be usable for chat.
func IsUnsupported(err error) bool {
	if errors.Is(err, ErrUnsupported) {
		return true
	}
	var ae *apiError
	return errors.As(err, &ae) &&
		(ae.Status == http.StatusNotFound || ae.Status == http.StatusMethodNotAllowed)
}

// IsRateLimit reports whether err came back as 429.
func IsRateLimit(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests
}

// doJSON issues a JSON request and decodes the response into out.
func (o Options) doJSON(ctx context.Context, method, url string, body any, headers map[string]string, out any) error {
	resp, err := o.do(ctx, method, url, body, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(data), 400))
	}
	return nil
}

func (o Options) do(ctx context.Context, method, url string, body any, headers map[string]string) (*http.Response, error) {
	return o.doWithClient(ctx, o.HTTPClient, method, url, body, headers)
}

// doStream applies Timeout as an inactivity limit. http.Client.Timeout is a
// total lifetime limit and would terminate a healthy stream that keeps sending.
func (o Options) doStream(ctx context.Context, method, url string, body any, headers map[string]string) (*http.Response, error) {
	client := *o.HTTPClient
	idleTimeout := o.Timeout
	if idleTimeout <= 0 {
		idleTimeout = client.Timeout
	}
	if idleTimeout <= 0 {
		return o.doWithClient(ctx, &client, method, url, body, headers)
	}
	client.Timeout = 0
	streamCtx, cancel := context.WithCancel(ctx)
	activity := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeout)
			case <-timer.C:
				cancel()
				return
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	resp, err := o.doWithClient(streamCtx, &client, method, url, body, headers)
	if err != nil {
		close(done)
		cancel()
		return nil, err
	}
	resp.Body = &activityReadCloser{ReadCloser: resp.Body, activity: activity, done: done, cancel: cancel}
	return resp, nil
}

func (o Options) doWithClient(ctx context.Context, client *http.Client, method, url string, body any, headers map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, describeTransport(err, url)
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		ra := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		return nil, &apiError{Status: resp.StatusCode, Body: string(data), URL: url, RetryAfter: ra}
	}
	return resp, nil
}

type activityReadCloser struct {
	io.ReadCloser
	activity chan<- struct{}
	done     chan struct{}
	cancel   context.CancelFunc
	once     sync.Once
}

func (r *activityReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		select {
		case r.activity <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (r *activityReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		close(r.done)
		r.cancel()
	})
	return err
}

// TransportError is a connection failure, as opposed to a response the
// provider actually sent. Callers use it to tell "your key is wrong" apart
// from "nothing is listening there".
type TransportError struct {
	Host string
	Kind string // refused|dns|timeout|tls|network
	err  error
}

func (e *TransportError) Error() string {
	switch e.Kind {
	case "refused":
		return fmt.Sprintf("nothing is listening at %s", e.Host)
	case "dns":
		return fmt.Sprintf("cannot resolve %s", e.Host)
	case "timeout":
		return fmt.Sprintf("%s did not respond in time", e.Host)
	case "tls":
		return fmt.Sprintf("the TLS handshake with %s failed", e.Host)
	default:
		return fmt.Sprintf("cannot reach %s", e.Host)
	}
}

func (e *TransportError) Unwrap() error { return e.err }

// IsUnreachable reports whether err is a connection failure rather than a
// response from the provider.
func IsUnreachable(err error) bool {
	var te *TransportError
	return errors.As(err, &te)
}

// describeTransport turns Go's layered network errors into one plain sentence.
// The raw form — `Get "http://127.0.0.1:1234/v1/models": dial tcp ...:
// connect: connection refused` — repeats the URL twice and buries the point.
func describeTransport(err error, rawURL string) error {
	host := rawURL
	if u, perr := url.Parse(rawURL); perr == nil && u.Host != "" {
		host = u.Scheme + "://" + u.Host
	}

	te := &TransportError{Host: host, Kind: "network", err: err}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		te.Kind = "refused"
	case errors.Is(err, context.DeadlineExceeded), os.IsTimeout(err):
		te.Kind = "timeout"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		te.Kind = "dns"
		te.Host = dnsErr.Name
	}
	var tlsErr *tls.CertificateVerificationError
	if errors.As(err, &tlsErr) {
		te.Kind = "tls"
	}
	return te
}

// sseLines walks an SSE body, calling fn with each `data:` payload.
// Returning io.EOF from fn stops iteration cleanly.
func sseLines(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	event := ""
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		payload := data.String()
		data.Reset()
		ev := event
		event = ""
		return fn(ev, payload)
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment/keepalive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// toolCallAccumulator reassembles streamed tool-call fragments in order.
type toolCallAccumulator struct {
	order []int
	calls map[int]*ToolCall
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: map[int]*ToolCall{}}
}

func (a *toolCallAccumulator) ensure(idx int) *ToolCall {
	if c, ok := a.calls[idx]; ok {
		return c
	}
	c := &ToolCall{}
	a.calls[idx] = c
	a.order = append(a.order, idx)
	return c
}

func (a *toolCallAccumulator) result() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		c := a.calls[idx]
		if c.Name == "" && c.Arguments == "" {
			continue
		}
		if strings.TrimSpace(c.Arguments) == "" {
			c.Arguments = "{}"
		}
		out = append(out, *c)
	}
	return out
}
