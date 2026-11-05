// Package conns keeps the desktop app's saved connections: the
// connections.json file (names, URLs, modes; never secrets) and the URL rules
// a remote connection must satisfy.
package conns

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileVersion is the connections.json schema version this build writes.
const FileVersion = 1

// Mode is how a connection reaches its Antares.
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeRemote Mode = "remote"
)

// Connection is one saved entry. URL is empty for local connections: the
// daemon's URL is read from its state file at connect time.
type Connection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mode      Mode      `json:"mode"`
	URL       string    `json:"url"`
	DeviceID  string    `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
}

// File is the on-disk shape of connections.json.
type File struct {
	Version     int          `json:"version"`
	Last        string       `json:"last"`
	Connections []Connection `json:"connections"`
}

var (
	ErrNotFound       = errors.New("connection not found")
	ErrLocalExists    = errors.New("a local connection already exists")
	ErrNewerFile      = errors.New("connections.json was written by a newer Antares desktop")
	errInvalidMode    = errors.New("mode must be local or remote")
	errNameRequired   = errors.New("name is required")
	errLocalHasURL    = errors.New("a local connection has no URL")
	errRemoteNeedsURL = errors.New("a remote connection needs a URL")
)

// DefaultPath is <user config dir>/Antares/connections.json.
// ANTARES_DESKTOP_CONFIG_DIR replaces the directory (tests, smoke runs).
func DefaultPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("ANTARES_DESKTOP_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "connections.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Antares", "connections.json"), nil
}

// Store reads and writes connections.json. Every mutation is written through
// immediately; the in-memory copy is just the last successful read.
type Store struct {
	path string
	mu   sync.Mutex
	file File
}

// Open loads path (a missing file is an empty store).
func Open(path string) (*Store, error) {
	s := &Store{path: path, file: File{Version: FileVersion}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if f.Version > FileVersion {
		return nil, ErrNewerFile
	}
	f.Version = FileVersion
	if f.Connections == nil {
		f.Connections = []Connection{}
	}
	s.file = f
	return s, nil
}

// Path is the file this store writes.
func (s *Store) Path() string { return s.path }

// List returns a copy of the saved connections, in saved order.
func (s *Store) List() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Connection(nil), s.file.Connections...)
}

// Get returns one connection by id.
func (s *Store) Get(id string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.file.Connections {
		if c.ID == id {
			return c, nil
		}
	}
	return Connection{}, ErrNotFound
}

// Local returns the local connection, if there is one.
func (s *Store) Local() (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.file.Connections {
		if c.Mode == ModeLocal {
			return c, true
		}
	}
	return Connection{}, false
}

// Last is the id of the connection opened most recently ("" if none).
func (s *Store) Last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Last
}

// Add validates c, fills ID/CreatedAt when empty, saves and returns it.
func (s *Store) Add(c Connection) (Connection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return Connection{}, errNameRequired
	}
	switch c.Mode {
	case ModeLocal:
		if c.URL != "" {
			return Connection{}, errLocalHasURL
		}
	case ModeRemote:
		u, err := ValidateURL(c.URL)
		if err != nil {
			return Connection{}, err
		}
		c.URL = u
	default:
		return Connection{}, errInvalidMode
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC().Truncate(time.Second)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.file.Connections {
		if c.Mode == ModeLocal && have.Mode == ModeLocal {
			return Connection{}, ErrLocalExists
		}
		if have.ID == c.ID {
			return Connection{}, fmt.Errorf("duplicate connection id %s", c.ID)
		}
	}
	next := s.file
	next.Connections = append(append([]Connection(nil), s.file.Connections...), c)
	if err := s.write(next); err != nil {
		return Connection{}, err
	}
	return c, nil
}

// Update replaces the connection with the same id (used after re-pairing).
func (s *Store) Update(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.file
	next.Connections = append([]Connection(nil), s.file.Connections...)
	for i := range next.Connections {
		if next.Connections[i].ID == c.ID {
			if next.Connections[i].Mode != c.Mode {
				return errInvalidMode
			}
			next.Connections[i] = c
			return s.write(next)
		}
	}
	return ErrNotFound
}

// Remove deletes a connection; clears Last if it pointed there.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.file
	next.Connections = make([]Connection, 0, len(s.file.Connections))
	found := false
	for _, c := range s.file.Connections {
		if c.ID == id {
			found = true
			continue
		}
		next.Connections = append(next.Connections, c)
	}
	if !found {
		return ErrNotFound
	}
	if next.Last == id {
		next.Last = ""
	}
	return s.write(next)
}

// SetLast records the connection opened most recently.
func (s *Store) SetLast(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		found := false
		for _, c := range s.file.Connections {
			found = found || c.ID == id
		}
		if !found {
			return ErrNotFound
		}
	}
	if s.file.Last == id {
		return nil
	}
	next := s.file
	next.Last = id
	return s.write(next)
}

// write persists f atomically (temp file + rename) and adopts it on success.
func (s *Store) write(f File) error {
	f.Version = FileVersion
	if f.Connections == nil {
		f.Connections = []Connection{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".connections-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmpName, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmpName, s.path)
	}
	if werr != nil {
		_ = os.Remove(tmpName)
		return werr
	}
	s.file = f
	return nil
}

// NewID returns "conn_" + 16 random hex chars.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "conn_" + hex.EncodeToString(b[:])
}
