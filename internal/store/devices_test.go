package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeviceCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	d, tok, err := PairDevice(ctx, s, "  MacBook Pro  ", "desktop-macos")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if !strings.HasPrefix(tok, "atd_") || len(tok) != 52 || !LooksLikeDeviceToken(tok) {
		t.Fatalf("token shape: len=%d", len(tok))
	}
	if !strings.HasPrefix(d.ID, "dev_") || len(d.ID) != 20 {
		t.Fatalf("id shape: %q", d.ID)
	}
	if d.Name != "MacBook Pro" || d.Platform != "desktop-macos" {
		t.Fatalf("normalised: %+v", d)
	}
	if d.TokenHash != HashDeviceToken(tok) || strings.Contains(d.TokenHash, tok) {
		t.Fatal("stored hash must be sha256 of the token")
	}

	got, err := s.DeviceByTokenHash(ctx, HashDeviceToken(tok))
	if err != nil || got.ID != d.ID || got.Revoked() || got.LastSeenAt != nil {
		t.Fatalf("by hash: %v %+v", err, got)
	}
	if _, err := s.DeviceByTokenHash(ctx, HashDeviceToken("atd_nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}

	// Touch is throttled to once a minute.
	now := time.Now()
	if err := s.TouchDevice(ctx, d.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchDevice(ctx, d.ID, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDevice(ctx, d.ID)
	if got.LastSeenAt == nil || got.LastSeenAt.UnixMilli() != now.UnixMilli() {
		t.Fatalf("touch within a minute must not write: %v", got.LastSeenAt)
	}
	later := now.Add(61 * time.Second)
	_ = s.TouchDevice(ctx, d.ID, later)
	got, _ = s.GetDevice(ctx, d.ID)
	if got.LastSeenAt.UnixMilli() != later.UnixMilli() {
		t.Fatalf("touch after a minute must write: %v", got.LastSeenAt)
	}

	d2, _, err := PairDevice(ctx, s, "cli box", "weird")
	if err != nil || d2.Platform != "other" {
		t.Fatalf("unknown platform -> other: %v %+v", err, d2)
	}
	list, err := s.ListDevices(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	if err := s.RevokeDevice(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDevice(ctx, d.ID)
	if !got.Revoked() {
		t.Fatal("revoked_at not set")
	}
	first := *got.RevokedAt
	time.Sleep(5 * time.Millisecond)
	_ = s.RevokeDevice(ctx, d.ID)
	got, _ = s.GetDevice(ctx, d.ID)
	if !got.RevokedAt.Equal(first) {
		t.Fatal("second revoke must keep the first time")
	}
	if err := s.RevokeDevice(ctx, "dev_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown: %v", err)
	}
	list, _ = s.ListDevices(ctx)
	if len(list) != 2 {
		t.Fatal("revoked rows stay in the list")
	}
}

func TestDeviceNameAndTokenShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"a", true},
		{"   ", false},
		{"", false},
		{strings.Repeat("x", 64), true},
		{strings.Repeat("x", 65), false},
		{strings.Repeat("é", 64), true},
	} {
		_, err := NormalizeDeviceName(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("NormalizeDeviceName(%q) err=%v want ok=%v", tc.name, err, tc.ok)
		}
	}
	for _, tc := range []struct {
		tok string
		ok  bool
	}{
		{"atd_" + strings.Repeat("a", 48), true},
		{"atd_" + strings.Repeat("A", 48), false},
		{"atd_" + strings.Repeat("a", 47), false},
		{"xyz_" + strings.Repeat("a", 48), false},
		{"atd_" + strings.Repeat("g", 48), false},
	} {
		if got := LooksLikeDeviceToken(tc.tok); got != tc.ok {
			t.Errorf("LooksLikeDeviceToken(%q)=%v", tc.tok, got)
		}
	}
}
