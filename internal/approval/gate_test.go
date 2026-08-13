package approval

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateRetainsImmutableOperation(t *testing.T) {
	g := NewGate(time.Minute)
	op := Operation{SessionID: "ses-1", Tool: "cursor_agent", Arguments: `{"model":"a"}`, Message: "Start Cursor"}
	emitted := make(chan Request, 1)
	done := make(chan bool, 1)
	go func() {
		ok, _ := g.Await(context.Background(), op, func(r Request) error {
			emitted <- r
			return nil
		})
		done <- ok
	}()
	req := <-emitted
	op.Arguments = `{"model":"b"}`
	if !g.Resolve(req.ID, true) || !<-done {
		t.Fatal("approval did not resolve")
	}
	if got := req.Arguments; got != `{"model":"a"}` {
		t.Fatalf("arguments mutated: %s", got)
	}
}

func TestGatePendingIsOldestFirstAndCopied(t *testing.T) {
	g := NewGate(time.Minute)
	first := make(chan Request, 1)
	second := make(chan Request, 1)

	go func() {
		_, _ = g.Await(context.Background(), Operation{Tool: "first", Arguments: `{"n":1}`}, func(r Request) error {
			first <- r
			return nil
		})
	}()
	firstReq := <-first

	go func() {
		_, _ = g.Await(context.Background(), Operation{Tool: "second", Arguments: `{"n":2}`}, func(r Request) error {
			second <- r
			return nil
		})
	}()
	secondReq := <-second

	pending := g.Pending()
	if len(pending) != 2 {
		t.Fatalf("pending length = %d, want 2", len(pending))
	}
	if pending[0].ID != firstReq.ID || pending[1].ID != secondReq.ID {
		t.Fatalf("pending order = [%s %s], want [%s %s]",
			pending[0].ID, pending[1].ID, firstReq.ID, secondReq.ID)
	}

	pending[0].Arguments = "changed"
	again := g.Pending()
	if again[0].Arguments != `{"n":1}` {
		t.Fatalf("Pending exposed mutable state: %q", again[0].Arguments)
	}

	if !g.Resolve(firstReq.ID, false) || !g.Resolve(secondReq.ID, false) {
		t.Fatal("cleanup resolutions failed")
	}
}

func TestGateDenyRemovesRequest(t *testing.T) {
	g := NewGate(time.Minute)
	emitted := make(chan Request, 1)
	done := make(chan struct {
		ok  bool
		err error
	}, 1)

	go func() {
		ok, err := g.Await(context.Background(), Operation{Tool: "write_file"}, func(r Request) error {
			emitted <- r
			return nil
		})
		done <- struct {
			ok  bool
			err error
		}{ok: ok, err: err}
	}()

	req := <-emitted
	if !g.Resolve(req.ID, false) {
		t.Fatal("deny did not resolve")
	}
	result := <-done
	if result.ok || result.err != nil {
		t.Fatalf("deny result = (%v, %v), want (false, nil)", result.ok, result.err)
	}
	if pending := g.Pending(); len(pending) != 0 {
		t.Fatalf("denied request remained pending: %+v", pending)
	}
	if g.Resolve(req.ID, true) {
		t.Fatal("denied request resolved twice")
	}
}

func TestGateTimeoutRemovesRequest(t *testing.T) {
	g := NewGate(20 * time.Millisecond)
	emitted := make(chan Request, 1)
	done := make(chan error, 1)

	go func() {
		ok, err := g.Await(context.Background(), Operation{Tool: "write_file"}, func(r Request) error {
			emitted <- r
			return nil
		})
		if ok {
			done <- errors.New("timed-out request was allowed")
			return
		}
		done <- err
	}()

	req := <-emitted
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want context deadline exceeded", err)
	}
	if pending := g.Pending(); len(pending) != 0 {
		t.Fatalf("timed-out request remained pending: %+v", pending)
	}
	if g.Resolve(req.ID, true) {
		t.Fatal("timed-out request resolved")
	}
}

func TestGateContextCancellationRemovesRequest(t *testing.T) {
	g := NewGate(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	emitted := make(chan Request, 1)
	done := make(chan error, 1)

	go func() {
		ok, err := g.Await(ctx, Operation{Tool: "write_file"}, func(r Request) error {
			emitted <- r
			return nil
		})
		if ok {
			done <- errors.New("cancelled request was allowed")
			return
		}
		done <- err
	}()

	req := <-emitted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v, want context canceled", err)
	}
	if pending := g.Pending(); len(pending) != 0 {
		t.Fatalf("cancelled request remained pending: %+v", pending)
	}
	if g.Resolve(req.ID, true) {
		t.Fatal("cancelled request resolved")
	}
}

func TestGateResolveUnknownID(t *testing.T) {
	if NewGate(time.Minute).Resolve("apr_missing", true) {
		t.Fatal("unknown request resolved")
	}
}

func TestGateConcurrentResolutionOnlyWinsOnce(t *testing.T) {
	g := NewGate(time.Minute)
	emitted := make(chan Request, 1)
	done := make(chan bool, 1)
	go func() {
		ok, _ := g.Await(context.Background(), Operation{Tool: "cursor_agent"}, func(r Request) error {
			emitted <- r
			return nil
		})
		done <- ok
	}()
	req := <-emitted

	const contenders = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	var wins atomic.Int32
	winner := make(chan bool, 1)
	for i := 0; i < contenders; i++ {
		allow := i%2 == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if g.Resolve(req.ID, allow) {
				wins.Add(1)
				winner <- allow
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("successful resolutions = %d, want 1", got)
	}
	if got, want := <-done, <-winner; got != want {
		t.Fatalf("await result = %v, winning resolution = %v", got, want)
	}
	if pending := g.Pending(); len(pending) != 0 {
		t.Fatalf("resolved request remained pending: %+v", pending)
	}
}
