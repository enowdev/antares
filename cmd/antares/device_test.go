package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func TestDevicePairCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PORT", "18999")

	var out bytes.Buffer
	if err := cmdDevicePair(&out, []string{"--name", "Test Mac", "--platform=desktop-macos", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Device struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Platform  string `json:"platform"`
			CreatedAt string `json:"created_at"`
		} `json:"device"`
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got.Device.Name != "Test Mac" || got.Device.Platform != "desktop-macos" || got.Device.CreatedAt == "" {
		t.Fatalf("device: %+v", got.Device)
	}
	if !store.LooksLikeDeviceToken(got.Token) {
		t.Fatal("token shape")
	}
	if got.URL != "http://127.0.0.1:18999" {
		t.Fatalf("url without a daemon should come from config: %q", got.URL)
	}

	// The row is in the local store under the token's hash, never the token.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), cfg.Database.Driver, cfg.Database.DSN, 2, 5000, true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.DeviceByTokenHash(context.Background(), store.HashDeviceToken(got.Token))
	db.Close()
	if err != nil || d.ID != got.Device.ID {
		t.Fatalf("stored: %v", err)
	}

	// Defaults: host name, platform cli; unknown platform becomes other.
	out.Reset()
	if err := cmdDevicePair(&out, []string{"--platform", "fridge", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"platform":"other"`) {
		t.Fatalf("%s", out.String())
	}
	o, _ := parseDevicePairArgs(nil)
	if o.Platform != "cli" || o.Name == "" {
		t.Fatalf("defaults: %+v", o)
	}
	if _, err := parseDevicePairArgs([]string{"--name"}); err == nil {
		t.Fatal("missing value should fail")
	}

	out.Reset()
	if err := cmdDeviceRevoke(&out, []string{got.Device.ID}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDeviceRevoke(&out, []string{"dev_missing"}); err == nil {
		t.Fatal("unknown id should fail")
	}
	out.Reset()
	if err := cmdDeviceList(&out, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil || len(list.Devices) != 2 {
		t.Fatalf("list: %v %s", err, out.String())
	}
	revoked := 0
	for _, d := range list.Devices {
		if d["revoked_at"] != nil {
			revoked++
		}
	}
	if revoked != 1 {
		t.Fatalf("revoked count %d", revoked)
	}
}
