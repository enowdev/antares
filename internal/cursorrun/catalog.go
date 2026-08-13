package cursorrun

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/cursor"
)

// Catalogue collection limits cap every nesting level before allocation. They
// comfortably exceed the current upstream catalogue while preventing a valid
// but adversarial response from being retained without bound.
const (
	maxCatalogModels          = 256
	maxCatalogAliases         = 64
	maxCatalogParameters      = 64
	maxCatalogParameterValues = 128
	maxCatalogVariants        = 256
	maxCatalogVariantParams   = 64
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

var errCatalogFetchPanicked = errors.New("cursor: catalogue fetch failed")

func (s *service) Catalog(ctx context.Context, force bool) (*cursor.ModelCatalog, error) {
	catalog, _, err := s.catalog(ctx, force)
	return catalog, err
}

// catalog reports whether the returned value was served from the TTL cache.
// Validation uses that provenance to refresh a potentially stale selection
// once without issuing an immediate duplicate request after a cold live fetch.
func (s *service) catalog(
	ctx context.Context,
	force bool,
) (*cursor.ModelCatalog, bool, error) {
	client, options, secret, err := s.clientWithOptions()
	if err != nil {
		return nil, false, err
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
			return catalog, true, nil
		}
	}

	fetchKey := catalogFetchKey{cacheKey: cacheKey, generation: generation}
	if pending, ok := s.catalogFetches[fetchKey]; ok {
		done := pending.done
		s.catalogMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-done:
			return cloneCatalog(pending.catalog), false, pending.err
		}
	}

	pending := &catalogFetch{done: make(chan struct{})}
	s.catalogFetches[fetchKey] = pending
	s.catalogMu.Unlock()

	catalog, err := s.fetchCatalog(
		ctx, client, secret, cacheKey, fetchKey, generation, pending,
	)
	return catalog, false, err
}

func (s *service) fetchCatalog(
	ctx context.Context,
	client *cursor.Client,
	secret string,
	cacheKey catalogCacheKey,
	fetchKey catalogFetchKey,
	generation uint64,
	pending *catalogFetch,
) (catalog *cursor.ModelCatalog, fetchErr error) {
	var expiresAt time.Time
	defer func() {
		if panicValue := recover(); panicValue != nil {
			s.completeCatalogFetch(
				cacheKey, fetchKey, generation, pending, nil, errCatalogFetchPanicked, time.Time{},
			)
			panic(panicValue)
		}
		s.completeCatalogFetch(
			cacheKey, fetchKey, generation, pending, catalog, fetchErr, expiresAt,
		)
		catalog = cloneCatalog(catalog)
	}()

	catalog, fetchErr = client.Models(ctx)
	if fetchErr != nil {
		fetchErr = sanitizeError(fetchErr, secret)
	} else {
		catalog = sanitizeCatalog(catalog, secret)
		expiresAt = s.now().Add(s.catalogTTL)
	}
	return catalog, fetchErr
}

func (s *service) completeCatalogFetch(
	cacheKey catalogCacheKey,
	fetchKey catalogFetchKey,
	generation uint64,
	pending *catalogFetch,
	catalog *cursor.ModelCatalog,
	fetchErr error,
	expiresAt time.Time,
) {
	s.catalogMu.Lock()
	pending.catalog = catalog
	pending.err = fetchErr
	if fetchErr == nil && s.catalogGeneration == generation {
		s.catalogs[cacheKey] = catalogCacheEntry{
			catalog:   catalog,
			expiresAt: expiresAt,
		}
	}
	delete(s.catalogFetches, fetchKey)
	close(pending.done)
	s.catalogMu.Unlock()
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

	catalog, fromCache, err := s.catalog(ctx, false)
	if err != nil {
		return nil, err
	}
	validated, validationErr := matchSelection(catalog, requested, policy)
	if validationErr == nil {
		return validated, nil
	}
	if !fromCache {
		return nil, unavailableSelectionError(validationErr)
	}

	refreshed, _, err := s.catalog(ctx, true)
	if err != nil {
		return nil, err
	}
	validated, validationErr = matchSelection(refreshed, requested, policy)
	if validationErr == nil {
		return validated, nil
	}
	return nil, unavailableSelectionError(validationErr)
}

func unavailableSelectionError(validationErr error) error {
	return fmt.Errorf(
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
		for i := range catalog.Items {
			for _, alias := range catalog.Items[i].Aliases {
				if alias != selection.ID {
					continue
				}
				if model != nil {
					return nil, fmt.Errorf("cursor: model alias is ambiguous")
				}
				model = &catalog.Items[i]
				break
			}
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
	modelCount := min(len(catalog.Items), maxCatalogModels)
	safe := &cursor.ModelCatalog{Items: make([]cursor.Model, modelCount)}
	for i, model := range catalog.Items[:modelCount] {
		aliasCount := min(len(model.Aliases), maxCatalogAliases)
		parameterCount := min(len(model.Parameters), maxCatalogParameters)
		variantCount := min(len(model.Variants), maxCatalogVariants)
		safe.Items[i] = cursor.Model{
			ID:          sanitizeString(model.ID, secret, maxIdentifierRunes),
			DisplayName: sanitizeString(model.DisplayName, secret, maxMetadataRunes),
			Description: sanitizeString(model.Description, secret, maxMetadataRunes),
			Aliases:     make([]string, aliasCount),
			Parameters:  make([]cursor.ModelParameter, parameterCount),
			Variants:    make([]cursor.ModelVariant, variantCount),
		}
		for j, alias := range model.Aliases[:aliasCount] {
			safe.Items[i].Aliases[j] = sanitizeString(alias, secret, maxMetadataRunes)
		}
		for j, parameter := range model.Parameters[:parameterCount] {
			valueCount := min(len(parameter.Values), maxCatalogParameterValues)
			safe.Items[i].Parameters[j] = cursor.ModelParameter{
				ID:          sanitizeString(parameter.ID, secret, maxIdentifierRunes),
				DisplayName: sanitizeString(parameter.DisplayName, secret, maxMetadataRunes),
				Values:      make([]cursor.ModelParameterValue, valueCount),
			}
			for k, value := range parameter.Values[:valueCount] {
				safe.Items[i].Parameters[j].Values[k] = cursor.ModelParameterValue{
					Value:       sanitizeString(value.Value, secret, maxIdentifierRunes),
					DisplayName: sanitizeString(value.DisplayName, secret, maxMetadataRunes),
				}
			}
		}
		for j, variant := range model.Variants[:variantCount] {
			paramCount := min(len(variant.Params), maxCatalogVariantParams)
			safe.Items[i].Variants[j] = cursor.ModelVariant{
				Params:      make([]cursor.ModelParameterSelection, paramCount),
				DisplayName: sanitizeString(variant.DisplayName, secret, maxMetadataRunes),
				Description: sanitizeString(variant.Description, secret, maxMetadataRunes),
				IsDefault:   variant.IsDefault,
			}
			for k, parameter := range variant.Params[:paramCount] {
				safe.Items[i].Variants[j].Params[k] = cursor.ModelParameterSelection{
					ID:    sanitizeString(parameter.ID, secret, maxIdentifierRunes),
					Value: sanitizeString(parameter.Value, secret, maxIdentifierRunes),
				}
			}
		}
	}
	return safe
}

func cloneCatalog(catalog *cursor.ModelCatalog) *cursor.ModelCatalog {
	return sanitizeCatalog(catalog, "")
}
