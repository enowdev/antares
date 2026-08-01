package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/mcp"
)

type fakeMCPRefresher struct {
	called bool
}

func (f *fakeMCPRefresher) Refresh(context.Context, *config.Config) []mcp.ServerStatus {
	f.called = true
	return []mcp.ServerStatus{{
		Name:      "ida",
		Started:   true,
		Connected: true,
		Tools:     []mcp.ToolDef{{Name: "server_health"}},
	}}
}

func TestMCPRefreshHandlerReturnsFreshStatus(t *testing.T) {
	s := &Server{cfg: &config.Config{MCP: config.MCP{Enabled: true}}}
	refresher := &fakeMCPRefresher{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/refresh", nil)
	w := httptest.NewRecorder()

	s.refreshMCP(w, req, refresher)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !refresher.called {
		t.Fatal("handler did not invoke MCP refresh")
	}
	var body struct {
		Enabled bool               `json:"enabled"`
		Servers []mcp.ServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || len(body.Servers) != 1 || !body.Servers[0].Connected || len(body.Servers[0].Tools) != 1 {
		t.Fatalf("response = %+v, want refreshed connected server", body)
	}
}

func TestMCPRefreshHandlerRejectsDisabledMCP(t *testing.T) {
	s := &Server{cfg: &config.Config{MCP: config.MCP{Enabled: false}}}
	refresher := &fakeMCPRefresher{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/refresh", nil)
	w := httptest.NewRecorder()

	s.refreshMCP(w, req, refresher)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if refresher.called {
		t.Fatal("disabled MCP unexpectedly refreshed")
	}
}
