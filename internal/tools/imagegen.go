package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// imageGenerateTool turns a prompt into an image via an OpenAI-compatible
// images endpoint, and saves the result to disk.
type imageGenerateTool struct{}

func (imageGenerateTool) Name() string { return "image_generate" }

func (imageGenerateTool) Description() string {
	return "Generate an image from a text prompt and save it to a file. Describe what you want in detail — " +
		"the subject, the style, the composition. Returns the path to the saved image."
}

func (imageGenerateTool) Schema() map[string]any {
	return schema(map[string]any{
		"prompt": prop("string", "A detailed description of the image to generate."),
		"size":   propEnum("The image size.", "1024x1024", "1792x1024", "1024x1792"),
	}, "prompt")
}

func (imageGenerateTool) RequiresApproval() bool { return true }

func (imageGenerateTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Prompt string `json:"prompt"`
		Size   string `json:"size"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return Errorf("prompt is required")
	}
	if in.Deps == nil || in.Deps.Config == nil {
		return Errorf("image generation is not configured")
	}
	cfg := in.Deps.Config.ImageGen
	if !cfg.Enabled {
		return Errorf("image generation is switched off (image_gen.enabled = false)")
	}

	// Resolve the endpoint and key: an explicit image-gen config, or borrow a
	// named provider's, or fall back to OpenAI's.
	base, key, model := cfg.BaseURL, cfg.APIKey, cfg.Model
	if base == "" || key == "" {
		id := cfg.Provider
		if id == "" {
			id = "openai"
		}
		if p, ok := in.Deps.Config.Providers[id]; ok {
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
		return Errorf("no API key for image generation — set image_gen.api_key or configure the provider")
	}
	if model == "" {
		model = "gpt-image-1"
	}
	size := args.Size
	if size == "" {
		size = firstNonBlank(cfg.Size, "1024x1024")
	}

	in.Emit(Progress{Tool: "image_generate", Message: "generating…"})

	body, _ := json.Marshal(map[string]any{
		"model": model, "prompt": args.Prompt, "size": size, "n": 1,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return Errorf("%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Errorf("could not reach the image endpoint: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return Errorf("the image endpoint returned %s: %s", resp.Status, truncateTool(string(raw), 400))
	}

	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Data) == 0 {
		return Errorf("the image endpoint returned nothing usable")
	}

	var img []byte
	switch {
	case out.Data[0].B64 != "":
		img, err = base64.StdEncoding.DecodeString(out.Data[0].B64)
		if err != nil {
			return Errorf("could not decode the image: %v", err)
		}
	case out.Data[0].URL != "":
		img, err = fetchBytes(ctx, out.Data[0].URL)
		if err != nil {
			return Errorf("could not download the image: %v", err)
		}
	default:
		return Errorf("the image endpoint returned neither data nor a url")
	}

	dir := filepath.Join(config.Home(), "images")
	if in.Workspace != "" {
		if info, err := os.Stat(in.Workspace); err == nil && info.IsDir() {
			dir = filepath.Join(in.Workspace, ".antares", "images")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Errorf("%v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("image-%d.png", time.Now().UnixMilli()))
	if err := os.WriteFile(path, img, 0o644); err != nil {
		return Errorf("%v", err)
	}
	return Result{
		Content: fmt.Sprintf("Generated an image and saved it to %s (%d KB).", path, len(img)/1024),
		Meta:    map[string]any{"path": path, "bytes": len(img)},
	}
}

// fetchBytes downloads a URL's body, bounded.
func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}
