# Desktop app — contract

Companion to `2026-10-02-desktop-app-plan.md`. This file is the interface
between the three pieces built in parallel: the **server** (`internal/`,
`cmd/antares`), the **dashboard** (`web/`) and the **desktop shell**
(`desktop/`). Anything not written here is each piece's own business. A change
to this contract is made here first, then in code.

Version of this contract: **1**.

---

## 1. Device tokens (server)

A device token is a long-lived credential for one client (a desktop app, later
a phone). It is shown once, stored only as a hash, listed and revocable.

### Format
- Token: `atd_` + 48 lowercase hex chars (24 random bytes). Example shape:
  `atd_3f9a…` (52 chars total).
- Stored: SHA-256 hex of the full token. The plain token is never stored or
  logged.

### Storage
New table `devices` (both dialects the store supports, via the store's
migration registry):

| column | type | notes |
|---|---|---|
| `id` | text pk | `dev_` + 16 hex |
| `name` | text not null | user-visible, e.g. "MacBook Pro" |
| `platform` | text not null | `desktop-macos`, `desktop-windows`, `desktop-linux`, `cli`, `other` |
| `token_hash` | text unique not null | sha256 hex |
| `created_at` | timestamp not null | |
| `last_seen_at` | timestamp null | updated at most once a minute per device |
| `revoked_at` | timestamp null | revoked rows stay for the list, never authorize |

Store methods (names are a suggestion; the behaviour is the contract):
`CreateDevice`, `DeviceByTokenHash`, `ListDevices`, `RevokeDevice`,
`TouchDevice`.

### Authorization
A request is authorized as a client when **any** of these holds:
1. `Authorization: Bearer <server.auth_token>` (existing), or
2. `Authorization: Bearer <device token>` for a device that is not revoked, or
3. a valid dashboard session cookie `antares_dash` (existing, minted by the
   password login or by the handoff below).

This applies everywhere a bearer is accepted today: `withAuth`,
`bearerAuthorized`, `bearerAuthorizedOrQuery` (the `?token=` allowlist accepts
a device token too) and `withDashboardAuth`. Point 3 is new for `withAuth`:
today a server with `auth_token` set refuses a cookie-only browser; after this
change a session cookie is enough. Lookups of device tokens go to the store
(a short in-memory cache, ≤ 30 s, is fine; revocation must take effect within
that window).

### HTTP API

All bodies are JSON. Errors use the server's existing `{"error": "…"}` shape.

#### `POST /api/devices/pair`
Creates a device and returns its token, once.

Request:
```json
{ "name": "MacBook Pro", "platform": "desktop-macos", "password": "…" }
```
- `password` is the dashboard password. Required when the request is not
  already authorized (see Authorization) and a dashboard password is set.
- If the request is already authorized, `password` is ignored.
- If the server has neither a dashboard password nor `auth_token` (an open
  server), pairing is allowed without a password.
- If the server has `auth_token` but no password, an unauthorized request is
  refused: pair with the token (as bearer) or from the CLI.
- Exempt from `withAuth`/`withDashboardAuth` (it authenticates itself), and
  rate-limited like `/api/auth/login`.

Response `200`:
```json
{ "device": { "id": "dev_…", "name": "MacBook Pro", "platform": "desktop-macos",
              "created_at": "2026-10-02T03:00:00Z" },
  "token": "atd_…" }
```
`401` wrong/missing password, `400` bad name/platform (name 1–64 chars after
trim; platform from the list, unknown → `other`).

#### `GET /api/devices` (authorized)
```json
{ "devices": [ { "id": "dev_…", "name": "…", "platform": "…",
                 "created_at": "…", "last_seen_at": "…|null",
                 "revoked_at": "…|null", "current": true } ] }
```
`current` is true for the device whose token made this request.

#### `DELETE /api/devices/{id}` (authorized)
Revokes. `200 {"ok": true}`; `404` unknown id. Revoking the current device is
allowed (the client then loses access).

#### `POST /api/auth/handoff` (authorized)
Mints a one-time login code so a client holding a token can open the
dashboard in a webview without putting the token in a URL.

Request: `{ "next": "/c/ses_123" }` (optional; default `/`).
Response: `{ "code": "ahc_…", "url": "/auth/handoff?code=ahc_…&next=%2Fc%2Fses_123", "expires_in": 60 }`
- Code: `ahc_` + 32 hex, single use, valid 60 s, kept in memory only.

#### `GET /auth/handoff?code=…&next=…` (not under `/api`, no auth)
- Valid code: mints a dashboard session (same as a password login: cookie
  `antares_dash`, HttpOnly, SameSite=Lax, Secure when the request is TLS,
  30 days), consumes the code, `302` to `next`.
- `next` must be a path: starts with `/`, not `//`, no scheme, no `\`;
  anything else is replaced by `/`.
- Invalid/expired/used code: `302` to `/login?handoff=expired`.
- Response has `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

