// Package browser drives a real Chromium over the DevTools protocol. It speaks
// CDP directly rather than shelling out to a driver, so the only dependency is
// a Chrome binary that is already on the machine.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/wsutil"
)

// Message is one CDP frame, either a reply to a command or an event.
type message struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// conn is a CDP connection multiplexed over one WebSocket.
type conn struct {
	ws *wsutil.Conn

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message

	// events buffers the event kinds callers ask about (console output, for
	// instance), rather than every frame Chrome emits.
	eventsMu sync.Mutex
	events   map[string][]json.RawMessage

	// onEvent, when set, is invoked for every event frame (each in its own
	// goroutine so a handler may call send() without deadlocking the read loop).
	// Used for CDP flows that must reply immediately, e.g. proxy auth.
	onEvent func(method string, params json.RawMessage, sessionID string)

	closed chan struct{}
	once   sync.Once
}

func dial(url string) (*conn, error) {
	ws, err := wsutil.Dial(url, &wsutil.DialOptions{
		Timeout: 10 * time.Second,
		// Chrome sends large screenshot payloads in one frame.
		MaxMessage: 128 << 20,
	})
	if err != nil {
		return nil, err
	}
	c := &conn{
		ws:      ws,
		pending: map[int]chan message{},
		events:  map[string][]json.RawMessage{},
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *conn) readLoop() {
	for {
		_, data, err := c.ws.Read()
		if err != nil {
			c.shutdown()
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ok {
				ch <- m
			}
			continue
		}
		if m.Method != "" {
			if c.onEvent != nil {
				go c.onEvent(m.Method, m.Params, m.SessionID)
			}
			c.eventsMu.Lock()
			// Keep the tail only; a chatty page would otherwise grow forever.
			list := c.events[m.Method]
			list = append(list, m.Params)
			if len(list) > 200 {
				list = list[len(list)-200:]
			}
			c.events[m.Method] = list
			c.eventsMu.Unlock()
		}
	}
}

func (c *conn) shutdown() {
	c.once.Do(func() {
		close(c.closed)
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		_ = c.ws.Close(1000, "")
	})
}

// send issues one command and waits for its reply. sessionID targets a page;
// empty addresses the browser itself.
func (c *conn) send(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, errors.New("the browser connection is closed")
	default:
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := c.ws.WriteText(raw); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errors.New("the browser connection closed while waiting for a reply")
	case m, ok := <-ch:
		if !ok {
			return nil, errors.New("the browser connection closed while waiting for a reply")
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	}
}

// takeEvents drains the buffered events for one method.
func (c *conn) takeEvents(method string) []json.RawMessage {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	list := c.events[method]
	delete(c.events, method)
	return list
}

// ---- browser process ---------------------------------------------------------

// Options configure how a browser is started.
type Options struct {
	// Executable overrides Chrome discovery.
	Executable string
	// UserDataDir persists cookies and logins between runs.
	UserDataDir string
	// Headless runs without a visible window; false needs a display.
	Headless bool
	// Width and Height set the viewport.
	Width, Height int
	// RemoteURL attaches to an already-running Chrome instead of starting one,
	// e.g. "http://127.0.0.1:9222".
	RemoteURL string
	// Stealth launches a source-patched anti-detection Chromium (downloaded and
	// verified on first use) instead of the system Chrome, so pages guarded by
	// bot-detection challenges (Cloudflare Turnstile and the like) load. It has
	// no effect when RemoteURL is set — that attaches to a browser already
	// running.
	Stealth bool
	// Proxy routes the stealth browser through a proxy, e.g.
	// "http://host:3128" or "socks5://host:1080".
	Proxy string
	// Timezone and Locale spoof the stealth browser's fingerprint, e.g.
	// "America/New_York" and "en-US".
	Timezone string
	Locale   string
}

// candidates lists the binaries to try, most standard first.
func candidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "windows":
		return []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"brave-browser", "microsoft-edge",
			// Puppeteer and the Chrome-for-Testing downloads land here.
			os.ExpandEnv("$HOME/chrome-testing/chrome-linux64/chrome"),
			os.ExpandEnv("$HOME/.cache/ms-playwright/chromium/chrome-linux/chrome"),
		}
	}
}

// FindExecutable returns the first usable Chrome on this machine.
func FindExecutable(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("no browser at %s", override)
	}
	for _, c := range candidates() {
		if strings.ContainsRune(c, filepath.Separator) {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome or Chromium was found — install one, or set tools.browser.executable to its path")
}

// versionInfo is the /json/version payload Chrome serves on its debug port.
type versionInfo struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	Browser              string `json:"Browser"`
}

func fetchVersion(ctx context.Context, base string) (versionInfo, error) {
	var out versionInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/json/version", nil)
	if err != nil {
		return out, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	return out, json.NewDecoder(resp.Body).Decode(&out)
}
