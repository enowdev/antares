package server

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func decodePersona(t *testing.T, code int, body []byte) map[string]any {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPersonaRoundTrip(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	auth := bearer(testAuthToken)

	for _, tc := range []struct{ file, path string }{
		{"agents", config.AgentsMDPath()},
		{"user", config.UserMDPath()},
	} {
		got := decodePersona(t, 200, f.do("GET", "/api/persona/"+tc.file, nil, auth).Body.Bytes())
		if got["content"] != "" || got["path"] != tc.path {
			t.Fatalf("%s: fresh file = %v", tc.file, got)
		}
		rr := f.do("POST", "/api/persona/"+tc.file, map[string]string{"content": "  hello " + tc.file + "\n"}, auth)
		got = decodePersona(t, rr.Code, rr.Body.Bytes())
		if got["content"] != "hello "+tc.file {
			t.Fatalf("%s: saved = %v", tc.file, got)
		}
		got = decodePersona(t, 200, f.do("GET", "/api/persona/"+tc.file, nil, auth).Body.Bytes())
		if got["content"] != "hello "+tc.file {
			t.Fatalf("%s: reread = %v", tc.file, got)
		}
		// Saving empty removes the file.
		rr = f.do("POST", "/api/persona/"+tc.file, map[string]string{"content": ""}, auth)
		decodePersona(t, rr.Code, rr.Body.Bytes())
		if _, err := os.Stat(tc.path); !os.IsNotExist(err) {
			t.Fatalf("%s: empty save left the file (err=%v)", tc.file, err)
		}
	}
}

func TestPersonaSoulMatchesLegacyAlias(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	auth := bearer(testAuthToken)

	rr := f.do("POST", "/api/persona/soul", map[string]string{"content": "I am Vega."}, auth)
	got := decodePersona(t, rr.Code, rr.Body.Bytes())
	if got["content"] != "I am Vega." || got["unset"] != false || got["path"] != config.SoulPath() {
		t.Fatalf("persona soul = %v", got)
	}
	legacy := decodePersona(t, 200, f.do("GET", "/api/soul", nil, auth).Body.Bytes())
	if legacy["soul"] != "I am Vega." {
		t.Fatalf("/api/soul = %v", legacy)
	}
}

func TestPersonaUnknownFileAndAuth(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	if rr := f.do("GET", "/api/persona/secrets", nil, bearer(testAuthToken)); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown file: status %d", rr.Code)
	}
	// Unauthenticated writes are refused exactly as for /api/soul.
	soul := f.do("POST", "/api/soul", map[string]string{"soul": "x"})
	for _, file := range []string{"soul", "agents", "user"} {
		rr := f.do("POST", "/api/persona/"+file, map[string]string{"content": "x"})
		if rr.Code == http.StatusOK || rr.Code != soul.Code {
			t.Fatalf("%s: unauthenticated POST status %d, /api/soul gives %d", file, rr.Code, soul.Code)
		}
	}
	if config.LoadAgentsMD() != "" || config.LoadUserMD() != "" {
		t.Fatal("unauthenticated POST wrote a file")
	}
}
