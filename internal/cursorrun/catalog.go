package cursorrun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/cursor"
)

type catalogCacheKey struct {
	baseURL    string
	credential [sha256.Size]byte
}

type catalogCacheEntry struct {
	catalog   *cursor.ModelCatalog
	expiresAt time.Time
}

type catalogFetchKey struct {
	cacheKey   catalogCacheKey
	generation uint64
}

type catalogFetch struct {
	done    chan struct{}
	catalog *cursor.ModelCatalog
	err     error
}

func (s *service) Catalog(ctx context.Context, force bool) (*cursor.ModelCatalog, error) {
	client, options, secret, err := s.clientWithOptions()
	if err != nil {
		return nil, err
	}
	cacheKey := catalogCacheKey{
		baseURL:    normalizeBaseURL(options.BaseURL),
		credential: sha256.Sum256([]byte(secret)),
	}
	now := s.now()

	s.catalogMu.Lock()
	generation := s.catalogGeneration
	if !force {
		if cached, ok := s.catalogs[cacheKey]; ok && now.Before(cached.expiresAt) {
			catalog := cloneCatalog(cached.catalog)
			s.catalogMu.Unlock()
			return catalog, nil
		}
	}

	fetchKey := catalogFetchKey{cacheKey: cacheKey, generation: generation}
	if pending, ok := s.catalogFetches[fetchKey]; ok {
		done := pending.done
		s.catalogMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return cloneCatalog(pending.catalog), pending.err
		}
	}

	pending := &catalogFetch{done: make(chan struct{})}
	s.catalogFetches[fetchKey] = pending
	s.catalogMu.Unlock()

	catalog, fetchErr := client.Models(ctx)
	if fetchErr != nil {
		fetchErr = sanitizeError(fetchErr, secret)
	} else {
		catalog = sanitizeCatalog(catalog, secret)
	}

	s.catalogMu.Lock()
	pending.catalog = catalog
	pending.err = fetchErr
	if fetchErr == nil && s.catalogGeneration == generation {
		s.catalogs[cacheKey] = catalogCacheEntry{
			catalog:   catalog,
			expiresAt: s.now().Add(s.catalogTTL),
		}
	}
	delete(s.catalogFetches, fetchKey)
	close(pending.done)
	s.catalogMu.Unlock()

	return cloneCatalog(catalog), fetchErr
}

func (s *service) InvalidateCatalog() {
	s.catalogMu.Lock()
	s.catalogGeneration++
	s.catalogs = make(map[catalogCacheKey]catalogCacheEntry)
	s.catalogMu.Unlock()
}

func (s *service) ValidateModel(
	ctx context.Context,
	selection *cursor.ModelSelection,
	policy SelectionPolicy,
) (*cursor.ModelSelection, error) {
	if selection == nil {
		return nil, nil
	}
	if policy != PreserveUpstreamDefault && policy != RequireExactVariant {
		return nil, fmt.Errorf("cursor: unknown model selection policy")
	}
	modelID := strings.TrimSpace(selection.ID)
	if modelID == "" {
		return nil, fmt.Errorf("cursor: model id is required")
	}
	if _, err := canonicalParams(selection.Params); err != nil {
		return nil, err
	}
	requested := &cursor.ModelSelection{
		ID:     modelID,
		Params: append([]cursor.ModelParameterSelection(nil), selection.Params...),
	}

	catalog, err := s.Catalog(ctx, false)
	if err != nil {
		return nil, err
	}
	validated, validationErr := matchSelection(catalog, requested, policy)
	if validationErr == nil {
		return validated, nil
	}

	refreshed, err := s.Catalog(ctx, true)
	if err != nil {
		return nil, err
	}
	validated, validationErr = matchSelection(refreshed, requested, policy)
	if validationErr == nil {
		return validated, nil
	}
	return nil, fmt.Errorf(
		"cursor model selection is no longer available; refresh and reselect: %w",
		validationErr,
	)
}

