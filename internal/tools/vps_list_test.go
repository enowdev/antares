package tools

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/store"
)

func TestFormatVPSListShowsCloudflared(t *testing.T) {
	out := formatVPSList([]store.VPSHost{
		{ID: "a", Label: "tun", Host: "ssh.example.com", Port: 22, Username: "root", Tunnel: "cloudflared"},
		{ID: "b", Label: "dir", Host: "1.2.3.4", Port: 2222, Username: "bob"},
	})
	var tun, dir string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "id=a") {
			tun = l
		}
		if strings.Contains(l, "id=b") {
			dir = l
		}
	}
	if !strings.Contains(tun, "root@ssh.example.com via cloudflared") || strings.Contains(tun, ":22") {
		t.Fatalf("tunnel line: %q", tun)
	}
	if !strings.Contains(dir, "bob@1.2.3.4:2222") {
		t.Fatalf("direct line: %q", dir)
	}
}
