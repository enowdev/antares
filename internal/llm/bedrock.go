package llm

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// bedrockClient runs Anthropic Claude models on AWS Bedrock. It reuses the
// Anthropic message format — Bedrock's payload is the same, minus the model
// (which moves into the URL) and plus an anthropic_version marker — but signs
// each request with AWS SigV4 instead of an api-key header. Credentials come
// from the standard AWS environment variables; the region from config or
// AWS_REGION.
//
// Streaming on Bedrock uses AWS's binary event framing; rather than decode that,
// Stream runs a normal request and emits the whole answer once. The result is
// identical, only less incremental.
type bedrockClient struct {
	opts     Options
	region   string
	inner    *anthropicClient // body-building only
	endpoint string
}

const bedrockAnthropicVersion = "bedrock-2023-05-31"

func newBedrock(o Options) (Client, error) {
	region := strings.TrimSpace(o.Region)
	if region == "" {
		region = firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		return nil, errors.New("bedrock needs a region: set the provider's region or AWS_REGION")
	}
	host := fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", region)
	if o.BaseURL != "" {
		host = strings.TrimPrefix(strings.TrimPrefix(o.BaseURL, "https://"), "http://")
	}
	return &bedrockClient{
		opts:     o,
		region:   region,
		inner:    &anthropicClient{opts: o},
		endpoint: "https://" + host,
	}, nil
}

func (c *bedrockClient) Kind() string { return "bedrock" }

// bedrockBody is the Anthropic body with the model removed and the Bedrock
// version marker added.
func (c *bedrockClient) bedrockBody(req Request) map[string]any {
	body := c.inner.buildBody(req, false)
	delete(body, "model")
	body["anthropic_version"] = bedrockAnthropicVersion
	return body
}

func (c *bedrockClient) Chat(ctx context.Context, req Request) (*Response, error) {
	if req.Model == "" {
		return nil, errors.New("bedrock needs a model id, e.g. anthropic.claude-3-5-sonnet-20241022-v2:0")
	}
	// Bedrock reuses anthropicClient.buildBody directly rather than going
	// through anthropicClient.Chat, so it must run the same pre-request
	// reasoning validation itself. Otherwise an invalid explicit effort is
	// silently dropped by buildBody and a signed, billable request still goes
	// upstream.
	if err := c.inner.validateReasoning(req); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(c.bedrockBody(req))
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/model/%s/invoke", c.endpoint, req.Model)

	raw, err := c.signedPost(ctx, url, payload)
	if err != nil {
		return nil, err
	}
	var resp antResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode bedrock response: %w", err)
	}
	return c.inner.fromResponse(&resp)
}

// Stream emits the whole answer once (see the type comment).
func (c *bedrockClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Reasoning != "" {
		_ = emit(Event{Type: EventReasoning, Delta: resp.Reasoning})
	}
	if resp.Content != "" {
		_ = emit(Event{Type: EventText, Delta: resp.Content})
	}
	return resp, nil
}

func (c *bedrockClient) Models(context.Context) ([]ModelInfo, error) { return nil, nil }

func (c *bedrockClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, ErrUnsupported
}

// signedPost signs the request with SigV4 and posts it, returning the body.
func (c *bedrockClient) signedPost(ctx context.Context, rawURL string, payload []byte) ([]byte, error) {
	access := firstNonEmpty(c.opts.APIKey, os.Getenv("AWS_ACCESS_KEY_ID"))
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	session := os.Getenv("AWS_SESSION_TOKEN")
	if access == "" || secret == "" {
		return nil, errors.New("bedrock needs AWS credentials: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", rawURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	signV4(req, payload, access, secret, session, c.region, "bedrock", time.Now().UTC())

	client := c.opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	httpResp, err := client.Do(req)
	if err != nil {
		return nil, describeTransport(err, rawURL)
	}
	defer httpResp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, &apiError{Status: httpResp.StatusCode, Body: string(data), URL: rawURL,
			RetryAfter: parseRetryAfter(httpResp.Header.Get("Retry-After"))}
	}
	return data, nil
}

// ---- AWS Signature Version 4 -----------------------------------------------

func signV4(req *http.Request, payload []byte, access, secret, session, region, service string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if session != "" {
		req.Header.Set("X-Amz-Security-Token", session)
	}
	host := req.URL.Host
	req.Header.Set("Host", host)

	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Canonical headers: host, x-amz-content-sha256, x-amz-date (+security token),
	// sorted by lowercase name.
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if session != "" {
		headers["x-amz-security-token"] = session
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(headers[n]) + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		"POST",
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveKey(secret, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		access, scope, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
}

func deriveKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
