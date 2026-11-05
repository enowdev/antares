// Package media is the shared media backend for image and video generation.
//
// It owns the two provider surfaces the Content Creator relies on:
//
//   - OpenAI-compatible /images/generations and /images/edits for stills, with
//     optional reference images passed as JSON `images:[{image_url:dataURL}]`.
//   - OpenAI-compatible async /videos → GET /videos/{id} → GET /videos/{id}/content
//     for clips, with optional `input_reference:{image_url:dataURL}` seed.
//
// Everything is HTTP JSON. There is no SDK dependency, no shell interpolation,
// no bearer forwarded to arbitrary download URLs, and no automatic retry of a
// paid POST — a failed create surfaces as an error the caller decides about.
// When the corresponding config block is disabled, endpoint resolution refuses
// and no request is issued.
package media

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// Endpoint is a resolved provider target: base URL, credential, model, plus
// any extra headers the provider needs. It is what every media call takes.
type Endpoint struct {
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
	// ReferenceMode is ReferenceModeEdits or ReferenceModeGenerations; empty
	// means edits. Only image requests with references look at it.
	ReferenceMode string
}

// How GenerateImage sends reference images; see config.ImageGen.ReferenceMode.
const (
	ReferenceModeEdits       = "edits"
	ReferenceModeGenerations = "generations"
)

// ImageEndpoint resolves the image generation endpoint from cfg.ImageGen,
// falling back through cfg.ImageGen.Provider → cfg.Providers[id] → openai
// defaults. It refuses when the block is disabled or no key is available.
func ImageEndpoint(cfg *config.Config) (Endpoint, error) {
	if cfg == nil {
		return Endpoint{}, errors.New("image generation is not configured")
	}
	ic := cfg.ImageGen
	if !ic.Enabled {
		return Endpoint{}, errors.New("image generation is switched off (image_gen.enabled = false)")
	}
	base, key := ic.BaseURL, ic.APIKey
	if base == "" || key == "" {
		id := ic.Provider
		if id == "" {
			id = "openai"
		}
		if p, ok := cfg.Providers[id]; ok {
			if base == "" {
				base = p.BaseURL
			}
			if key == "" {
				key = p.APIKey
			}
		}
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if key == "" {
		return Endpoint{}, errors.New("no API key for image generation — set image_gen.api_key or configure the provider")
	}
	model := ic.Model
	if model == "" {
		model = "gpt-image-1"
	}
	mode := strings.TrimSpace(ic.ReferenceMode)
	switch mode {
	case "", ReferenceModeEdits, ReferenceModeGenerations:
	default:
		return Endpoint{}, fmt.Errorf("image_gen.reference_mode must be %q or %q, not %q", ReferenceModeEdits, ReferenceModeGenerations, mode)
	}
	ep := Endpoint{BaseURL: strings.TrimRight(base, "/"), APIKey: key, Model: model, ReferenceMode: mode}
	if ic.Provider != "" || ic.BaseURL == "" {
		id := ic.Provider
		if id == "" {
			id = "openai"
		}
		if p, ok := cfg.Providers[id]; ok {
			ep.Headers = maps.Clone(p.Headers)
		}
	}
	return ep, nil
}

// VideoEndpoint resolves the video generation endpoint the same way, from
// cfg.VideoGen. Refuses when the block is disabled or no key is available.
func VideoEndpoint(cfg *config.Config) (Endpoint, error) {
	if cfg == nil {
		return Endpoint{}, errors.New("video generation is not configured")
	}
	vc := cfg.VideoGen
	if !vc.Enabled {
		return Endpoint{}, errors.New("video generation is switched off (video_gen.enabled = false)")
	}
	base, key := vc.BaseURL, vc.APIKey
	if base == "" || key == "" {
		id := vc.Provider
		if id == "" {
			id = "openai"
		}
		if p, ok := cfg.Providers[id]; ok {
			if base == "" {
				base = p.BaseURL
			}
			if key == "" {
				key = p.APIKey
			}
		}
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if key == "" {
		return Endpoint{}, errors.New("no API key for video generation — set video_gen.api_key or configure the provider")
	}
	model := vc.Model
	if model == "" {
		model = "sora-2"
	}
	ep := Endpoint{BaseURL: strings.TrimRight(base, "/"), APIKey: key, Model: model}
	// Inherit any custom headers the named provider defines (e.g.
	// OpenAI-Organization, Anthropic-Version, HTTP-Referer for OpenRouter)
	// so a video provider that requires them Just Works, the same way the
	// LLM provider path already honours them.
	if vc.Provider != "" || vc.BaseURL == "" {
		id := vc.Provider
		if id == "" {
			id = "openai"
		}
		if p, ok := cfg.Providers[id]; ok {
			ep.Headers = maps.Clone(p.Headers)
		}
	}
	return ep, nil
}

// apply sets Authorization + any Endpoint.Headers on the outgoing request.
func (e Endpoint) apply(h map[string][]string) {
	h["Authorization"] = []string{"Bearer " + e.APIKey}
	for k, v := range e.Headers {
		h[k] = []string{v}
	}
}

// url joins e.BaseURL + path, tolerating a trailing slash on either side.
func (e Endpoint) url(path string) string {
	return strings.TrimRight(e.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

// redact scrubs the API key and any Authorization header from a message so
// error strings never leak the credential.
func (e Endpoint) redact(s string) string {
	if s == "" {
		return s
	}
	if e.APIKey != "" {
		s = strings.ReplaceAll(s, e.APIKey, "[redacted]")
	}
	for _, value := range e.Headers {
		if value != "" {
			s = strings.ReplaceAll(s, value, "[redacted]")
		}
	}
	return s
}

// mustSize returns a sane WxH string, falling back to fallback when input is
// blank. Callers use it so the provider never sees an empty size.
func mustSize(size, fallback string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return fallback
	}
	return size
}
