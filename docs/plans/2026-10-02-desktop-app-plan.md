# Antares desktop app — plan

Status: planning. Mobile is out of scope for now (see "Later" at the end).

## Goal

A small desktop app (macOS first, then Windows and Linux) that opens the
Antares dashboard in its own window, in one of two modes:

1. **Remote** — connect to an Antares that already runs somewhere else (a VPS,
   a home server, a laptop over Tailscale).
2. **Local** — run Antares on this machine, on the data in `~/.antares`, the
   same data the CLI and TUI use.

The app is a shell. The dashboard stays the one UI: the app never grows a
second copy of it.

## Framework: Wails v3

- Go, like the rest of Antares; the window uses the system webview (WebKit on
  macOS and Linux, WebView2 on Windows), so the shell adds a few MB.
- v3 (currently `v3.0.0-beta.27`) over v2 (`v2.16.0`) because v3 has the
  system tray, single-instance lock and notifications built in, and v2 has no
  tray. Risk: v3 is beta and its API may still move — pin the version and keep
  the shell thin so an upgrade touches little.
- Wails needs cgo and a webview, so it lives in its own binary
  (`cmd/antares-desktop`). The `antares` binary stays pure Go and server-safe.

## Architecture

```
antares-desktop (Wails)
 ├─ connection screen        (small bundled page: list, add, edit, remove)
 ├─ window → dashboard URL   (served by the Antares it is connected to)
 ├─ tray, single instance, auto-start, notifications
 └─ connections.json + OS keychain for tokens

Remote:  window → https://antares.example.com
Local:   window → http://127.0.0.1:<port>  ←  `antares serve` (the existing daemon)
```

### Local mode reuses the existing daemon

`antares serve` already runs as a daemon with a pid/state file and refuses a
second copy ("Antares is already running (pid …) at …"). Local mode builds on
that instead of starting a server inside the app:

- On connect, read the daemon state. If Antares is running, use its URL.
- If not, start it with the bundled or installed `antares` binary
  (`antares serve`) and wait for `/health`.
- Closing the window leaves the daemon running (like closing a browser tab);
  the tray offers "Stop Antares".
- So there is never a second server on the same database, and the CLI, TUI
  and desktop app all see one Antares.

Which `antares` binary: the one bundled in the app, unless an installed one
on PATH or in `~/.local/bin` is newer. The bundled copy keeps the app working
on a machine that never installed the CLI.

### Opening the dashboard signed in

- Remote: the user enters a URL and signs in once (password, through
  `/api/auth/login`, or a token). The app keeps a per-device token, not the
  password.
- Local: the app is on the same machine as `~/.antares/config.yaml` and can
  read `server.auth_token`, or ask the server for a device token.
- Handoff into the dashboard: navigate to the dashboard with a one-time code
  the server exchanges for a session, so no long-lived secret sits in a URL or
  in page history. (Needs a small server endpoint — see below.)

## Server work (do first)

1. **Device tokens** — named tokens, one per device, listed and revocable in
   Settings ("Devices"), with last-seen time. Today there is one shared
   `server.auth_token` and a dashboard password.
2. **One-time login codes** — `POST /api/auth/device-code` (authorised) mints a
   short-lived single-use code; `GET /auth/code?c=…` exchanges it for a
   session cookie and redirects to the dashboard.
3. **Version endpoint** — `GET /api/version` with version and a minimum
   compatible desktop version, so the app can warn about a stale server.
4. **Embedded-webview check** — the dashboard must not assume a browser:
   downloads, `window.open`, external links and file pickers go through the
   shell where the webview cannot do them.

## The app

### Connection screen
- List of saved connections (name, URL or "This Mac", status dot).
- Add: Remote (URL + password or token, test before saving) or Local (one
  per machine; shows whether Antares is installed and running).
- The last connection opens automatically on launch; the screen comes back
  from the tray or a menu ("Switch connection", ⌘⇧O).

### Window and native bits
- One main window showing the dashboard; remembers size and position.
- Tray: connection status, Open, Switch connection, Start/Stop Antares
  (Local), Quit.
- Single instance: launching again focuses the running app.
- Auto-start at login (off by default; offered for Local).
- Notifications for agent events the dashboard already knows about — a turn
  finished while the window was hidden, an approval or a question waiting.
- Lost connection → a clear "Reconnecting…" overlay with Retry and Switch,
  never a blank page.
- `antares://` deep link to open a connection or a session.

### Storage
- `~/Library/Application Support/Antares Desktop/connections.json` (and the
  OS equivalents): names, URLs, modes. No secrets.
- Tokens in the OS keychain (Keychain, Credential Manager, Secret Service).

## Phases

1. **Server**: device tokens, one-time codes, version endpoint. Tests.
2. **Shell, macOS**: Wails v3 project, connection screen, Remote mode, keychain.
3. **Local mode**: daemon detection/start, bundled binary, token handoff.
4. **Native**: tray, single instance, auto-start, notifications, reconnect
   overlay, deep links.
5. **Windows and Linux** builds; check the dashboard in WebView2 and
   WebKitGTK.
6. **Distribution**: macOS signing + notarization, Windows signing, update
   check against GitHub Releases, release workflow alongside the CLI.

Each phase ends with something usable; phase 2 alone already replaces
"open a browser tab to my VPS".

## Open questions

- App name and identity: "Antares" with the red star icon, bundle id
  (`dev.enowdev.antares`?).
- Bundle the `antares` binary in the app (≈ +48 MB) or require the CLI to be
  installed for Local mode?
- Notifications from Remote servers while the app is closed would need a push
  relay; start with notifications only while the app runs?
- Code-signing identities (Apple Developer ID, Windows certificate).

## Later: mobile

Mobile is Remote only in practice. Android could run Antares locally (the
binary cross-compiles with `GOOS=android`, pure-Go SQLite), with limits: a
foreground service, no shell beyond the app sandbox, no headless Chrome, no
ffmpeg unless bundled. iOS cannot run it as a program (no subprocesses; Go
needs cgo linking there), only as an in-app library while the app is open.
The device tokens and one-time codes above are what a mobile app would need
too (plus QR pairing), so phase 1 is not wasted when mobile comes back.
