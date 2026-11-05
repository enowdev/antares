package migrate

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Keychain reads a secret another agent keeps in the OS keychain (macOS
// Keychain, Windows Credential Manager, Secret Service on Linux). Reading may
// show the OS's own access prompt; that prompt is the user's consent.
type Keychain interface {
	Get(service, account string) (string, error)
}

// ErrNoSecret means the keychain has no such entry.
var ErrNoSecret = errors.New("no such keychain entry")

// OSKeychain is the real keychain. Sources take a Keychain so tests can pass
// a fake; DefaultKeychain is what the API and CLI use.
type OSKeychain struct{}

func (OSKeychain) Get(service, account string) (string, error) {
	v, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoSecret
	}
	return v, err
}

// DefaultKeychain is the keychain sources read when planning. Tests replace it.
var DefaultKeychain Keychain = OSKeychain{}
