// Command Antares is the desktop shell: a window onto an Antares dashboard,
// local (the daemon on this machine) or remote. See docs/desktop.md and
// docs/plans/2026-10-02-desktop-contract.md (section 3).
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/enowdev/antares/desktop/internal/conns"
	"github.com/enowdev/antares/desktop/internal/core"
	"github.com/enowdev/antares/desktop/internal/daemon"
	"github.com/enowdev/antares/desktop/internal/secrets"
)

// Version is the desktop shell's version (set with -ldflags -X main.Version=…).
var Version = "0.1.0"

//go:embed all:frontend
var frontendFS embed.FS

//go:embed inject.js
var injectJS string

//go:embed assets/tray-template.png
var trayTemplate []byte

//go:embed assets/appicon.png
var appIcon []byte

const bundleID = "dev.enowdev.antares"

func main() {
	level := slog.LevelInfo
	if os.Getenv("ANTARES_DESKTOP_DEBUG") != "" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	path, err := conns.DefaultPath()
	if err != nil {
		log.Fatal(err)
	}
	store, err := conns.Open(path)
	if err != nil {
		log.Fatalf("connections: %v", err)
	}
	// Smoke runs set ANTARES_DESKTOP_KEYCHAIN (memory | file:<path>) so the
	// real keychain is never touched.
	keys := secrets.FromEnv(os.Getenv("ANTARES_DESKTOP_KEYCHAIN"))

	assets, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		log.Fatal(err)
	}

	sh := &Shell{}
	app := application.New(application.Options{
		Name:        "Antares",
		Description: "Antares desktop",
		Icon:        appIcon,
		Logger:      logger,
		LogLevel:    level,
		Services:    []application.Service{application.NewService(sh)},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac: application.MacOptions{
			// Closing the window keeps the app (and its tray) running.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "antares-desktop"},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: bundleID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				sh.showWindow()
			},
		},
		// Every quit path (menu, Dock, logout, SIGTERM) asks this first: from
		// here on, closing the window must close it, not hide it, or the
		// shutdown waits forever on a window that refuses to close.
		ShouldQuit: func() bool {
			sh.quitting.Store(true)
			return true
		},
		RawMessageHandler: func(w application.Window, msg string, origin *application.OriginInfo) {
			sh.onPageMessage(msg, origin)
		},
	})
	sh.app = app

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Antares",
		Width:            1280,
		Height:           820,
		MinWidth:         720,
		MinHeight:        520,
		URL:              "/",
		BackgroundColour: application.NewRGB(8, 9, 10),
		// Runs after every navigation, bundled page or dashboard: external
		// links to the system browser, and the path the window is on.
		JS:          injectJS,
		KeyBindings: nonMacKeyBindings(sh),
	})
	sh.win = win

	// Closing hides; Quit (menu, tray, Cmd+Q) exits.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if sh.quitting.Load() {
			return
		}
		win.Hide()
		e.Cancel()
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		sh.showWindow()
	})

	sh.mgr = core.New(core.Deps{
		Store:       store,
		Secrets:     keys,
		Resolver:    daemon.Resolver{},
		Probe:       daemon.Probe{},
		Nav:         navigator{sh},
		MachineName: machineName(),
		Platform:    "desktop-" + platformName(),
		AppVersion:  Version,
		Log:         logger,
	})

	if runtime.GOOS == "darwin" {
		app.Menu.Set(sh.appMenu())
	}
	sh.setupTray()

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go sh.mgr.AutoOpen(context.Background())
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func platformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func machineName() string {
	if n := strings.TrimSpace(computerName()); n != "" {
		return truncate(n, 64)
	}
	h, _ := os.Hostname()
	h = strings.TrimSuffix(strings.TrimSuffix(h, ".local"), ".lan")
	if h == "" {
		return "Desktop"
	}
	return truncate(h, 64)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
