package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

type providerCatalogScope struct {
	providerID            string
	kind                  string
	baseURLFingerprint    [sha256.Size]byte
	credentialFingerprint [sha256.Size]byte
	apiVersion            string
	region                string
}

type providerCatalogEntry struct {
	done        chan struct{}
	ready       bool
	hasSuccess  bool
	expiresAt   time.Time
	models      []llm.ModelInfo
	adapterKind string
	err         error
}

const (
	providerCatalogTTL          = 5 * time.Minute
	providerCatalogFetchTimeout = 45 * time.Second
)

func (a *Agent) cachedProviderCatalog(
	ctx context.Context,
	providerID string,
	provider config.Provider,
	fetch func(context.Context) ([]llm.ModelInfo, string, error),
) ([]llm.ModelInfo, string, error) {
	scope := providerCatalogScopeFor(providerID, provider)
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}

		var startFetch bool
		a.catalogMu.Lock()
		if a.catalogCache == nil {
			a.catalogCache = make(map[providerCatalogScope]*providerCatalogEntry)
		}
		if entry, ok := a.catalogCache[scope]; ok {
			if entry.ready {
				if a.providerCatalogTime().Before(entry.expiresAt) {
					models, adapterKind, err := cloneModelInfo(entry.models), entry.adapterKind, entry.err
					a.catalogMu.Unlock()
					return models, adapterKind, err
				}
				entry.ready = false
				entry.done = make(chan struct{})
				startFetch = true
			}
			done := entry.done
			a.catalogMu.Unlock()
			if startFetch {
				go a.refreshProviderCatalog(entry, fetch)
			}
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}

		entry := &providerCatalogEntry{done: make(chan struct{})}
		a.catalogCache[scope] = entry
		done := entry.done
		a.catalogMu.Unlock()
		go a.refreshProviderCatalog(entry, fetch)
		select {
		case <-done:
			continue
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
}

func (a *Agent) refreshProviderCatalog(
	entry *providerCatalogEntry,
	fetch func(context.Context) ([]llm.ModelInfo, string, error),
) {
	// The shared fetch belongs to the cache entry, not to whichever caller won
	// the miss race. Individual waiters may cancel without aborting or poisoning
	// the provider scope; this independent context bounds orphaned work.
	ctx, cancel := context.WithTimeout(context.Background(), providerCatalogFetchTimeout)
	defer cancel()
	models, adapterKind, err := fetch(ctx)

	a.catalogMu.Lock()
	if err == nil {
		entry.models = cloneModelInfo(models)
		entry.adapterKind = adapterKind
		entry.err = nil
		entry.hasSuccess = true
	} else if entry.hasSuccess {
		models = cloneModelInfo(entry.models)
		adapterKind = entry.adapterKind
		err = nil
		entry.err = nil
	} else if len(models) > 0 {
		entry.models = cloneModelInfo(models)
		entry.adapterKind = adapterKind
		entry.err = nil
		entry.hasSuccess = true
		err = nil
	} else {
		entry.models = nil
		entry.adapterKind = adapterKind
		entry.err = err
	}
	entry.expiresAt = a.providerCatalogTime().Add(providerCatalogTTL)
	entry.ready = true
	close(entry.done)
	a.catalogMu.Unlock()
}

func providerCatalogScopeFor(providerID string, provider config.Provider) providerCatalogScope {
	kind := normalizedProviderKind(provider.Kind)
	baseURL := normalizedProviderBaseURL(kind, provider.BaseURL)
	return providerCatalogScope{
		providerID:            strings.ToLower(strings.TrimSpace(providerID)),
		kind:                  kind,
		baseURLFingerprint:    sha256.Sum256([]byte(baseURL)),
		credentialFingerprint: providerCredentialFingerprint(provider),
		apiVersion:            strings.TrimSpace(provider.APIVersion),
		region:                strings.ToLower(strings.TrimSpace(provider.Region)),
	}
}

func normalizedProviderKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "google":
		return "gemini"
	case "claude":
		return "anthropic"
	case "responses", "openai-responses":
		return "codex"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func normalizedProviderBaseURL(kind, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		baseURL = parsed.String()
	} else {
		baseURL = strings.TrimRight(baseURL, "/")
	}
	if baseURL != "" {
		if kind == "gemini" {
			lower := strings.ToLower(baseURL)
			if (strings.HasSuffix(lower, "/antigravity") || strings.Contains(lower, "/antigravity/")) &&
				!strings.Contains(lower, "/v1beta") {
				return baseURL + "/v1beta"
			}
		}
		return baseURL
	}
	switch kind {
	case "openai", "codex":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	default:
		return ""
	}
}

func providerCredentialFingerprint(provider config.Provider) [sha256.Size]byte {
	h := sha256.New()
	writeFingerprintValue(h, provider.APIKey)

	keys := make([]string, 0, len(provider.Headers))
	for key := range provider.Headers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	for _, key := range keys {
		writeFingerprintValue(h, strings.ToLower(strings.TrimSpace(key)))
		writeFingerprintValue(h, provider.Headers[key])
	}

	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], h.Sum(nil))
	return fingerprint
}

func writeFingerprintValue(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}

func cloneModelInfo(models []llm.ModelInfo) []llm.ModelInfo {
	return append([]llm.ModelInfo(nil), models...)
}

func (a *Agent) providerCatalogTime() time.Time {
	if a.catalogNow != nil {
		return a.catalogNow()
	}
	return time.Now()
}
