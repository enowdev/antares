package main

import (
	"context"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/enowdev/antares/desktop/internal/conns"
	"github.com/enowdev/antares/desktop/internal/core"
)

// Shell is the Wails-facing side: the methods the bundled pages call
// (main.Shell.*), the core.Navigator the manager drives, the tray and the
// app menu.
type Shell struct {
	app *application.App
	win *application.WebviewWindow
	mgr *core.Manager

	quitting atomic.Bool

	trayMu    sync.Mutex
	tray      *application.SystemTray
	trayMenu  *application.Menu
	itStatus  *application.MenuItem
	itStart   *application.MenuItem
	itStop    *application.MenuItem
	lastLabel string
}

// ---- bound methods (called from frontend/*.js) ----

func (s *Shell) State() core.State { return s.mgr.State() }

func (s *Shell) Refresh() core.State { return s.mgr.Refresh(context.Background()) }

func (s *Shell) AddRemote(name, rawURL, password string) (core.ConnView, error) {
	return s.mgr.AddRemote(context.Background(), name, rawURL, password)
}

func (s *Shell) AddLocal() (core.ConnView, error) { return s.mgr.AddLocal(context.Background()) }

func (s *Shell) Repair(id, password string) error {
	return s.mgr.Repair(context.Background(), id, password)
}

func (s *Shell) Remove(id string) error { return s.mgr.Remove(context.Background(), id) }

func (s *Shell) Open(id string) error { return s.mgr.Open(context.Background(), id) }

func (s *Shell) Retry() error { return s.mgr.Retry(context.Background()) }

func (s *Shell) SwitchConnection() { s.mgr.SwitchConnection() }

// ---- core.Navigator ----

// navigator is the window as core.Manager drives it. It is a separate type
// so its methods are not bound for the pages to call.
type navigator struct{ s *Shell }

func (n navigator) ShowConnections() { n.s.win.SetURL("/") }

func (n navigator) ShowReconnect() { n.s.win.SetURL("/reconnect.html") }

func (n navigator) Navigate(u string) { n.s.win.SetURL(u) }

// Changed refreshes the tray and tells the bundled pages. While a dashboard
// is showing there is no bundled page to tell.
func (n navigator) Changed() {
	s := n.s
	st := s.mgr.State()
	s.app.Logger.Debug("state changed", "phase", st.Phase, "local", st.Local)
	s.updateTray(st)
	if st.Phase != core.PhaseOpen {
		s.app.Event.Emit("antares:state", st)
	}
}

// ---- page messages (from inject.js) ----

// onPageMessage handles "antares:open:<url>" (an external link: system
// browser) and "antares:path:<path>" (where the dashboard is). Only the main
// frame of a bundled page or of the open dashboard is listened to.
func (s *Shell) onPageMessage(msg string, origin *application.OriginInfo) {
	if origin == nil || !origin.IsMainFrame {
		return
	}
	s.app.Logger.Debug("page message", "msg", msg, "origin", origin.Origin)
	from := originOf(origin.Origin)
	bundled := strings.HasPrefix(from, "wails://")
	dashboard := from != "" && from == s.mgr.CurrentOrigin()
	if !bundled && !dashboard {
		return
	}
	switch {
	case strings.HasPrefix(msg, "antares:open:"):
		target := strings.TrimPrefix(msg, "antares:open:")
		u, err := url.Parse(target)
		if err != nil {
			return
		}
		switch u.Scheme {
		case "http", "https", "mailto":
			_ = s.app.Browser.OpenURL(u.String())
		}
	case strings.HasPrefix(msg, "antares:path:") && dashboard:
		s.mgr.PagePath(origin.Origin, strings.TrimPrefix(msg, "antares:path:"))
	}
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// ---- window, menu, tray ----

func (s *Shell) showWindow() {
	if s.win == nil {
		return
	}
	// Not Restore(): on macOS it un-zooms by toggling zoom, which maximises
	// a window AppKit considers "zoomed" at its default size.
	if s.win.IsMinimised() {
		s.win.UnMinimise()
	}
	s.win.Show()
	s.win.Focus()
	if runtime.GOOS == "darwin" {
		// Activate the app, not just order the window front. App.Show is
		// not main-thread safe in v3 beta.27 (AppKit traps off-thread).
		application.InvokeSync(s.app.Show)
	}
}

func (s *Shell) switchConnection() {
	s.mgr.SwitchConnection()
	s.showWindow()
}

func (s *Shell) quit() {
	// Never stops the local daemon: that is only "Stop Antares".
	s.quitting.Store(true)
	s.app.Quit()
}

func (s *Shell) appMenu() *application.Menu {
	menu := s.app.NewMenu()
	appSub := menu.AddSubmenu("Antares")
	appSub.AddRole(application.About)
	appSub.AddSeparator()
	appSub.Add("Switch connection…").SetAccelerator("CmdOrCtrl+Shift+O").OnClick(func(*application.Context) {
		s.switchConnection()
	})
	appSub.AddSeparator()
	appSub.AddRole(application.ServicesMenu)
	appSub.AddSeparator()
	appSub.AddRole(application.Hide)
	appSub.AddRole(application.HideOthers)
	appSub.AddRole(application.ShowAll)
	appSub.AddSeparator()
	appSub.Add("Quit Antares").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) { s.quit() })
	// Without an Edit menu, copy/paste shortcuts do nothing in WKWebView.
	menu.AddRole(application.EditMenu)
	view := menu.AddSubmenu("View")
	view.Add("Reload").SetAccelerator("CmdOrCtrl+R").OnClick(func(*application.Context) { s.win.Reload() })
	view.AddSeparator()
	view.AddRole(application.ResetZoom)
	view.AddRole(application.ZoomIn)
	view.AddRole(application.ZoomOut)
	view.AddSeparator()
	view.AddRole(application.ToggleFullscreen)
	menu.AddRole(application.WindowMenu)
	return menu
}

