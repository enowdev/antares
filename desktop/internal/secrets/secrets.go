// Package secrets keeps device tokens in the OS keychain: service
// dev.enowdev.antares, account = connection id, secret = the device token.
package secrets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name for every Antares device token.
const Service = "dev.enowdev.antares"

// ErrNotFound means no token is stored for that connection.
var ErrNotFound = errors.New("no token in the keychain for this connection")

// Store is where device tokens live.
type Store interface {
	Get(connID string) (string, error)
	Set(connID, token string) error
	Delete(connID string) error
}

// Keychain is the OS keychain (macOS Keychain, Windows Credential Manager,
// Secret Service on Linux).
type Keychain struct{}

func (Keychain) Get(connID string) (string, error) {
	v, err := keyring.Get(Service, connID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (Keychain) Set(connID, token string) error { return keyring.Set(Service, connID, token) }

// Delete removes the entry; a missing entry is not an error.
func (Keychain) Delete(connID string) error {
	err := keyring.Delete(Service, connID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process Store for tests and ANTARES_DESKTOP_KEYCHAIN=memory.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = token
	return nil
}

func (s *Memory) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// File is a JSON file of tokens (0600). Only for smoke runs and tests
// (ANTARES_DESKTOP_KEYCHAIN=file:<path>), where the real keychain must not
// be touched but tokens must survive a relaunch.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) load() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func (f *File) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.Path, b, 0o600)
}

func (f *File) Get(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[id]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *File) Set(id, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[id] = token
	return f.save(m)
}

func (f *File) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, id)
	return f.save(m)
}

// FromEnv picks the store for ANTARES_DESKTOP_KEYCHAIN: "" (the OS
// keychain), "memory", or "file:<path>".
func FromEnv(v string) Store {
	switch {
	case v == "memory":
		return NewMemory()
	case len(v) > 5 && v[:5] == "file:":
		return &File{Path: v[5:]}
	default:
		return Keychain{}
	}
}