func matchSelection(
	catalog *cursor.ModelCatalog,
	selection *cursor.ModelSelection,
	policy SelectionPolicy,
) (*cursor.ModelSelection, error) {
	if catalog == nil {
		return nil, fmt.Errorf("cursor: model catalogue is empty")
	}
	var model *cursor.Model
	for i := range catalog.Items {
		if catalog.Items[i].ID == selection.ID {
			model = &catalog.Items[i]
			break
		}
	}
	if model == nil {
		return nil, fmt.Errorf("cursor: model was not found")
	}

	if len(selection.Params) == 0 {
		if len(model.Variants) == 0 || policy == PreserveUpstreamDefault {
			return &cursor.ModelSelection{ID: model.ID}, nil
		}
		return nil, fmt.Errorf("cursor: model requires an exact variant")
	}
	if len(model.Variants) == 0 {
		return nil, fmt.Errorf("cursor: model does not accept parameters")
	}

	requested, err := canonicalParams(selection.Params)
	if err != nil {
		return nil, err
	}
	for _, variant := range model.Variants {
		candidate, err := canonicalParams(variant.Params)
		if err != nil {
			return nil, fmt.Errorf("cursor: model has an invalid variant: %w", err)
		}
		if equalParams(requested, candidate) {
			return &cursor.ModelSelection{
				ID:     model.ID,
				Params: append([]cursor.ModelParameterSelection(nil), variant.Params...),
			}, nil
		}
	}
	return nil, fmt.Errorf("cursor: parameters do not match a model variant")
}

func canonicalParams(
	params []cursor.ModelParameterSelection,
) ([]cursor.ModelParameterSelection, error) {
	canonical := append([]cursor.ModelParameterSelection(nil), params...)
	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].ID < canonical[j].ID
	})
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1].ID == canonical[i].ID {
			return nil, fmt.Errorf("cursor: duplicate model parameter id")
		}
	}
	return canonical, nil
}

func equalParams(left, right []cursor.ModelParameterSelection) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeBaseURL(raw string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	if baseURL == "" {
		return "https://api.cursor.com"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return parsed.String()
}

func sanitizeCatalog(
	catalog *cursor.ModelCatalog,
	secret string,
) *cursor.ModelCatalog {
	if catalog == nil {
		return nil
	}
	safe := &cursor.ModelCatalog{Items: make([]cursor.Model, len(catalog.Items))}
	for i, model := range catalog.Items {
		safe.Items[i] = cursor.Model{
			ID:          redact(model.ID, secret),
			DisplayName: redact(model.DisplayName, secret),
			Description: redact(model.Description, secret),
			Aliases:     make([]string, len(model.Aliases)),
			Parameters:  make([]cursor.ModelParameter, len(model.Parameters)),
			Variants:    make([]cursor.ModelVariant, len(model.Variants)),
		}
		for j, alias := range model.Aliases {
			safe.Items[i].Aliases[j] = redact(alias, secret)
		}
		for j, parameter := range model.Parameters {
			safe.Items[i].Parameters[j] = cursor.ModelParameter{
				ID:          redact(parameter.ID, secret),
				DisplayName: redact(parameter.DisplayName, secret),
				Values:      make([]cursor.ModelParameterValue, len(parameter.Values)),
			}
			for k, value := range parameter.Values {
				safe.Items[i].Parameters[j].Values[k] = cursor.ModelParameterValue{
					Value:       redact(value.Value, secret),
					DisplayName: redact(value.DisplayName, secret),
				}
			}
		}
		for j, variant := range model.Variants {
			safe.Items[i].Variants[j] = cursor.ModelVariant{
				Params:      make([]cursor.ModelParameterSelection, len(variant.Params)),
				DisplayName: redact(variant.DisplayName, secret),
				Description: redact(variant.Description, secret),
				IsDefault:   variant.IsDefault,
			}
			for k, parameter := range variant.Params {
				safe.Items[i].Variants[j].Params[k] = cursor.ModelParameterSelection{
					ID:    redact(parameter.ID, secret),
					Value: redact(parameter.Value, secret),
				}
			}
		}
	}
	return safe
}

func cloneCatalog(catalog *cursor.ModelCatalog) *cursor.ModelCatalog {
	return sanitizeCatalog(catalog, "")
}