func nonMacKeyBindings(s *Shell) map[string]func(application.Window) {
	if runtime.GOOS == "darwin" {
		return nil // the app menu carries the shortcut
	}
	return map[string]func(application.Window){
		"CmdOrCtrl+Shift+O": func(application.Window) { s.switchConnection() },
	}
}

func (s *Shell) setupTray() {
	tray := s.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplate)
	} else {
		tray.SetIcon(appIcon)
	}
	tray.SetTooltip("Antares")

	m := s.app.NewMenu()
	s.itStatus = m.Add("Not connected").SetEnabled(false)
	m.AddSeparator()
	m.Add("Open Antares").OnClick(func(*application.Context) { s.showWindow() })
	m.Add("Switch connection…").OnClick(func(*application.Context) { s.switchConnection() })
	m.AddSeparator()
	s.itStart = m.Add("Start Antares").OnClick(func(*application.Context) {
		go func() {
			if err := s.mgr.StartLocal(context.Background()); err == nil {
				s.showWindow()
			}
		}()
	})
	s.itStop = m.Add("Stop Antares").OnClick(func(*application.Context) {
		go func() { _ = s.mgr.StopLocal(context.Background()) }()
	})
	m.AddSeparator()
	m.Add("Quit Antares").OnClick(func(*application.Context) { s.quit() })
	s.itStart.SetHidden(true)
	s.itStop.SetHidden(true)
	tray.SetMenu(m)

	s.tray, s.trayMenu = tray, m

	// Keep "This Mac"'s Start/Stop honest while nothing else refreshes it.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			if s.mgr.State().Phase != core.PhaseOpen {
				s.mgr.Refresh(context.Background())
			}
		}
	}()
}

func (s *Shell) updateTray(st core.State) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	if s.trayMenu == nil {
		return
	}
	label := "Not connected"
	switch st.Phase {
	case core.PhaseOpening:
		label = "Connecting to " + st.CurrentName + "…"
	case core.PhaseOpen:
		label = st.CurrentName + " — connected"
	case core.PhaseReconnecting:
		label = st.CurrentName + " — reconnecting…"
	}
	hasLocal := false
	for _, c := range st.Connections {
		if c.Mode == conns.ModeLocal {
			hasLocal = true
		}
	}
	key := label + "|" + boolStr(hasLocal && !st.Local.Running && st.Local.Installed) + boolStr(hasLocal && st.Local.Running)
	if key == s.lastLabel {
		return
	}
	s.lastLabel = key
	s.itStatus.SetLabel(label)
	s.itStart.SetHidden(!(hasLocal && !st.Local.Running && st.Local.Installed))
	s.itStop.SetHidden(!(hasLocal && st.Local.Running))
	s.trayMenu.Update()
	s.tray.SetTooltip("Antares · " + label)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