#### `GET /api/version` (no auth)
```json
{ "version": "v0.5.0-16-g9b24008", "contract": 1, "min_desktop": "0.1.0" }
```
Exempt from every auth gate, like `/api/health`.

### CLI
- `antares device pair [--name N] [--platform P] [--json]` — creates a device
  **directly in the local store** (proof of being the local user is access to
  `~/.antares`). Works whether or not the server is running. `--json` prints
  `{"device": {...}, "token": "atd_…", "url": "http://127.0.0.1:8787"}` where
  `url` is the running daemon's URL from the daemon state file, or the
  configured `server.host:port` when not running. Name defaults to the host
  name, platform to `cli`.
- `antares device list [--json]`, `antares device revoke <id>`.

### Dashboard
Settings gets a **Devices** section: the list (name, platform, created, last
seen, "this device" badge, revoked state) and a Revoke action with confirm.
No "create token" button in the web UI for now (pairing happens from the app
or the CLI).

---

## 2. Local daemon (existing behaviour the desktop relies on)

- State dir: `$ANTARES_HOME`, default `~/.antares`.
- Daemon state file: `<state dir>/antares.pid`, JSON:
  ```json
  { "pid": 123, "start_time": "…", "exe": "/path/antares", "url": "http://127.0.0.1:8787",
    "version": "v0.5.0-…", "started_at": "…", "managed": false }
  ```
  A running daemon is: file present, `pid` alive, and `GET {url}/api/health`
  returns `{"ok": true}`. A stale file (dead pid) means not running.
- Start: `antares serve` (returns once the daemon is up; prints
  "Antares is already running (pid …) at …" and exits 0 if it was).
- Stop: `antares stop`.
- These stay as they are; the desktop shell must not write the state file.

---

## 3. Desktop shell (`desktop/`)

### Module
- Separate Go module at `desktop/` (`module github.com/enowdev/antares/desktop`)
  so cgo/Wails never enter the main module: `go build ./...` at the repo root
  must keep working on a machine without a webview toolchain.
- Wails **v3**, version pinned in `desktop/go.mod`.
- App name "Antares", bundle id `dev.enowdev.antares`, icon = the red star
  (`web/public/antares.png`).

### Connections file
`<user config dir>/Antares/connections.json` (`os.UserConfigDir()`):
```json
{
  "version": 1,
  "last": "conn_9c2e…",
  "connections": [
    { "id": "conn_9c2e…", "name": "This Mac", "mode": "local",
      "url": "", "device_id": "dev_…", "created_at": "…" },
    { "id": "conn_51aa…", "name": "VPS", "mode": "remote",
      "url": "https://antares.example.com", "device_id": "dev_…", "created_at": "…" }
  ]
}
```
- At most one `local` connection. `url` is empty for local (resolved at
  connect time from the daemon state).
- No secrets in this file.

### Secrets
Device tokens live in the OS keychain: service `dev.enowdev.antares`,
account = connection `id`, secret = the device token. Removing a connection
deletes its keychain entry and, best effort, revokes the device on the server.

### Connect flows
**Remote (add):** user enters name, URL, password →
1. `GET {url}/api/version` — must answer with `contract` ≥ 1 (else "this
   server is too old, update Antares there").
2. `POST {url}/api/devices/pair` with name = machine name, platform
   `desktop-<os>`, password.
3. Save connection + token.
URL rules: `https://` required, except loopback, private LAN ranges and
Tailscale (`100.64.0.0/10`, `*.ts.net`), where `http://` is allowed.

**Local (add):** run `<antares> device pair --name "<machine>" --platform desktop-<os> --json`
and save the token. `<antares>` = the binary bundled in the app
(`Contents/Resources/antares` on macOS, next to the executable elsewhere),
else `antares` on PATH, else `~/.local/bin/antares`.

**Open (both):**
1. Local: if the daemon is not running, run `<antares> serve` and wait up to
   20 s for health; read `url` from the state file.
2. `POST {url}/api/auth/handoff` with the device token as bearer.
3. Navigate the main window to `{url}{handoff.url}`.
4. `401` on step 2 → token revoked: mark the connection "signed out" and ask
   to pair again.

### Window, tray, behaviour
- One main window; the connection screen is a bundled page; an open
  connection replaces it with the server's dashboard.
- Tray menu: current connection + status, Open Antares, Switch connection…,
  Start/Stop Antares (local only), Quit.
- Single instance: a second launch focuses the first.
- Health poll every 10 s while a connection is open; failure shows the
  bundled "Reconnecting…" page (Retry, Switch connection); recovery
  re-handoffs and returns to the same path if known.
- External links (other origins) open in the system browser.
- Closing the window hides it (app stays in the tray); Quit exits. Quitting
  never stops the local daemon unless the user picked "Stop Antares".
- Notifications are out of scope for v1 of the shell.

### Build
- `make desktop` (repo root) → `desktop/bin/Antares.app` on macOS, bundling a
  freshly built `bin/antares`.
- Not signed/notarized in v1 (no identity yet); ad-hoc signed so it runs
  locally. Documented in `docs/desktop.md`.
