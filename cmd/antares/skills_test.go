package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/skills"
)

func TestSkillRefreshStopsWithLiveParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lifecycle.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("---\nname: lifecycle\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("before stop")
	mgr := skills.NewManager(skills.Options{Dirs: []string{dir}})
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &runtimeServices{skills: mgr}
	rt.startSkillRefresh(parent)
	done := rt.skillsDone
	t.Cleanup(rt.stopSkillRefresh)
	stopped := make(chan struct{})
	go func() { rt.stopSkillRefresh(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not join refresh while parent remained live")
	}
	select {
	case <-done:
	default:
		t.Fatal("stop returned before refresh completed")
	}
	if parent.Err() != nil {
		t.Fatal("stopping refresh canceled its parent")
	}
	rt.stopSkillRefresh()
	(*runtimeServices)(nil).stopSkillRefresh()
	write("after stop")
	deadline := time.NewTimer(5500 * time.Millisecond)
	defer deadline.Stop()
	probe := time.NewTicker(20 * time.Millisecond)
	defer probe.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-probe.C:
			got, ok := mgr.Get("lifecycle")
			if !ok || got.Body != "before stop" {
				t.Fatalf("catalog refreshed after joined stop: %+v", got)
			}
		}
	}
}
