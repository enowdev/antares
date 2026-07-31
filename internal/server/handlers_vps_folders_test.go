package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func TestVPSFolderAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	s := New(Options{
		Config: &config.Config{Server: config.Server{DashboardPasswordHash: "configured"}},
		Store:  db,
	})

	request := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		var raw bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&raw).Encode(body); err != nil {
				t.Fatalf("encode request: %v", err)
			}
		}
		req := httptest.NewRequest(method, path, &raw)
		res := httptest.NewRecorder()
		s.mux.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, res.Code, res.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return result
	}

	parent := request(http.MethodPost, "/api/vps/folders", map[string]any{"name": "Production", "parent_id": ""}, http.StatusOK)
	parentID, _ := parent["id"].(string)
	child := request(http.MethodPost, "/api/vps/folders", map[string]any{"name": "Web", "parent_id": parentID}, http.StatusOK)
	childID, _ := child["id"].(string)
	host := request(http.MethodPost, "/api/vps", map[string]any{
		"label": "web-01", "host": "web-01.example", "folder_id": parentID,
	}, http.StatusOK)
	hostID, _ := host["id"].(string)
	request(http.MethodPut, "/api/vps/hosts/"+hostID+"/move", map[string]any{
		"folder_id": childID, "index": 0,
	}, http.StatusOK)
	request(http.MethodPut, "/api/vps/hosts/"+hostID+"/move", map[string]any{
		"folder_id": parentID, "index": 0,
	}, http.StatusOK)

	request(http.MethodPut, "/api/vps/folders/"+parentID+"/move", map[string]any{
		"parent_id": childID, "index": 0,
	}, http.StatusBadRequest)
	request(http.MethodDelete, "/api/vps/folders/"+parentID, nil, http.StatusOK)

	list := request(http.MethodGet, "/api/vps", nil, http.StatusOK)
	folders, _ := list["folders"].([]any)
	if len(folders) != 1 || folders[0].(map[string]any)["parent_id"] != "" {
		t.Fatalf("child folder was not promoted: %+v", folders)
	}
	hosts, _ := list["hosts"].([]any)
	if len(hosts) != 1 || hosts[0].(map[string]any)["folder_id"] != "" {
		t.Fatalf("host was not promoted: %+v", hosts)
	}
}
