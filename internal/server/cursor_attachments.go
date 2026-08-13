package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/cursor"
)

const (
	maxCursorImages     = 5
	maxCursorImageBytes = 15 << 20
	cursorChatBodyLimit = 105 << 20
)

// decodeCursorImages validates Cursor's stricter attachment contract and
// returns the original base64 payloads. The single decoded buffer used for
// size and signature validation becomes unreachable before the function
// returns; PromptImage retains no duplicate decoded bytes.
func decodeCursorImages(dataURLs []string) ([]cursor.PromptImage, error) {
	if len(dataURLs) > maxCursorImages {
		return nil, fmt.Errorf("cursor accepts at most %d images", maxCursorImages)
	}
	images := make([]cursor.PromptImage, 0, len(dataURLs))
	for i, dataURL := range dataURLs {
		dataURL = strings.TrimSpace(dataURL)
		if !strings.HasPrefix(dataURL, "data:") {
			return nil, fmt.Errorf("cursor image %d must be a base64 data URL", i+1)
		}
		metadata, payload, ok := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
		if !ok || payload == "" {
			return nil, fmt.Errorf("cursor image %d must be a base64 data URL", i+1)
		}
		mimeType, encoding, ok := strings.Cut(metadata, ";")
		if !ok || encoding != "base64" || !cursorImageMIMETypeSupported(mimeType) {
			return nil, fmt.Errorf("cursor image %d has an unsupported MIME type or encoding", i+1)
		}
		if strings.ContainsAny(payload, " \t\r\n") {
			return nil, fmt.Errorf("cursor image %d contains invalid base64", i+1)
		}

		decoder := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(payload))
		decoded, err := io.ReadAll(io.LimitReader(decoder, maxCursorImageBytes+1))
		if err != nil {
			return nil, fmt.Errorf("cursor image %d contains invalid base64: %w", i+1, err)
		}
		if len(decoded) > maxCursorImageBytes {
			return nil, fmt.Errorf("cursor image %d exceeds the 15 MiB decoded limit", i+1)
		}
		if detected := http.DetectContentType(decoded); detected != mimeType {
			return nil, fmt.Errorf(
				"cursor image %d MIME type does not match its decoded signature", i+1,
			)
		}
		images = append(images, cursor.PromptImage{
			Data:     payload,
			MimeType: mimeType,
		})
	}
	return images, nil
}

func cursorImageMIMETypeSupported(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// decodeCursorChatBody applies Cursor's route-specific request allowance. It
// remains bounded while leaving enough room for five 15 MiB decoded images
// after base64 expansion.
func decodeCursorChatBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeCursorChatBodyWithLimit(w, r, dst, cursorChatBodyLimit)
}

func decodeCursorChatBodyWithLimit(
	w http.ResponseWriter,
	r *http.Request,
	dst any,
	limit int64,
) error {
	if r == nil || r.Body == nil {
		return errors.New("invalid JSON body: body is required")
	}
	if r.ContentLength > limit {
		return fmt.Errorf("invalid JSON body: %w", &http.MaxBytesError{Limit: limit})
	}

	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid JSON body: multiple JSON values")
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}
