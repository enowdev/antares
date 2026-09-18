# Architecture

One process, one binary, no runtime to install.

```
cmd/antares/          the CLI: serve, setup, doctor, config, cron, and the TUI
internal/
  agent/              the conversation loop, tool dispatch, compaction, harness
  commands/           slash commands, shared by every surface
  llm/                provider adapters and streaming
  tools/              the callable tool surface and the toolsets
  browser/            Chrome DevTools Protocol client and page control
  hub/                skill and MCP catalogue, and the installers
  store/              the Store interface and its SQL implementation
  rag/                retrieval backends
  skills/             the skill library
  memory/             durable facts
  cron/               schedule parsing and the runner
  gateway/            Telegram and Discord
  mcp/                Model Context Protocol client
  tui/                the terminal interface
  server/             HTTP API and dashboard hosting
  wsutil/             a minimal RFC 6455 client
  config/             layered configuration and its schema
web/                  the React dashboard
macos/                AntaresKit + Antares SwiftUI companion (HTTP/SSE to :8787)
```

## The turn

1. `server` or `tui` or `gateway` calls `agent.Run` with a request.
2. The session is resolved or created; history is loaded.
3. The system prompt is built: instructions, environment, standing goal, memory,
   the skill catalogue, tool notes.
4. If the history is close to the context window, older turns are summarised.
5. The model is called, streaming events out as they arrive.
6. Tool calls are dispatched in parallel, each streaming its own progress.
7. Results go back into the history and the loop repeats.
8. When the model stops calling tools, the [harness](harness.md) decides whether
   it is really finished.
9. `done` is emitted. `Run` owns the terminal events, so no caller double-reports.

## Choices worth explaining

**Go, standard library where it can be.** One static binary, nothing to
install on the target, low idle memory. `net/http` method-and-pattern
routing covers every route here, so there is no router dependency; the
`database/sql` layer serves both SQLite and Postgres; `embed` carries the
dashboard. Direct dependencies are the ones with no reasonable in-tree
substitute — `yaml.v3`, the two database drivers (`pgx` and
`modernc/sqlite`, both pure Go), an in-process HNSW graph for RAG, a
browser-fingerprinted HTTP stack for `http_request`, the Bubble Tea stack
for the TUI, and a few narrow utilities (SFTP, IMAP, PDF text). `go.mod`
is the authoritative list.

Schema upgrades use a transactional `schema_migrations` version/checksum ledger.
Legacy databases are adopted without deleting records. Unknown newer versions,
missing ledger versions, and changed migration checksums are rejected. SQLite
serializes upgrades with a writer lock; PostgreSQL uses a transaction advisory lock.

RAG caches one HNSW graph per collection in process. A persisted revision detects
changes from other writers. Graph rebuilds read a consistent database snapshot;
warm queries fetch only candidate rows. Hybrid retrieval combines dense candidates
with FTS5 or PostgreSQL GIN lexical candidates using reciprocal-rank fusion.

**One `Store` interface.** SQLite and Postgres behind the same methods. Two
things make that work: timestamps stored as unix milliseconds so both dialects
agree, and `?` placeholders rebound to `$n` for Postgres. Search is the only
place they diverge — FTS5 against `tsvector`.

**A hand-rolled WebSocket client.** The Discord gateway is the only consumer.
`internal/wsutil` is a few hundred lines of RFC 6455, which is less than a
dependency costs to carry.

**CDP directly for the browser.** No driver process, no Node, no Playwright
install. The protocol is JSON over a WebSocket, and the WebSocket client already
existed.

**Commands defined once.** The terminal, the web chat, and the gateways all
dispatch through `internal/commands`, so a command means the same thing
everywhere and adding one is a single registration.

**The dashboard embedded.** `make build` copies `web/dist` into the binary. One
file to copy anywhere. In development Vite serves the UI and proxies the API.
On macOS, `make macos` can also build an optional native SwiftUI companion; it
does not replace the Go process or the embedded web dashboard.

## The dashboard

`web/src/lib/routeManifest.ts` defines route metadata shared by navigation and
browser smoke checks. `routes.ts` attaches icons and lazy page components.
`AppShell` renders page chrome. English loads eagerly; other locale dictionaries
load only when selected. Development sample routes are excluded from production.

Built with React 19, Vite, Tailwind v4 with oklch tokens, Radix primitives, and
Phosphor icons. Five languages, English by default.

macOS has an optional native companion in `macos/`: `AntaresKit` (HTTP, SSE,
process supervisor) plus the `Antares` SwiftUI app. It talks only to the
existing Go API at `http://127.0.0.1:8787`. The Mac app is optional; the CLI
`antares` still serves the web UI for other platforms. The Go backend is
unchanged.

## Streaming

Events flow the same way to every surface:

```
llm.Client.Stream → agent.Emit → SSE / TUI redraw / message edit
```

The terminal folds them into a live frame, the browser renders them into the
transcript, and Telegram edits its own message as text arrives.

## Failure

- A provider error is reported as an event and ends the turn cleanly.
- A tool error goes back to the model, which usually recovers.
- An MCP server that will not start is reported and skipped.
- A gateway that drops reconnects with exponential backoff.
- A panic in a handler is recovered and logged with the request that caused it.

Nothing here takes the process down. A single tool that will not run should not
cost you the session.

## Testing

Go tests cover the cron parser, provider adapters, the MCP client, the store
against both drivers, the WebSocket client, the hub, the harness, and the
browser against a real Chromium.

The dashboard smoke check loads every manifest route and alias in a headless
browser at desktop and mobile widths, asserting no redirects, exceptions, blank
pages, or horizontal overflow. It uses an isolated authenticated fixture.
That exists because two of the worst bugs so far passed every type check and
blanked the entire page. Nothing static catches that class of failure.
