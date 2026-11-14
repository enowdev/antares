package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/enowdev/antares/internal/secret"
)

func newVPSTestStore(t *testing.T) *sqlStore {
	t.Helper()
	ss := newTestStore(t).(*sqlStore)
	ss.boxOnce.Do(func() { ss.box, ss.boxErr = secret.NewBox(bytes.Repeat([]byte{7}, 32)) })
	return ss
}

func TestVPSHostTunnelFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	ss := newVPSTestStore(t)
	h := &VPSHost{ID: "vps_t", Host: "ssh.example.com", Tunnel: "cloudflared",
		CFClientID: "abc.access", CFClientSecret: "s3cret", AuthMethod: "password", Password: "pw"}
	if err := ss.PutVPSHost(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, err := ss.GetVPSHost(ctx, "vps_t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tunnel != "cloudflared" || got.CFClientID != "abc.access" || got.CFClientSecret != "s3cret" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	var raw string
	if err := ss.row(ctx, `SELECT cf_client_secret FROM vps_hosts WHERE id='vps_t'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "" || raw == "s3cret" {
		t.Fatalf("cf_client_secret not encrypted at rest: %q", raw)
	}
	h.CFClientSecret = "new"
	if err := ss.PutVPSHost(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, err = ss.GetVPSHost(ctx, "vps_t")
	if err != nil || got.CFClientSecret != "new" {
		t.Fatalf("update: %v %+v", err, got)
	}
}

func TestVPSHostDefaultsToDirect(t *testing.T) {
	ctx := context.Background()
	ss := newVPSTestStore(t)
	// A row as a pre-v4 binary wrote it: none of the tunnel columns are named.
	if _, err := ss.db.ExecContext(ctx, `INSERT INTO vps_hosts
		(id, label, host, port, username, auth_method, password, private_key, passphrase, host_key, created_at, updated_at)
		VALUES ('vps_d', 'old', '1.2.3.4', 22, 'root', 'password', '', '', '', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	got, err := ss.GetVPSHost(ctx, "vps_d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tunnel != "" || got.CFClientID != "" || got.CFClientSecret != "" {
		t.Fatalf("expected direct defaults: %+v", got)
	}
}
