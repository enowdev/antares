package llm

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// AWS publishes a signing-key derivation example: secret + 20120215 + us-east-1
// + iam yields a fixed key. Matching it proves the SigV4 crypto core is correct.
func TestDeriveKeyMatchesAWSVector(t *testing.T) {
	key := deriveKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20120215", "us-east-1", "iam")
	got := hex.EncodeToString(key)
	want := "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d"
	if got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestSignV4SetsAuthHeaders(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude/invoke", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	signV4(req, []byte("{}"), "AKIDEXAMPLE", "secret", "sess-token", "us-east-1", "bedrock", time.Unix(1440938160, 0).UTC())

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
		t.Fatalf("bad auth header: %s", auth)
	}
	if !strings.Contains(auth, "us-east-1/bedrock/aws4_request") {
		t.Fatalf("scope missing: %s", auth)
	}
	if req.Header.Get("X-Amz-Security-Token") != "sess-token" {
		t.Fatal("session token not set")
	}
	if req.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Fatal("payload hash not set")
	}
	// The session token must be a signed header when present.
	if !strings.Contains(auth, "x-amz-security-token") {
		t.Fatalf("security token not in signed headers: %s", auth)
	}
}

func TestNewBedrockNeedsRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	if _, err := New(Options{Kind: "bedrock"}); err == nil {
		t.Fatal("bedrock without a region should error")
	}
	c, err := New(Options{Kind: "bedrock", Region: "us-west-2"})
	if err != nil || c.Kind() != "bedrock" {
		t.Fatalf("expected a bedrock client, got %v %v", c, err)
	}
}

// TestBedrockReasoningValidationBlocksRequestBeforeSigning guards a bypass:
// bedrockClient.Chat builds its body via anthropicClient.buildBody directly
// rather than anthropicClient.Chat, so it must run the same pre-request
// reasoning validation itself. Without it, an invalid explicit reasoning
// value was silently dropped by buildBody and a signed, billable request
// still went upstream.
func TestBedrockReasoningValidationBlocksRequestBeforeSigning(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cap, err := NewReasoningCapability([]ReasoningValue{{Value: "low", Label: "Low"}}, "low", false, ReasoningCapabilityStatic)
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{BaseURL: srv.URL, HTTPClient: srv.Client()}
	c := &bedrockClient{opts: opts, region: "us-east-1", inner: &anthropicClient{opts: opts}, endpoint: srv.URL}

	_, err = c.Chat(context.Background(), Request{
		Model:               "anthropic.claude-3-5-sonnet-20241022-v2:0",
		ReasoningEffort:     "max",
		ReasoningCapability: cap,
	})
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("requests sent = %d, want 0 (must fail before signing/sending)", got)
	}
}
