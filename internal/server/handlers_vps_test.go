package server

import (
	"errors"
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
	v := &store.VPSHost{Tunnel: "cloudflared", CFClientID: "id"}
	keepStoredSecrets(v, prev)
	if v.CFClientSecret != "old" || v.Password != "pw" || !v.CreatedAt.Equal(t0) {
		t.Fatalf("blank secrets not kept: %+v", v)
	}
	v2 := &store.VPSHost{Tunnel: "cloudflared", CFClientID: "id", CFClientSecret: "new"}
	keepStoredSecrets(v2, prev)
	if v2.CFClientSecret != "new" {
		t.Fatalf("explicit secret overwritten: %q", v2.CFClientSecret)
	}
}

func TestKeepStoredSecretsCFRules(t *testing.T) {
	prev := &store.VPSHost{Tunnel: "cloudflared", CFClientID: "id", CFClientSecret: "old"}
	// tunnel, Client ID kept, secret blank: keep stored secret
	v := &store.VPSHost{Tunnel: "cloudflared", CFClientID: "id"}
	keepStoredSecrets(v, prev)
	if v.CFClientSecret != "old" {
		t.Errorf("secret not kept: %q", v.CFClientSecret)
	}
	// tunnel, Client ID cleared: secret dropped
	v = &store.VPSHost{Tunnel: "cloudflared"}
	keepStoredSecrets(v, prev)
	if v.CFClientSecret != "" {
		t.Errorf("secret survived cleared Client ID: %q", v.CFClientSecret)
	}
	// direct mode: nothing carried over
	v = vpsFromBody(vpsBody{Host: "1.2.3.4", CFClientID: "id", CFClientSecret: "sec"})
	keepStoredSecrets(v, prev)
	if v.CFClientID != "" || v.CFClientSecret != "" {
		t.Errorf("direct mode kept CF fields: %+v", v)
	}
}

func TestVPSFromBodyEmptyTunnelHost(t *testing.T) {
	if v := vpsFromBody(vpsBody{Host: "https://", Tunnel: "cloudflared"}); v.Host != "" {
		t.Fatalf("Host = %q", v.Host)
	}
}

func TestSameEndpoint(t *testing.T) {
	cases := []struct {
		name string
		a, b store.VPSHost
		want bool
	}{
		{"identical direct", store.VPSHost{Host: "h", Port: 22}, store.VPSHost{Host: "h", Port: 22}, true},
		{"port differs", store.VPSHost{Host: "h", Port: 22}, store.VPSHost{Host: "h", Port: 2222}, false},
		{"host differs", store.VPSHost{Host: "h", Port: 22}, store.VPSHost{Host: "g", Port: 22}, false},
		{"tunnel vs direct", store.VPSHost{Host: "h", Tunnel: "cloudflared"}, store.VPSHost{Host: "h"}, false},
		{"tunnel ports ignored", store.VPSHost{Host: "h", Tunnel: "cloudflared", Port: 1}, store.VPSHost{Host: "h", Tunnel: "cloudflared", Port: 22}, true},
	}
	for _, c := range cases {
		if got := sameEndpoint(&c.a, &c.b); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
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

func TestApplySavedForTestSameEndpointUsesSavedSecrets(t *testing.T) {
	prev := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 22, Password: "pw", HostKey: "ssh-ed25519 AAAA"}
	v := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 22, AuthMethod: "password"}
	pinTo, err := applySavedForTest(v, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinTo != prev {
		t.Fatalf("pinTo = %v, want the saved record", pinTo)
	}
	if v.Password != "pw" || v.HostKey != "ssh-ed25519 AAAA" {
		t.Fatalf("saved secret/host key not used: %+v", v)
	}
}

func TestApplySavedForTestChangedAddressKeepsSavedSecretsHome(t *testing.T) {
	prev := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 22, Password: "pw", PrivateKey: "KEY", Passphrase: "pp",
		Tunnel: "cloudflared", CFClientID: "abc", CFClientSecret: "cfs", HostKey: "ssh-ed25519 AAAA"}
	v := &store.VPSHost{ID: "vps_1", Host: "evil.example.com", Port: 22, AuthMethod: "password", Tunnel: "cloudflared", CFClientID: "abc"}
	pinTo, err := applySavedForTest(v, prev)
	if !errors.Is(err, errAddressChanged) {
		t.Fatalf("err = %v, want errAddressChanged", err)
	}
	if pinTo != nil {
		t.Fatal("must not pin onto the saved record for a different address")
	}
	if v.Password != "" || v.PrivateKey != "" || v.Passphrase != "" || v.CFClientSecret != "" || v.HostKey != "" {
		t.Fatalf("saved secrets leaked into a changed-address test: %+v", v)
	}
}

func TestApplySavedForTestChangedAddressWithTypedSecret(t *testing.T) {
	prev := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 22, Password: "pw", CFClientSecret: "cfs", HostKey: "ssh-ed25519 AAAA"}
	v := &store.VPSHost{ID: "vps_1", Host: "5.6.7.8", Port: 22, AuthMethod: "password", Password: "typed"}
	pinTo, err := applySavedForTest(v, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinTo != nil || v.HostKey != "" {
		t.Fatalf("changed address must run a fresh TOFU check: pinTo=%v hostKey=%q", pinTo, v.HostKey)
	}
	if v.Password != "typed" || v.CFClientSecret != "" {
		t.Fatalf("only typed secrets may be used: %+v", v)
	}

	k := &store.VPSHost{ID: "vps_1", Host: "5.6.7.8", Port: 22, AuthMethod: "key", Password: "typed-but-key-auth"}
	if _, err := applySavedForTest(k, prev); !errors.Is(err, errAddressChanged) {
		t.Fatalf("key auth without a typed key: err = %v, want errAddressChanged", err)
	}
}

func TestApplySavedForTestPortZeroIsDefault(t *testing.T) {
	prev := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 22, Password: "pw"}
	v := &store.VPSHost{ID: "vps_1", Host: "1.2.3.4", Port: 0, AuthMethod: "password"}
	if _, err := applySavedForTest(v, prev); err != nil || v.Password != "pw" {
		t.Fatalf("port 0 should mean the default 22: err=%v password=%q", err, v.Password)
	}
}
