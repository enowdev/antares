package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
)

func TestDecodeCursorImagesAcceptsExactlyFiveSupportedImages(t *testing.T) {
	dataURLs := []string{
		cursorImageDataURL("image/png", cursorImageSignature("image/png")),
		cursorImageDataURL("image/jpeg", cursorImageSignature("image/jpeg")),
		cursorImageDataURL("image/gif", cursorImageSignature("image/gif")),
		cursorImageDataURL("image/webp", cursorImageSignature("image/webp")),
		cursorImageDataURL("image/png", cursorImageSignature("image/png")),
	}

	images, err := decodeCursorImages(dataURLs)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 5 {
		t.Fatalf("images=%d, want 5", len(images))
	}
	for i, image := range images {
		mimeType, payload := splitCursorImageDataURL(t, dataURLs[i])
		if image.MimeType != mimeType || image.Data != payload || image.URL != "" {
			t.Fatalf("image %d = %+v, want mime=%q original base64 payload", i, image, mimeType)
		}
	}
}

func TestDecodeCursorImagesRejectsBeforeDownstreamCallback(t *testing.T) {
	validPNG := cursorImageDataURL("image/png", cursorImageSignature("image/png"))
	oversized := make([]byte, (15<<20)+1)
	copy(oversized, cursorImageSignature("image/png"))

	tests := map[string][]string{
		"six images": {
			validPNG, validPNG, validPNG, validPNG, validPNG, validPNG,
		},
		"unsupported MIME": {
			cursorImageDataURL("image/bmp", cursorImageSignature("image/png")),
		},
		"MIME signature mismatch": {
			cursorImageDataURL("image/jpeg", cursorImageSignature("image/png")),
		},
		"decoded payload above 15 MiB": {
			cursorImageDataURL("image/png", oversized),
		},
		"remote URL": {
			"https://example.invalid/image.png",
		},
		"bare base64": {
			base64.StdEncoding.EncodeToString(cursorImageSignature("image/png")),
		},
		"malformed base64": {
			"data:image/png;base64,not-base64!",
		},
	}
	for name, dataURLs := range tests {
		t.Run(name, func(t *testing.T) {
			called := false
			err := decodeCursorImagesThen(dataURLs, func([]cursor.PromptImage) {
				called = true
			})
			if err == nil {
				t.Fatal("invalid images were accepted")
			}
			if called {
				t.Fatal("downstream approval/upstream callback ran before image validation")
			}
		})
	}
}

func TestDecodeCursorImagesAllowsExactDecodedLimit(t *testing.T) {
	exact := make([]byte, 15<<20)
	copy(exact, cursorImageSignature("image/png"))
	images, err := decodeCursorImages([]string{cursorImageDataURL("image/png", exact)})
	if err != nil {
		t.Fatalf("exact 15 MiB image rejected: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("images=%d, want 1", len(images))
	}
}

func TestCursorImagesBodyUsesStrictJSONDecoding(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/chat/cursor",
			strings.NewReader(`{"message":"hello","images":[]}`))
		rec := httptest.NewRecorder()
		var dst struct {
			Message string   `json:"message"`
			Images  []string `json:"images"`
		}
		if err := decodeCursorChatBody(rec, req, &dst); err != nil {
			t.Fatal(err)
		}
		if dst.Message != "hello" || dst.Images == nil {
			t.Fatalf("decoded body = %+v", dst)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/chat/cursor",
			strings.NewReader(`{"message":"hello","unexpected":true}`))
		rec := httptest.NewRecorder()
		var dst struct {
			Message string `json:"message"`
		}
		err := decodeCursorChatBody(rec, req, &dst)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("err=%v, want unknown-field rejection", err)
		}
	})

	t.Run("trailing JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/chat/cursor",
			strings.NewReader(`{"message":"hello"} {"message":"again"}`))
		rec := httptest.NewRecorder()
		var dst struct {
			Message string `json:"message"`
		}
		if err := decodeCursorChatBody(rec, req, &dst); err == nil {
			t.Fatal("multiple JSON values were accepted")
		}
	})
}

func TestCursorImagesBodyCapAllowsFiveMaximumImagesAndMapsLargerBodiesTo413(t *testing.T) {
	const (
		maxImageBytes = 15 << 20
		wantBodyLimit = 105 << 20
	)
	if cursorChatBodyLimit != wantBodyLimit {
		t.Fatalf("cursor body limit=%d, want %d", cursorChatBodyLimit, wantBodyLimit)
	}
	encodedImageBytes := base64.StdEncoding.EncodedLen(maxImageBytes)
	fiveImageBodyBytes := len(`{"images":[]}`) +
		5*(len(`data:image/png;base64,`)+encodedImageBytes+2) + 4
	if fiveImageBodyBytes > cursorChatBodyLimit {
		t.Fatalf("five legal maximum images need %d bytes, body cap is %d",
			fiveImageBodyBytes, cursorChatBodyLimit)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dst struct{}
		if err := decodeCursorChatBody(w, r, &dst); err != nil {
			status := http.StatusBadRequest
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, status, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/cursor", strings.NewReader(`{}`))
	req.ContentLength = cursorChatBodyLimit + 1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s, want 413", rec.Code, rec.Body.String())
	}
}

func TestCursorImagesBodyCapUsesMaxBytesReaderForUnknownLength(t *testing.T) {
	body := `{"message":"` + strings.Repeat("a", 80) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/cursor", strings.NewReader(body))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	var dst struct {
		Message string `json:"message"`
	}
	err := decodeCursorChatBodyWithLimit(rec, req, &dst, 64)
	var maxBytesError *http.MaxBytesError
	if !errors.As(err, &maxBytesError) {
		t.Fatalf("err=%T %v, want *http.MaxBytesError", err, err)
	}
}

