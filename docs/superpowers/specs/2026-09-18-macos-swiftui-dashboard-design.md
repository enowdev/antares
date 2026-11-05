# macOS SwiftUI Dashboard Design

## Summary

Antares keeps the Go agent as the only backend. On macOS, the operator UI
becomes a native SwiftUI app that launches (or attaches to) that backend, then
talks to the existing HTTP/SSE API. Every page in the current React dashboard
is in scope. The React app stays embedded for Linux, Windows, and remote
browsers. The Mac app does not wrap the SPA in a web view.

## Goals

- Opening `Antares.app` starts a usable native dashboard without the operator
  opening Chrome.
- The Go process is the same `antares` binary: agent, store, cron, gateways,
  MCP, tools.
- Every route in `web/src/lib/routeManifest.ts` has a SwiftUI screen that
  calls the same endpoints the web page uses.
- If a backend is already healthy on the configured loopback URL, the app
  attaches and does not spawn a second process.
- If the app spawned the backend, quitting the app stops that child.
- Linux/Windows operators still get the embedded web dashboard.

## Non-goals

- Rewriting `internal/agent` (or any Go package) in Swift.
- Embedding the React SPA in `WKWebView`.
- An App Store sandboxed build in v1 (local companion app, Developer ID /
  unsigned is enough).
- Pixel-identical CSS. Native controls, native navigation, native lists.
- Deleting `web/` or dropping `//go:embed` of the dashboard.
- Changing TUI behaviour.
- iOS / iPadOS / Menu Bar extra targets in v1.

## Architecture

```
Antares.app
  Contents/MacOS/Antares          SwiftUI
  Contents/MacOS/antares          Go binary (same cmd/antares)
           │
           │  spawn `antares --foreground` if /api/health is down
           ▼
  127.0.0.1:8787
           │
           ├─ JSON  GET/POST /api/*
           └─ SSE   POST /api/chat, GET /api/chat/attach,
                    GET /api/logs/stream, intercept breakpoint stream
```

Three layers in `macos/`:

| Layer | Path | Responsibility |
|-------|------|----------------|
| Kit | `macos/AntaresKit/` | Pure Swift: URL, JSON, SSE parser, process supervisor. XCTest / `swift test`. |
| App | `macos/Antares/` | SwiftUI windows, navigation, per-page views. |
| Bundle | `make macos` | Builds `bin/antares` (darwin) and copies it next to the app executable. |

The SwiftUI app never imports Go. It never links the agent. It is a client.

### Runtime supervisor

On launch:

1. Read `ANTARES_URL` or default `http://127.0.0.1:8787`.
2. `GET /api/health` (no auth). If 200, `mode = attached`, do not spawn.
3. Otherwise spawn the bundled `antares` with arguments `--foreground`, inherit
   `HOME` so `~/.antares` is shared with CLI/TUI, `mode = spawned`.
4. Poll health every 200ms, timeout 30s. Show a launch screen with the last
   stderr line if spawn fails.
5. On terminate: if `mode == spawned`, send SIGTERM to the child, wait, SIGKILL
   after 5s. If `mode == attached`, leave the process running.

Port clash: if health fails but the port is in use by something that is not
Antares, surface the error and do not spawn.

### Auth

Match the web client in `web/src/lib/api.ts`:

- `credentials: include` equivalent: shared `URLSession` with cookie storage.
- Optional `Authorization: Bearer` from Keychain (`antares.token`).
- `401` on non-`/auth/` routes presents `LoginView`.
- `428` presents the set-dashboard-password flow.
- Setup: `GET /api/setup/status`; if needed, `SetupView` before the shell.

### Streaming

Port `web/src/lib/sse.ts` byte-for-byte semantics into `SSEClient`:

- CRLF / LF / lone CR line endings
- `data:` join with `\n`, strip one leading space
- ignore `:` comments
- blank line dispatches JSON
- drop `[DONE]`
- unfinished trailing record discarded on EOS

Chat uses `POST /api/chat` with `Accept: text/event-stream`. Attach uses
`GET /api/chat/attach`. Do not use `EventSource` (cannot set headers).

Command catalogue calls `GET /api/commands?surface=web` until a dedicated
`surface=macos` exists. Slash commands stay server-defined.

## Navigation

`NavigationSplitView` sidebar, same information architecture as
`ROUTE_MANIFEST`:

| id | path | Swift view |
|----|------|------------|
| chat | `/`, `/c/:id` | `ChatView` |
| sessions | `/sessions` | `SessionsView` |
| providers | `/providers` | `ProvidersView` |
| tools | `/tools` | `ToolsView` |
| memory | `/memory` | `MemoryView` |
| roles | `/roles` | `RolesView` |
| soul | `/soul` | `SoulView` |
| skills | `/skills` | `SkillsView` |
| cron | `/cron` | `CronView` |
| channels | `/channels` | `ChannelsView` |
| autopilot | `/autopilot` | `AutopilotView` |
| engagement | `/engagement` | `EngagementView` |
| board | `/board` | `BoardView` |
| intercept | `/intercept` | `InterceptView` |
| mcp | `/mcp` | `MCPView` |
| plugins | `/plugins` | `PluginsView` |
| proxies | `/proxies` | `ProxiesView` |
| vps | `/vps` | `VPSView` |
| analytics | `/analytics` | `AnalyticsView` |
| files | `/files` | `FilesView` |
| logs | `/logs` | `LogsView` |
| config | `/config` | `ConfigView` |
| system | `/system` | `SystemView` |
| content-creator | `/content-creator` | `ContentCreatorView` |
| social-media | `/social-media` | `SocialMediaView` |

Plus `LoginView` and `SetupView` outside the split view.

Chat is the default selection. Deep link `antares://c/<sessionId>` is optional
v1.1.

## Appearance

v1 uses system semantic colors (`Color.primary`, `Color.accentColor`) and
respects macOS light/dark. Porting the getdesign palettes (Claude, Facebook,
Pinterest, Supabase) onto Swift `Asset` catalogs is a follow-up, not a blocker
for page parity.

## Testing

- `AntaresKit` tests run with `swift test` (no GUI). SSE parser, JSON error
  envelope, health URL join, spawn-vs-attach decision table.
- SwiftUI pages: smoke that each view constructs with fixture JSON. Full
  interaction tests only where logic is non-trivial (chat event reducer, config
  schema form).
- Do not require Xcode UI tests for v1.
- Go tests stay the source of truth for API behaviour.

## Delivery

- `make macos` produces `macos/Antares/build/Antares.app` (or xcodebuild
  derived data) containing the Go binary.
- README / getting-started gain a macOS app section. `antares` CLI and
  `antares tui` stay documented.
- Minimum OS: macOS 14.

## Success

An operator on a Mac can install the app, complete setup, chat, and reach
every sidebar page without a browser. Linux/Windows installs are unchanged.
