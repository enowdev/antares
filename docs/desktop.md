# Desktop app

`desktop/` is a small app that shows the Antares dashboard in its own window.
It is a shell: the dashboard is still the one served by an Antares server, and
the app adds a connection screen, a tray menu and the plumbing to sign the
window in. It is packaged for macOS (.dmg), Windows (an installer) and Linux
(.deb and .tar.gz), on amd64 and arm64; downloads are on
[antares.enowx.ai](https://antares.enowx.ai/#download) and in each GitHub
release.

The interface between the app, the server and the dashboard is
[plans/2026-10-02-desktop-contract.md](plans/2026-10-02-desktop-contract.md)
(section 3 is the app).

## Connections

The connection screen lists saved connections (name, URL or "This Mac", mode,
a status dot) and adds new ones. The last connection opened opens again on
launch; the screen comes back from the tray or **Antares → Switch
connection…** (⌘⇧O).

**Local (This Mac).** At most one. The app pairs itself with
`antares device pair --platform desktop-macos --json`, which writes a device
straight into the local store (`$ANTARES_HOME`, default `~/.antares`). Opening
it starts the daemon with `antares serve` when it is not running and waits up
to 20 s for its health check. The app never stops the daemon on its own:
closing the window or quitting leaves it running, exactly like the CLI and TUI
expect; only **Stop Antares** in the tray stops it.

The `antares` binary used is, in order: the copy bundled in the app
(`Antares.app/Contents/Resources/antares`), `antares` on `PATH`,
`~/.local/bin/antares`.

**Remote.** Name, URL and the server's dashboard password. The app checks
`GET /api/version` (a server without it is reported as too old), then pairs
with `POST /api/devices/pair`. The password is used once and not stored.
URLs must be `https://`, except loopback, private LAN ranges (10/8,
172.16/12, 192.168/16, link-local, IPv6 ULA) and Tailscale (100.64.0.0/10,
`*.ts.net`), where `http://` is accepted. A bare host gets `https://`.

**Opening** either kind asks the server for a one-time handoff code
(`POST /api/auth/handoff` with the device token) and navigates the window to
`/auth/handoff?code=…`, which sets the dashboard session cookie. The token never
appears in a URL. A `401` there means the device was revoked: the connection is
marked **Signed out** and offers **Pair again**.

**Removing** a connection deletes its Keychain entry and, best effort, revokes
the device on the server (over HTTP, or through `antares device revoke` when
the local daemon is down).

## What is stored where

| What | Where |
|---|---|
| Connections (names, URLs, modes, device ids; no secrets) | `~/Library/Application Support/Antares/connections.json` (`os.UserConfigDir()/Antares` elsewhere) |
| Device tokens | Keychain, service `dev.enowdev.antares`, account = connection id |

Paired devices show up in the dashboard under **Settings → Devices** and in
`antares device list`, where they can be revoked.

## While a dashboard is open

- Health is checked every 10 s. When it fails, the window shows a
  **Reconnecting…** page (Retry, Switch connection) and keeps trying every few
  seconds; on recovery it signs in again and returns to the page it was on.
- The same poll checks the device token (`GET /api/devices`). Once the device
  is revoked (dashboard Settings → Devices, `antares device revoke`, or the
  server's 30 s cache running out) the window returns to the connection
  screen with the connection marked **Signed out**. Landing on the dashboard's
  login page triggers the same check right away.
- The signed-out mark is not saved: after a relaunch the connection shows its
  reachability again, and opening it reports the revoked token.
- Links and `window.open` to other origins open in the default browser.
- Closing the window hides it; the app stays in the menu bar. Quit (⌘Q, the
  tray, the Dock) exits the app only.
- A second launch focuses the running app.

## Building

```bash
make desktop        # builds bin/antares (make build), then desktop/bin/Antares.app
make desktop-test   # go vet + go test in desktop/
```

`make desktop` needs macOS with Xcode command line tools (cgo, WebKit). The
app is its own Go module (`desktop/go.mod`, Wails pinned to
`v3.0.0-beta.27`), so `go build ./...` at the repository root never compiles
it and needs no cgo. No `wails3` CLI or Taskfile is needed:
`desktop/scripts/build-macos.sh` runs `go build -tags production`, lays out the
bundle (`Info.plist`, `icons.icns`, the bundled `antares`) and signs it.

### Packages

`.github/workflows/release.yml` builds every package on a tag push, with the
release's own `antares` binary bundled:

| OS | Script | Package | Layout |
|---|---|---|---|
| macOS | `scripts/build-macos.sh` (`ARCH=arm64\|amd64`), `scripts/package-macos.sh` | `Antares-macos-<arm64\|x64>.dmg` | `Antares.app`, server in `Contents/Resources/antares` |
| Windows | `go build -H windowsgui` with `go-winres` for the icon, `build/windows/installer.nsi` (NSIS) | `Antares-windows-<x64\|arm64>-setup.exe` | `%LOCALAPPDATA%\Programs\Antares\AntaresDesktop.exe` and `antares.exe` beside it |
| Linux | `go build -tags production,gtk3` (cgo, GTK 3, WebKitGTK 4.1), `scripts/package-linux.sh` | `Antares-linux-<x64\|arm64>.deb` and `.tar.gz` | `/opt/antares/antares-desktop` and `antares`, a launcher entry and an icon |

On Windows the shell is `AntaresDesktop.exe` because file names there are
case-insensitive, and the server beside it must be `antares.exe`.

The app is **ad-hoc signed and not notarized** on macOS, and not
Authenticode-signed on Windows (there is no certificate yet). A downloaded
copy is stopped by Gatekeeper until it is opened once with right-click › Open
(or Open Anyway in System Settings › Privacy & Security), or the quarantine
attribute is removed (`xattr -dr com.apple.quarantine /Applications/Antares.app`).
On Windows, SmartScreen may ask once: More info › Run anyway.

Icons come from `web/public/antares.png`;
`python3 desktop/scripts/make-icons.py` regenerates the tray template, the app
icon and `icons.icns`.

## Layout

```
desktop/
  main.go, shell.go     Wails wiring: window, app menu, tray, bound methods
  inject.js             runs in every page: external links, current path
  frontend/             connection screen + reconnect page (plain HTML/JS/CSS)
  internal/conns        connections.json and the URL rules
  internal/secrets      Keychain (go-keyring), plus memory/file stores for tests
  internal/api          client for /api/version, pair, handoff, devices, health
  internal/daemon       antares binary lookup, daemon state, serve/stop/pair
  internal/core         the behaviour: add, open, remove, health poll, reconnect
  internal/mockserver   in-memory server implementing the contract, for tests
  scripts/              build-macos.sh, make-icons.py
  build/darwin/         Info.plist, icons.icns
```

The frontend has no build step: `frontend/` is embedded as is and talks to Go
through the Wails runtime at `/wails/runtime.js` (`main.Shell.*`). Opened from
a plain web server with `?demo=list|empty|notice|reconnect` it renders demo
data, which is how its screenshots are made.

## Testing without touching your real Antares

```bash
cd desktop && go test ./...
```

The unit tests run against `internal/mockserver`. To exercise the remote flow
against a real server, point the opt-in test at an isolated one (it pairs and
revokes a device):

```bash
ANTARES_DESKTOP_E2E_URL=http://127.0.0.1:18951 \
ANTARES_DESKTOP_E2E_PASSWORD=… go test -run RealServer -v ./internal/core/
```

To run the app itself against a throwaway home, seed one with
`cmd/smokefixture` and launch the binary with:

| Variable | Effect |
|---|---|
| `ANTARES_HOME` | the Antares state directory the app reads and the `antares` it runs use |
| `ANTARES_DESKTOP_CONFIG_DIR` | directory for `connections.json` instead of Application Support |
| `ANTARES_DESKTOP_KEYCHAIN` | `memory` or `file:<path>` instead of the real Keychain |
| `ANTARES_DESKTOP_DEBUG` | debug logging (Wails and the shell) |

```bash
ANTARES_HOME=/tmp/ah ANTARES_DESKTOP_CONFIG_DIR=/tmp/ah-desk \
ANTARES_DESKTOP_KEYCHAIN=memory \
  desktop/bin/Antares.app/Contents/MacOS/Antares
```

Launch the binary directly (not with `open`) so the environment reaches it.
