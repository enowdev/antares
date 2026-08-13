package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/agent"
)

func TestLiveRun_ReplayThenFollow(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{Type: agent.EventText, Delta: "a"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "b"})

	// A follower joining late must first replay the backlog, then see new events.
	got := make(chan string, 8)
	go func() {
		_ = lr.follow(context.Background(), 0, func(e agent.Event, _ int) error {
			got <- e.Delta
			return nil
		})
		close(got)
	}()

	// Adjacent backlog deltas are coalesced into one frame.
	if v := <-got; v != "ab" {
		t.Fatalf("want ab, got %q", v)
	}
	// Live.
	lr.publish(agent.Event{Type: agent.EventText, Delta: "c"})
	if v := <-got; v != "c" {
		t.Fatalf("want c, got %q", v)
	}
	// Finish closes the follower.
	lr.finish()
	select {
	case _, ok := <-got:
		if ok {
			// drain any trailing value then expect close
			<-got
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not return after finish")
	}
}

func TestLiveRun_CoalescesBacklogAndReportsAbsoluteCursor(t *testing.T) {
	lr := newLiveRun()
	for i := 0; i < 4000; i++ {
		lr.publish(agent.Event{Type: agent.EventReasoning, Delta: "x"})
	}
	lr.publish(agent.Event{Type: agent.EventUsage, InputTokens: 10})
	lr.finish()

	var frames []agent.Event
	var cursors []int
	if err := lr.follow(context.Background(), 0, func(e agent.Event, cursor int) error {
		frames = append(frames, e)
		cursors = append(cursors, cursor)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("4001 backlog events produced %d frames, want 4", len(frames))
	}
	if frames[0].Type != agent.EventReset {
		t.Fatalf("first compacted frame=%q, want reset", frames[0].Type)
	}
	if got := len(frames[1].Delta); got != 1 {
		t.Fatalf("checkpoint reasoning length = %d, want 1", got)
	}
	if got := len(frames[2].Delta); got != 3999 {
		t.Fatalf("coalesced retained reasoning length = %d, want 3999", got)
	}
	if cursors[0] != 0 || cursors[1] != 1 ||
		cursors[2] != 4000 || cursors[3] != 4001 {
		t.Fatalf("absolute cursors = %v, want [0 1 4000 4001]", cursors)
	}

	// Reattaching at the reported cursor must not replay the reasoning backlog.
	var replayed []agent.Event
	if err := lr.follow(context.Background(), cursors[2], func(e agent.Event, _ int) error {
		replayed = append(replayed, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].Type != agent.EventUsage {
		t.Fatalf("cursor replay = %#v, want only usage event", replayed)
	}
}

func TestLiveRun_CompactionRetainsCanonicalReplayCheckpoint(t *testing.T) {
	lr := newCursorLiveRun(liveRunCursorRecovery)
	all := []agent.Event{{Type: agent.EventReset}}
	lr.publish(all[0])

	var wantText, wantReasoning strings.Builder
	for i := 0; i < maxLiveEvents+137; i++ {
		var event agent.Event
		if i%2 == 0 {
			event = agent.Event{
				Type:  agent.EventReasoning,
				Delta: fmt.Sprintf("r%04d|", i),
			}
			wantReasoning.WriteString(event.Delta)
		} else {
			event = agent.Event{
				Type:  agent.EventText,
				Delta: fmt.Sprintf("t%04d|", i),
			}
			wantText.WriteString(event.Delta)
		}
		all = append(all, event)
		lr.publish(event)
	}

	// This follower has already rendered all but the final ten original events.
	// It must continue from its absolute cursor without receiving a reset.
	nearCursor := len(all) - 10
	prefixText, prefixReasoning := renderCanonicalLiveEvents(all[:nearCursor])

	done := agent.Event{Type: agent.EventDone}
	all = append(all, done)
	lr.publish(done)
	lr.finish()

	for reconnect := 1; reconnect <= 2; reconnect++ {
		events, cursors := collectLiveReplay(t, lr, 0)
		text, reasoning := renderCanonicalLiveEvents(events)
		if text != wantText.String() || reasoning != wantReasoning.String() {
			t.Fatalf(
				"reconnect %d canonical mismatch: text=%d/%d reasoning=%d/%d",
				reconnect, len(text), wantText.Len(), len(reasoning), wantReasoning.Len(),
			)
		}
		if len(events) == 0 || events[0].Type != agent.EventReset {
			t.Fatalf("reconnect %d first event=%v, want reset", reconnect, events)
		}
		if got := cursors[len(cursors)-1]; got != len(all) {
			t.Fatalf("reconnect %d final cursor=%d, want %d", reconnect, got, len(all))
		}
	}

	nearEvents, nearCursors := collectLiveReplay(t, lr, nearCursor)
	for _, event := range nearEvents {
		if event.Type == agent.EventReset {
			t.Fatal("near-end follower was unnecessarily reset")
		}
	}
	nearText, nearReasoning := renderCanonicalLiveEventsFrom(
		prefixText, prefixReasoning, nearEvents,
	)
	if nearText != wantText.String() || nearReasoning != wantReasoning.String() {
		t.Fatalf("near-end continuation mismatch: text=%d/%d reasoning=%d/%d",
			len(nearText), wantText.Len(), len(nearReasoning), wantReasoning.Len())
	}
	if got := nearCursors[len(nearCursors)-1]; got != len(all) {
		t.Fatalf("near-end final cursor=%d, want %d", got, len(all))
	}

	lr.mu.Lock()
	retained := len(lr.events)
	lr.mu.Unlock()
	if retained > maxLiveEvents {
		t.Fatalf("retained events=%d, max=%d", retained, maxLiveEvents)
	}
}

func TestLiveRun_CompactedCheckpointSurvivesMidAnchorReconnect(t *testing.T) {
	lr := newCursorLiveRun(liveRunCursorRecovery)
	lr.publish(agent.Event{Type: agent.EventReset})
	lr.publish(agent.Event{Type: agent.EventReasoning, Delta: "complete reasoning"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "complete text"})
	for range maxLiveEvents {
		lr.publish(agent.Event{Type: agent.EventNotice, Message: "progress"})
	}
	lr.finish()

	stop := errors.New("disconnect during replay checkpoint")
	for disconnectAfter := 1; disconnectAfter <= 2; disconnectAfter++ {
		var before []agent.Event
		lastCursor := 0
		err := lr.follow(context.Background(), 0, func(event agent.Event, cursor int) error {
			before = append(before, event)
			lastCursor = cursor
			if len(before) == disconnectAfter {
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) {
			t.Fatalf("disconnect %d follow error=%v, want sentinel", disconnectAfter, err)
		}

		after, _ := collectLiveReplay(t, lr, lastCursor)
		text, reasoning := renderCanonicalLiveEvents(append(before, after...))
		if text != "complete text" || reasoning != "complete reasoning" {
			t.Fatalf("disconnect %d lost checkpoint: text=%q reasoning=%q cursor=%d",
				disconnectAfter, text, reasoning, lastCursor)
		}
	}
}

func TestLiveRun_CompactedCheckpointMemoryIsBounded(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{
		Type:  agent.EventText,
		Delta: strings.Repeat("界", maxLiveReplaySnapshotRunes+17),
	})
	for range maxLiveEvents {
		lr.publish(agent.Event{Type: agent.EventNotice, Message: "progress"})
	}

	lr.mu.Lock()
	text := string(lr.replayText)
	textRunes := lr.replayTextRunes
	retained := len(lr.events)
	lr.mu.Unlock()
	if textRunes != maxLiveReplaySnapshotRunes ||
		utf8.RuneCountInString(text) != maxLiveReplaySnapshotRunes {
		t.Fatalf("checkpoint snapshot runes=(%d,%d), want bounded %d",
			textRunes, utf8.RuneCountInString(text), maxLiveReplaySnapshotRunes)
	}
	if retained != maxLiveEvents {
		t.Fatalf("retained events=%d, want %d", retained, maxLiveEvents)
	}
}

func collectLiveReplay(t *testing.T, lr *liveRun, cursor int) ([]agent.Event, []int) {
	t.Helper()
	var events []agent.Event
	var cursors []int
	if err := lr.follow(context.Background(), cursor, func(event agent.Event, next int) error {
		events = append(events, event)
		cursors = append(cursors, next)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return events, cursors
}

func renderCanonicalLiveEvents(events []agent.Event) (text, reasoning string) {
	return renderCanonicalLiveEventsFrom("", "", events)
}

func renderCanonicalLiveEventsFrom(
	text string,
	reasoning string,
	events []agent.Event,
) (string, string) {
	var textBuilder, reasoningBuilder strings.Builder
	textBuilder.WriteString(text)
	reasoningBuilder.WriteString(reasoning)
	for _, event := range events {
		switch event.Type {
		case agent.EventReset:
			textBuilder.Reset()
			reasoningBuilder.Reset()
		case agent.EventText:
			textBuilder.WriteString(event.Delta)
		case agent.EventReasoning:
			reasoningBuilder.WriteString(event.Delta)
		}
	}
	return textBuilder.String(), reasoningBuilder.String()
}

func TestLiveRun_FollowFromCursor(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{Type: agent.EventText, Delta: "x"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "y"})
	lr.finish()

	var seen []string
	_ = lr.follow(context.Background(), 1, func(e agent.Event, _ int) error {
		seen = append(seen, e.Delta)
		return nil
	})
	if len(seen) != 1 || seen[0] != "y" {
		t.Fatalf("cursor replay wrong: %v", seen)
	}
}

func TestLiveRun_FollowStopsOnContextCancel(t *testing.T) {
	lr := newLiveRun()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- lr.follow(ctx, 0, func(agent.Event, int) error { return nil })
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context error")
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop on cancel")
	}
}

func TestLiveHub_PutGetRemove(t *testing.T) {
	h := newLiveHub()
	lr := newLiveRun()
	h.put("s1", lr)
	if h.get("s1") != lr {
		t.Fatal("get did not return the run")
	}
	// A stale remove (different run) must not evict the current one.
	h.remove("s1", newLiveRun())
	if h.get("s1") != lr {
		t.Fatal("stale remove evicted the live run")
	}
	h.remove("s1", lr)
	if h.get("s1") != nil {
		t.Fatal("run not removed")
	}
}
