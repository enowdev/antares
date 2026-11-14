package server

import (
	"testing"
	"time"

	"github.com/enowdev/antares/internal/store"
)

func TestVPSFromBodyNormalizesTunnelHost(t *testing.T) {
	v := vpsFromBody(vpsBody{Host: "https://ssh.example.com/", Tunnel: "cloudflared"})
	if v.Host != "ssh.example.com" || v.Tunnel != "cloudflared" || v.Username != "root" || v.Label != "ssh.example.com" {
		t.Fatalf("unexpected host: %+v", v)
	}
}

func TestVPSFromBodyUnknownTunnelIsDirect(t *testing.T) {
	v := vpsFromBody(vpsBody{Host: " https://ssh.example.com/ ", Tunnel: "bogus"})
	if v.Tunnel != "" {
		t.Fatalf("tunnel = %q, want empty", v.Tunnel)
	}
	if v.Host != "https://ssh.example.com/" {
		t.Fatalf("direct host should only be trimmed, got %q", v.Host)
	}
}

func TestKeepStoredSecretsKeepsBlankCFSecret(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := &store.VPSHost{CFClientSecret: "old", Password: "pw", CreatedAt: t0}
	v := &store.VPSHost{}
	keepStoredSecrets(v, prev)
	if v.CFClientSecret != "old" || v.Password != "pw" || !v.CreatedAt.Equal(t0) {
		t.Fatalf("blank secrets not kept: %+v", v)
	}
	v2 := &store.VPSHost{CFClientSecret: "new"}
	keepStoredSecrets(v2, prev)
	if v2.CFClientSecret != "new" {
		t.Fatalf("explicit secret overwritten: %q", v2.CFClientSecret)
	}
}

func TestVPSViewHidesCFSecret(t *testing.T) {
	m := vpsView(store.VPSHost{CFClientSecret: "s3cret", CFClientID: "abc", Tunnel: "cloudflared"})
	if m["has_cf_secret"] != true || m["cf_client_id"] != "abc" || m["tunnel"] != "cloudflared" {
		t.Fatalf("bad view: %+v", m)
	}
	for k, val := range m {
		if val == "s3cret" {
			t.Fatalf("secret leaked in %q", k)
		}
	}
}

func TestTargetForMapsTunnel(t *testing.T) {
	tg := targetFor(&store.VPSHost{Tunnel: "cloudflared", CFClientID: "id", CFClientSecret: "sec"})
	if tg.Tunnel != "cloudflared" || tg.CFClientID != "id" || tg.CFClientSecret != "sec" {
		t.Fatalf("bad target: %+v", tg)
	}
}
