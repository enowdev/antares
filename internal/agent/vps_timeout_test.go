package agent

import (
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestVPSToolTimeoutExceedsMaxCommandTimeout(t *testing.T) {
	a := &Agent{cfg: config.Default()}
	for _, name := range []string{"vps_run", "vps_upload", "vps_download"} {
		got := a.toolTimeout(name)
		// Tools allow timeout_seconds up to 900; the agent envelope must not
		// cut them off earlier (that produced bare "context deadline exceeded"
		// failures on long systemctl/apt runs).
		if got < 15*time.Minute {
			t.Fatalf("%s tool timeout = %s, want at least 15m (above 900s max command timeout)", name, got)
		}
	}
}

func TestVPSToolTimeoutFromConfigMap(t *testing.T) {
	cfg := config.Default()
	if cfg.Tools.Timeouts["vps_run"] < 900 {
		t.Fatalf("default config vps_run timeout = %d, want >= 900", cfg.Tools.Timeouts["vps_run"])
	}
}
