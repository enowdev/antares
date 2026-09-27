package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// GenerateImage produces a single PNG at output. When references is empty it
// calls POST /images/generations. With references it follows ep.ReferenceMode:
// edits (the default) calls POST /images/edits with images:[{image_url}], and
// generations calls POST /images/generations with image:[dataURL...] for
// gateways that have no edits route. All non-URL fetches are bounded and the
// output is written atomically. Errors are redacted of the API key.
//
// This is a paid POST: it is issued once, exactly. No automatic retry, and no
// hidden fallback to a different endpoint on 4xx.
func GenerateImage(ctx context.Context, ep Endpoint, prompt, size string, references []string, output string) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("prompt is required")
	}
	if strings.TrimSpace(output) == "" {
		return errors.New("output path is required")
	}

	size = mustSize(size, "1024x1024")

	// Build the reference payload once; a bad reference short-circuits before
	// any network call so we never spend money on garbage input.
	var refs []string
	for _, p := range references {
		if strings.TrimSpace(p) == "" {
			continue
		}
		u, err := dataURL(p)
		if err != nil {
			return fmt.Errorf("reference %q: %w", p, err)
		}
		refs = append(refs, u)
	}

	endpoint, payload := imageRequest(ep, prompt, size, refs)

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	ep.apply(req.Header)

	resp, err := jsonClient().Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the image endpoint: %s", ep.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, readErr := readAllBounded(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("the image endpoint returned %s: %s", resp.Status, ep.redact(truncate(string(raw), 400)))
	}
	if readErr != nil {
		return fmt.Errorf("could not read the image response: %s", ep.redact(readErr.Error()))
	}

	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Data) == 0 {
		return unusableImageResponse(ep, endpoint, resp.Header.Get("Content-Type"), raw)
	}
	first := out.Data[0]
	switch {
	case first.B64 != "":
		img, err := base64.StdEncoding.DecodeString(first.B64)
		if err != nil {
			return fmt.Errorf("could not decode the image: %v", err)
		}
		// The provider claims PNG, but we validate + re-encode so a garbage
		// payload can never land on disk as a passable image the dashboard
		// would serve as active content.
		return writeReencodedPNG(img, output)
	case first.URL != "":
		// Provider-hosted URL: fetch through the safe downloader (public
		// destination only, no bearer forwarded), then validate + re-encode.
		resp, err := safeGetURL(ctx, first.URL)
		if err != nil {
			return fmt.Errorf("could not download the image: %s", ep.redact(err.Error()))
		}
		return downloadAndReencodeAsPNG(ctx, resp, output)
	}
	return errors.New("the image endpoint returned neither data nor a url")
}

// imageRequest picks the route and body for one image call. Without
// references it is a plain generation; with them, ep.ReferenceMode decides.
func imageRequest(ep Endpoint, prompt, size string, refs []string) (string, map[string]any) {
	payload := map[string]any{
		"model":  ep.Model,
		"prompt": prompt,
		"size":   size,
		"n":      1,
	}
	if len(refs) == 0 {
		return ep.url("/images/generations"), payload
	}
	if ep.ReferenceMode == ReferenceModeGenerations {
		payload["image"] = refs
		return ep.url("/images/generations"), payload
	}
	images := make([]map[string]any, 0, len(refs))
	for _, u := range refs {
		images = append(images, map[string]any{"image_url": u})
	}
	payload["images"] = images
	payload["output_format"] = "png"
	return ep.url("/images/edits"), payload
}

// unusableImageResponse explains a 2xx reply that carried no image. It names
// what came back, because "nothing usable" alone left no way to tell a
// provider without the route (which often answers with its website's HTML
// page) from an empty result.
func unusableImageResponse(ep Endpoint, endpoint, contentType string, raw []byte) error {
	body := strings.TrimSpace(string(raw))
	if strings.Contains(strings.ToLower(contentType), "text/html") || strings.HasPrefix(strings.ToLower(body), "<!doctype html") || strings.HasPrefix(strings.ToLower(body), "<html") {
		hint := "check image_gen.base_url"
		if strings.HasSuffix(endpoint, "/images/edits") {
			hint = "the provider likely has no /images/edits route; set image_gen.reference_mode to generations if it accepts reference images on /images/generations"
		}
		return fmt.Errorf("the image endpoint %s returned an HTML page instead of JSON — %s", ep.redact(endpoint), hint)
	}
	if body == "" {
		return fmt.Errorf("the image endpoint %s returned an empty response", ep.redact(endpoint))
	}
	return fmt.Errorf("the image endpoint %s returned no image data: %s", ep.redact(endpoint), ep.redact(truncate(body, 300)))
}

// truncate caps s at n runes and marks the elision. Used only for error text.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	// n is a character budget; over-run only reduces the tail we show.
	return s[:n] + "…"
}