func TestCursorRepositoryRouteRequiresDashboardAuthentication(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	dir := t.TempDir()

	t.Run("unprotected dashboard", func(t *testing.T) {
		s := New(Options{Config: config.Default()})
		req := httptest.NewRequest(http.MethodGet,
			"/api/project/cursor-repository?dir="+url.QueryEscape(dir), nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusPreconditionRequired {
			t.Fatalf("status=%d body=%s, want 428", rec.Code, rec.Body.String())
		}
	})

	t.Run("locked dashboard without login", func(t *testing.T) {
		cfg := config.Default()
		cfg.Server.DashboardPasswordHash = "configured"
		s := New(Options{Config: cfg})
		req := httptest.NewRequest(http.MethodGet,
			"/api/project/cursor-repository?dir="+url.QueryEscape(dir), nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s, want 401", rec.Code, rec.Body.String())
		}
	})
}

func TestCursorRepositoryRouteUsesProjectPathGuardAndReturnsRepositoryInfo(t *testing.T) {
	requireServerGit(t)
	t.Setenv("ANTARES_HOME", t.TempDir())
	repo := initServerTestRepository(t)
	serverRunGit(t, repo, "remote", "add", "origin", "git@github.com:owner/repo.git")

	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	s := New(Options{Config: cfg})

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/cursor-repository?dir="+url.QueryEscape(repo), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var info cursorrun.RepositoryInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Repository || info.URL != "https://github.com/owner/repo" ||
		info.StartingRef != "main" {
		t.Fatalf("repository info = %+v", info)
	}

	req = httptest.NewRequest(http.MethodGet,
		"/api/project/cursor-repository?dir="+url.QueryEscape("relative/path"), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative path status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestCursorRepositoryRouteReturnsDegradedPreflightForUnsafeOrigin(t *testing.T) {
	requireServerGit(t)
	t.Setenv("ANTARES_HOME", t.TempDir())
	repo := initServerTestRepository(t)
	serverRunGit(t, repo, "remote", "add", "origin",
		"https://user:secret@github.com/owner/repo.git")

	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	s := New(Options{Config: cfg})
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/cursor-repository?dir="+url.QueryEscape(repo), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var info cursorrun.RepositoryInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Repository || info.StartingRef != "main" || info.URL != "" ||
		info.Warning == "" {
		t.Fatalf("degraded repository info = %+v", info)
	}
	if len(info.Warning) > 512 || strings.Contains(rec.Body.String(), "secret") ||
		strings.Contains(rec.Body.String(), "user") {
		t.Fatalf("unsafe origin leaked in response: %s", rec.Body.String())
	}
}

func decodeCursorImagesThen(dataURLs []string, callback func([]cursor.PromptImage)) error {
	images, err := decodeCursorImages(dataURLs)
	if err != nil {
		return err
	}
	callback(images)
	return nil
}

func cursorImageDataURL(mimeType string, decoded []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(decoded)
}

func cursorImageSignature(mimeType string) []byte {
	switch mimeType {
	case "image/png":
		return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	case "image/jpeg":
		return []byte{0xff, 0xd8, 0xff, 0xdb}
	case "image/gif":
		return []byte("GIF89a")
	case "image/webp":
		return []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P'}
	default:
		return nil
	}
}

func splitCursorImageDataURL(t *testing.T, dataURL string) (string, string) {
	t.Helper()
	meta, payload, ok := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
	if !ok {
		t.Fatalf("invalid test data URL: %q", dataURL)
	}
	mimeType, encoding, ok := strings.Cut(meta, ";")
	if !ok || encoding != "base64" {
		t.Fatalf("invalid test data URL metadata: %q", meta)
	}
	return mimeType, payload
}

func initServerTestRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	cmd := exec.Command("git", "init", "--initial-branch=main", repo)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	serverRunGit(t, repo, "config", "user.name", "Task 8 Test")
	serverRunGit(t, repo, "config", "user.email", "task8@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverRunGit(t, repo, "add", "tracked.txt")
	serverRunGit(t, repo, "commit", "-m", "initial")
	return repo
}

func serverRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func requireServerGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
}
