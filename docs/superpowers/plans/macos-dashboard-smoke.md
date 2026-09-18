# macOS dashboard smoke checklist

Manual pass against a live `antares --foreground` (default `http://127.0.0.1:8787`).

## Build and open

```bash
make macos
open macos/build/Build/Products/Release/Antares.app
```

From the package (no app bundle): `cd macos/Antares && swift build`. Debug binary: `.build/debug/Antares`.

If nothing is listening on `:8787`, the app should spawn the bundled `antares --foreground`. If a healthy backend is already up, it should attach and not spawn a second process.

## Login / setup

- [ ] Launch gate: wait for health (or a clear spawn error, not a crash).
- [ ] First-run setup (`GET /api/setup/status` incomplete): provider + key complete without crash.
- [ ] Login: password bootstrap on 428, then `POST /api/auth/login` when a dashboard password is set.
- [ ] After auth, `AppShell` appears with Chat selected.

## Every sidebar row

Click each row. The detail pane must load without crashing (empty/error states are OK).

- [ ] Chat
- [ ] Sessions
- [ ] Providers
- [ ] Tools
- [ ] Memory
- [ ] Roles
- [ ] Soul
- [ ] Skills
- [ ] Cron
- [ ] Channels
- [ ] Autopilot
- [ ] Engagement
- [ ] Board
- [ ] Intercept
- [ ] MCP
- [ ] Plugins
- [ ] Proxies
- [ ] VPS
- [ ] Analytics
- [ ] Files
- [ ] Logs
- [ ] Config
- [ ] System
- [ ] Content creator
- [ ] Social media

## Chat send + stream

- [ ] Send a message from Chat.
- [ ] Assistant text streams (SSE) into the transcript.
- [ ] Optional: interrupt, then reload the session.

## Config GET

- [ ] Open Config.
- [ ] Schema-driven form loads from `GET /api/config/schema` (or a visible error, not a crash).
- [ ] Optional: Raw YAML tab from `GET /api/config/raw`.

## Live backend notes (2026-09-18)

Against an already-running `antares` on `http://127.0.0.1:8787` (HTTP only; GUI click-through was not run):

- `GET /api/health` → 200 `{"ok":true}`
- `GET /api/setup/status` → 200, `needs_setup: false`
- `GET /api/auth/status` → 200, `password_required: true` (login gate would show)
- `GET /api/config/schema` → 401 without a session (expected; Config should show an error, not crash)

Chat send+stream and authenticated config GET still need a logged-in GUI pass.
