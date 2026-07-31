package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/vps"
)

// The VPS API stores servers (SSH credentials encrypted at rest by the store)
// and reads their live state on demand over SSH. Secrets are never returned to
// the client — list/get mask them and only report whether each is set.

func targetFor(v *store.VPSHost) vps.Target {
	return vps.Target{
		Host: v.Host, Port: v.Port, Username: v.Username, AuthMethod: v.AuthMethod,
		Password: v.Password, PrivateKey: v.PrivateKey, Passphrase: v.Passphrase,
		KnownHostKey: v.HostKey,
	}
}

// pinHostKey records a server's SSH key on first connect (TOFU). It only writes
// when the host had no key yet and the connect produced one, so a later key
// change is a blocking ErrHostKeyChanged rather than a silent re-pin.
func (s *Server) pinHostKey(ctx context.Context, v *store.VPSHost, seen string) {
	if v == nil || v.HostKey != "" || strings.TrimSpace(seen) == "" {
		return
	}
	if err := s.db.SetVPSHostKey(ctx, v.ID, seen); err != nil {
		slog.Warn("vps: could not pin host key", "id", v.ID, "error", err)
	}
}

// vpsView renders a host for the API with all secrets stripped — only booleans
// saying whether a password / key is on file.
func vpsView(v store.VPSHost) map[string]any {
	return map[string]any{
		"id":           v.ID,
		"label":        v.Label,
		"host":         v.Host,
		"port":         v.Port,
		"username":     v.Username,
		"auth_method":  v.AuthMethod,
		"has_password": v.Password != "",
		"has_key":      v.PrivateKey != "",
		"folder_id":    v.FolderID,
		"sort_order":   v.SortOrder,
		"created_at":   v.CreatedAt,
		"updated_at":   v.UpdatedAt,
	}
}

func (s *Server) handleListVPS(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.ListVPSHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]map[string]any, 0, len(hosts))
	for _, v := range hosts {
		out = append(out, vpsView(v))
	}
	folders, err := s.db.ListVPSFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": out, "folders": folders})
}

type vpsBody struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthMethod string `json:"auth_method"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	FolderID   string `json:"folder_id"`
}

// handleSaveVPS creates or updates a host. On update, a blank password/key
// keeps the stored one (the client never receives the secret to echo back).
func (s *Server) handleSaveVPS(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Host) == "" {
		writeError(w, http.StatusBadRequest, errors.New("host is required"))
		return
	}

	v := &store.VPSHost{
		ID:         strings.TrimSpace(body.ID),
		Label:      strings.TrimSpace(body.Label),
		Host:       strings.TrimSpace(body.Host),
		Port:       body.Port,
		Username:   strings.TrimSpace(body.Username),
		AuthMethod: strings.TrimSpace(body.AuthMethod),
		Password:   body.Password,
		PrivateKey: body.PrivateKey,
		Passphrase: body.Passphrase,
		FolderID:   strings.TrimSpace(body.FolderID),
	}
	if v.Username == "" {
		v.Username = "root"
	}
	if v.AuthMethod == "" {
		if v.PrivateKey != "" {
			v.AuthMethod = "key"
		} else {
			v.AuthMethod = "password"
		}
	}
	if v.Label == "" {
		v.Label = v.Host
	}

	if v.ID == "" {
		v.ID = "vps_" + randHex(6)
	} else {
		// Editing: preserve secrets left blank so a round trip through the masked
		// view never wipes them.
		if prev, err := s.db.GetVPSHost(r.Context(), v.ID); err == nil {
			v.CreatedAt = prev.CreatedAt
			v.FolderID = prev.FolderID
			v.SortOrder = prev.SortOrder
			if v.Password == "" {
				v.Password = prev.Password
			}
			if v.PrivateKey == "" {
				v.PrivateKey = prev.PrivateKey
			}
			if v.Passphrase == "" {
				v.Passphrase = prev.Passphrase
			}
		}
	}

	if err := s.db.PutVPSHost(r.Context(), v); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": v.ID})
}

func (s *Server) handleDeleteVPS(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.db.DeleteVPSHost(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type vpsFolderBody struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

func (s *Server) handleCreateVPSFolder(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsFolderBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	f := &store.VPSFolder{
		ID:       "vpsf_" + randHex(6),
		Name:     strings.TrimSpace(body.Name),
		ParentID: strings.TrimSpace(body.ParentID),
	}
	if err := s.db.CreateVPSFolder(r.Context(), f); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": f.ID})
}

func (s *Server) handleRenameVPSFolder(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsFolderBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.RenameVPSFolder(r.Context(), r.PathValue("id"), body.Name); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type vpsFolderMoveBody struct {
	ParentID string `json:"parent_id"`
	Index    int    `json:"index"`
}

func (s *Server) handleMoveVPSFolder(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsFolderMoveBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.MoveVPSFolder(r.Context(), r.PathValue("id"), strings.TrimSpace(body.ParentID), body.Index); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type vpsHostMoveBody struct {
	FolderID string `json:"folder_id"`
	Index    int    `json:"index"`
}

func (s *Server) handleMoveVPSHost(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsHostMoveBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.MoveVPSHost(r.Context(), r.PathValue("id"), strings.TrimSpace(body.FolderID), body.Index); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteVPSFolder(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if err := s.db.DeleteVPSFolder(r.Context(), r.PathValue("id")); err != nil {
		writeVPSStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeVPSStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrInvalidVPSHierarchy):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

// handleTestVPS dials a saved host (by id) or an ad-hoc one from the body and
// reports whether it connected, plus the remote user@hostname it reached.
func (s *Server) handleTestVPS(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body vpsBody
	_ = decodeBody(r, &body)

	var t vps.Target
	var saved *store.VPSHost // set only when testing a stored host, so we can pin
	if id := strings.TrimSpace(body.ID); id != "" {
		v, err := s.db.GetVPSHost(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "host not found"})
			return
		}
		saved = v
		t = targetFor(v)
		// Allow overriding a just-typed secret before saving.
		if body.Password != "" {
			t.Password = body.Password
		}
		if body.PrivateKey != "" {
			t.PrivateKey = body.PrivateKey
		}
	} else {
		t = vps.Target{
			Host: strings.TrimSpace(body.Host), Port: body.Port,
			Username: strings.TrimSpace(body.Username), AuthMethod: strings.TrimSpace(body.AuthMethod),
			Password: body.Password, PrivateKey: body.PrivateKey, Passphrase: body.Passphrase,
		}
	}
	if t.Host == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "host is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	who, seen, err := vps.Ping(ctx, t)
	if err != nil {
		// A changed host key is a security signal the user should see verbatim;
		// other dial failures pass through their message too (this is the user's
		// own tool, behind the dashboard auth).
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.pinHostKey(ctx, saved, seen) // no-op for ad-hoc tests and already-pinned hosts
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "as": who})
}

// handleVPSMetrics reads a fresh snapshot of one saved host over SSH.
func (s *Server) handleVPSMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, err := s.db.GetVPSHost(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	m, seen := vps.Collect(ctx, targetFor(v))
	if m.Reachable {
		s.pinHostKey(ctx, v, seen)
	}
	writeJSON(w, http.StatusOK, m)
}

// handleVPSProcesses lists the full running process table of one host, for the
// "show processes" modal.
func (s *Server) handleVPSProcesses(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, err := s.db.GetVPSHost(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	procs, seen, err := vps.Processes(ctx, targetFor(v))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "processes": []any{}})
		return
	}
	s.pinHostKey(ctx, v, seen)
	writeJSON(w, http.StatusOK, map[string]any{"processes": procs})
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
