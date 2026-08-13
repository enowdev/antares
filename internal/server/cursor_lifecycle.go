package server

import (
	"sort"
	"sync"
)

type sessionLockEntry struct {
	mu   sync.Mutex
	refs int
}

// sessionLocker serializes Cursor lifecycle decisions per Antares session.
// Entries are reference-counted so completed sessions do not accumulate locks.
type sessionLocker struct {
	mu      sync.Mutex
	entries map[string]*sessionLockEntry
}

func (l *sessionLocker) Lock(sessionID string) func() {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*sessionLockEntry)
	}
	entry := l.entries[sessionID]
	if entry == nil {
		entry = &sessionLockEntry{}
		l.entries[sessionID] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			l.mu.Lock()
			entry.refs--
			if entry.refs == 0 && l.entries[sessionID] == entry {
				delete(l.entries, sessionID)
			}
			l.mu.Unlock()
		})
	}
}

// LockMany acquires unique session IDs in sorted order, preventing two bulk
// lifecycle operations from deadlocking when their input orders differ.
func (l *sessionLocker) LockMany(sessionIDs []string) func() {
	ids := append([]string(nil), sessionIDs...)
	sort.Strings(ids)
	unique := ids[:0]
	for _, id := range ids {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	unlocks := make([]func(), 0, len(unique))
	for _, id := range unique {
		unlocks = append(unlocks, l.Lock(id))
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
		})
	}
}
