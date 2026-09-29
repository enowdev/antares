package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/store"
)

func TestRememberKeepsEveryMemory(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	deps := Deps{Store: db}

	for _, text := range []string{"editor: prefers helix", "coffee is black"} {
		if _, err := Run(ctx, deps, Input{Name: "remember", Args: text}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := db.ListMemories(ctx, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want both memories kept, got %d: %+v", len(items), items)
	}
	for _, m := range items {
		if m.ID == "" {
			t.Fatalf("memory stored without an id: %+v", m)
		}
	}

	res, err := Run(ctx, deps, Input{Name: "memory", Args: "helix"})
	if err != nil || !strings.Contains(res.Output, "prefers helix") {
		t.Fatalf("search: %+v %v", res, err)
	}
	if _, err := Run(ctx, deps, Input{Name: "forget", Args: "editor"}); err != nil {
		t.Fatal(err)
	}
	items, _ = db.ListMemories(ctx, "", "", 10)
	if len(items) != 1 || items[0].Content != "coffee is black" {
		t.Fatalf("forget by key removed the wrong thing: %+v", items)
	}
}
