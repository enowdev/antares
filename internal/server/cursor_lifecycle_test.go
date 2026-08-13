package server

import (
	"testing"
	"time"
)

func TestSessionLockerDoesNotBlockUnrelatedSessions(t *testing.T) {
	var locker sessionLocker
	unlockA := locker.Lock("session-a")

	acquiredB := make(chan func(), 1)
	go func() {
		acquiredB <- locker.Lock("session-b")
	}()
	select {
	case unlockB := <-acquiredB:
		unlockB()
	case <-time.After(250 * time.Millisecond):
		t.Fatal("session-a lock blocked unrelated session-b")
	}

	acquiredA := make(chan func(), 1)
	go func() {
		acquiredA <- locker.Lock("session-a")
	}()
	select {
	case unlock := <-acquiredA:
		unlock()
		t.Fatal("same-session lock did not serialize")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	select {
	case unlock := <-acquiredA:
		unlock()
	case <-time.After(time.Second):
		t.Fatal("same-session waiter did not resume")
	}
}

func TestSessionLockerLockManyUsesDeterministicOrder(t *testing.T) {
	var locker sessionLocker
	unlockFirst := locker.LockMany([]string{"session-b", "session-a", "session-a"})

	acquired := make(chan func(), 1)
	go func() {
		acquired <- locker.LockMany([]string{"session-a", "session-b"})
	}()
	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("overlapping multi-session lock was not serialized")
	case <-time.After(50 * time.Millisecond):
	}
	unlockFirst()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(time.Second):
		t.Fatal("deterministically ordered multi-session waiter deadlocked")
	}
}
