package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

// smallPNG returns the bytes of a 4x4 opaque PNG.
func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: 200, G: 120, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// referenceFile writes a small PNG to disk for use as a reference image.
func referenceFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(path, smallPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// imageServer records the one request it receives and answers with reply.
type recorded struct {
	path string
	body map[string]any
}

func imageServer(t *testing.T, contentType, reply string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &rec.body)
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func okImageReply(t *testing.T) string {
	b, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(smallPNG(t))}}})
	return string(b)
}

// Without references the call is a plain generation in either mode, and it
// carries no image fields a gateway could misread.
func TestGenerateImage_NoReferencesUsesGenerations(t *testing.T) {
	for _, mode := range []string{"", ReferenceModeEdits, ReferenceModeGenerations} {
		srv, rec := imageServer(t, "application/json", okImageReply(t))
		ep := Endpoint{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m", ReferenceMode: mode}
		out := filepath.Join(t.TempDir(), "out.png")
		if err := GenerateImage(context.Background(), ep, "a planet", "1024x1024", nil, out); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if rec.path != "/v1/images/generations" {
			t.Fatalf("mode %q: path = %s, want /v1/images/generations", mode, rec.path)
		}
		if _, ok := rec.body["image"]; ok {
			t.Fatalf("mode %q: plain generation sent an image field", mode)
		}
		if _, ok := rec.body["images"]; ok {
			t.Fatalf("mode %q: plain generation sent an images field", mode)
		}
	}
}

// The default mode keeps the existing OpenAI edits request unchanged.
func TestGenerateImage_EditsModeSendsImagesToEdits(t *testing.T) {
	srv, rec := imageServer(t, "application/json", okImageReply(t))
	ep := Endpoint{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"}
	if err := GenerateImage(context.Background(), ep, "a planet", "1024x1024", []string{referenceFile(t)}, filepath.Join(t.TempDir(), "out.png")); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/v1/images/edits" {
		t.Fatalf("path = %s, want /v1/images/edits", rec.path)
	}
	images, _ := rec.body["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images = %#v, want one entry", rec.body["images"])
	}
	first, _ := images[0].(map[string]any)
	if u, _ := first["image_url"].(string); !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("images[0].image_url = %q, want a PNG data URL", u)
	}
	if rec.body["output_format"] != "png" {
		t.Fatalf("output_format = %v, want png", rec.body["output_format"])
	}
}

// Generations mode sends references as an image array on /images/generations,
// the shape gateways without an edits route accept.
func TestGenerateImage_GenerationsModeSendsImageArray(t *testing.T) {
	srv, rec := imageServer(t, "application/json", okImageReply(t))
	ep := Endpoint{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m", ReferenceMode: ReferenceModeGenerations}
	out := filepath.Join(t.TempDir(), "out.png")
	if err := GenerateImage(context.Background(), ep, "a planet", "1024x1024", []string{referenceFile(t), referenceFile(t)}, out); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/v1/images/generations" {
		t.Fatalf("path = %s, want /v1/images/generations", rec.path)
	}
	refs, _ := rec.body["image"].([]any)
	if len(refs) != 2 {
		t.Fatalf("image = %#v, want two data URLs", rec.body["image"])
	}
	for _, r := range refs {
		if u, _ := r.(string); !strings.HasPrefix(u, "data:image/png;base64,") {
			t.Fatalf("image entry %q is not a PNG data URL", u)
		}
	}
	if _, ok := rec.body["images"]; ok {
		t.Fatal("generations mode also sent the edits-style images field")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("no output written: %v", err)
	}
}

// A gateway without an edits route answers with its website's HTML page and a
// 200. The error must say so and point at reference_mode, instead of the old
// "returned nothing usable" that hid what came back.
func TestGenerateImage_HTMLReplyExplainsMissingRoute(t *testing.T) {
	srv, _ := imageServer(t, "text/html; charset=utf-8", "<!doctype html><html><body><div id=root></div></body></html>")
	ep := Endpoint{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"}
	err := GenerateImage(context.Background(), ep, "a planet", "1024x1024", []string{referenceFile(t)}, filepath.Join(t.TempDir(), "out.png"))
	if err == nil {
		t.Fatal("expected an error for an HTML reply")
	}
	for _, want := range []string{"HTML page", "/images/edits", "reference_mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// A JSON reply without image data shows a trimmed, redacted body so the cause
// is visible without leaking the key.
func TestGenerateImage_EmptyDataShowsRedactedBody(t *testing.T) {
	srv, _ := imageServer(t, "application/json", `{"data":[],"note":"blocked for key sk-secret-xyz"}`)
	ep := Endpoint{BaseURL: srv.URL + "/v1", APIKey: "sk-secret-xyz", Model: "m"}
	err := GenerateImage(context.Background(), ep, "a planet", "1024x1024", nil, filepath.Join(t.TempDir(), "out.png"))
	if err == nil {
		t.Fatal("expected an error for an empty data array")
	}
	if !strings.Contains(err.Error(), "no image data") || !strings.Contains(err.Error(), "blocked for key") {
		t.Fatalf("error %q does not show the response", err)
	}
	if strings.Contains(err.Error(), "sk-secret-xyz") {
		t.Fatalf("error leaked the API key: %q", err)
	}
}

// ImageEndpoint carries a valid mode through and refuses an unknown one
// before any request could be built.
func TestImageEndpoint_ReferenceMode(t *testing.T) {
	cfg := &config.Config{}
	cfg.ImageGen = config.ImageGen{Enabled: true, BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m"}
	for _, mode := range []string{"", ReferenceModeEdits, ReferenceModeGenerations} {
		cfg.ImageGen.ReferenceMode = mode
		ep, err := ImageEndpoint(cfg)
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if ep.ReferenceMode != mode {
			t.Fatalf("mode %q: endpoint carries %q", mode, ep.ReferenceMode)
		}
	}
	cfg.ImageGen.ReferenceMode = "multipart"
	if _, err := ImageEndpoint(cfg); err == nil || !strings.Contains(err.Error(), "reference_mode") {
		t.Fatalf("unknown mode error = %v, want a reference_mode error", err)
	}
}
