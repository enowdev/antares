# Antares — Handoff

Last updated: 2026-08-08. Branch: `main`. Working tree: clean.
**11 commits are committed locally but NOT pushed** (`dff21d7`..`0119b7a`).

## Goal

Ongoing maintenance of Antares (Go backend + React/Vite dashboard in `web/`).
This session cleared the open PR, added an LLM provider, and fixed a run of
dashboard and Discord/Telegram gateway issues reported by the user. Everything
below is done and green unless called out under **Next Steps**.

## How to build / run (read this first)

- **Toolchain:** always build/test with `GOTOOLCHAIN=go1.26.3` (the repo needs Go
  1.26; httpcloak v1.6.8 forces it). A bare `go build` may pick the wrong
  toolchain.
- **Dev:** `make dev` runs BOTH the Air-reloaded backend on **:8787** and Vite on
  **:5173**. **Open :5173** — it hot-reloads the frontend and proxies `/api` to
  :8787. **:8787 serves the embedded React bundle**, which Air does NOT rebuild on
  frontend changes, so it shows a stale UI until `make build-web` runs. Several
  "the fix didn't work" reports this session were just the user viewing :8787.
- **Embedded bundle:** `make build-web` builds `web/dist` and copies it to
  `internal/server/dist` (which is gitignored except `.gitkeep` — keep that file;
  `make build-web` deletes it, so `touch internal/server/dist/.gitkeep` after).
- **Run the binary:** on macOS use `antares --foreground`. Cross-compiled
  `CGO_ENABLED=0` darwin binaries get an invalid ad-hoc signature and are
  SIGKILLed (exit 137) on other machines — fix with `codesign --force --sign -`.
- Tests: `GOTOOLCHAIN=go1.26.3 go test ./...` → 32 packages, all green.

## Current Progress (this session, all committed, not pushed)

1. **Merged PR #18** (`dff21d7`) + review fixes (`5d8406a`): edit_file CRLF/line-
   prefix recovery, vps_run timeouts + new vps_upload/vps_download SFTP tools,
   persisted context compaction, dashboard session persistence, MCP refresh,
   composer history, Gemini thoughtSignature. Review fixes I added on top:
   - `ANTARES_API_KEY`/`ANTARES_BASE_URL` had gone silently inert (PR inverted
     credential precedence); restored env-override via `inline*FromEnv` flags.
   - `POST /api/mcp/refresh` nil-panic when MCP unconfigured (typed-nil in
     interface); guarded.
   - `vps_download`/`vps_upload` could truncate the destination on a mid-transfer
     timeout, and download copied the remote file's perm bits (world-writable
     risk); both now stream to a temp path + rename, download forces 0600.
2. **OpenCode Go (Zen) provider** (`4693848`): new `kind: opencode`. It routes
   per-model — MiniMax/Qwen → Anthropic `/messages` + x-api-key, everything else
   → OpenAI `/chat/completions` + Bearer (see `internal/llm/opencode.go`). Family
   match is prefix-based. Verified endpoints answer 401 (not 404) live.
3. **Dashboard scroll judder while streaming** (`b0b00ac`): moved the streaming
   indicator out of the Virtuoso Footer, memoised components, `followOutput={true}`,
   stable `initialTopMostItemIndex`.
4. **Transcript OOM crash** ("Aw, Snap!") on a tool-heavy turn (`e738c7c`):
   the whole turn is ONE Virtuoso item (list virtualises messages, not segments),
   and unmemoised segment children made it O(N²) re-render. Fixed by memoising
   `ToolCallCard`, `ReasoningBlock`, new `TextSegment`. (LCS diff was ruled out by
   benchmark — write_file takes the cheap empty-old branch.)
5. **Per-tab resumed session** (`a16f4bb`): the "resume last session" pointer
   moved from `localStorage` (shared across tabs) to `sessionStorage` (per-tab),
   so two tabs can hold different sessions. Auth token stays in localStorage.
6. **Gateway trio** (`7abfb93`): `reply_mode: "always"` now actually works
   (`handleMessage` never read it before); **per-user group sessions** via
   `GatewaySessionKey` (folds user id into the KV key in groups — the
   `group_sessions_per_user` flag was defined but never used); new **`none`
   toolset** for a tool-free chat turn.
7. **Discord replies** (`47ae931`): the bot now replies to the triggering message
   (`message_reference`) + pings the author, first chunk only. Shared body builder
   `discordMessagePayload`.
8. **Sender identity to the agent** (`f76833e`): captures Discord server nickname
   (`member.nick`) + `global_name`; system prompt gets a `Talking to: <name>
   (<platform> user id <id>)` line. Fixes the bot saying "I can't see your nick".
9. **Per-user RAG** (`0119b7a`): opt-in `rag.per_user`. Each user gets their own
   RAG collection (`rag.UserCollection`); `indexUserTurn` summarises each turn
   into durable facts via the aux model and indexes them; `autoContext` folds the
   user's collection in first. Toggle surfaces automatically in the config editor.

## What Worked

- **Benchmark before fixing.** The OOM's first suspect (LCS diff) was cleared by a
  quick micro-benchmark — the real cause was O(N²) re-render. Don't fix by
  hypothesis alone here.
- **Reflected config schema.** Adding a `yaml`-tagged field to a config struct
  makes it appear in the dashboard config editor automatically (`config.Schema()`
  walks the struct). Add the path to `common`/`help` tables in
  `internal/config/schema.go` for tier + description; no frontend work.
- **Small pure helpers + unit tests** for the risky gateway logic
  (`messageAddressesBot`, `discordDisplayName`, `GatewaySessionKey`,
  `discordMessagePayload`, `UserCollection`) instead of trying to test the
  network-touching handlers.
- **Worktree + subagent** to review the large PR #18 without polluting the main
  tree.

## What Didn't Work / Gotchas

- **Disk kept filling** (ENOSPC, <500 MiB free) mid-session, which fails Go
  builds cryptically. The USER frees space themselves — **do not delete anything**
  without explicit say-so.
- **`make dev` run from inside the agent's tool sandbox** gets killed on session
  teardown and leaves orphan `air`/`vite` processes that then hold ports 5173/8787.
  Tell the user to run it in their own terminal.
- Viewing **:8787 instead of :5173** repeatedly looked like "the fix didn't
  land". Always confirm which port the user is on.
- `ResolveBinding` **skips disabled bindings**, so a binding with `reply_mode:
  always` but `enabled: false` does nothing — the likely real cause of the user's
  original "always doesn't work". They must enable the binding.
- gopls shows **stale "undefined" diagnostics** from a lowercase-path module alias;
  the real `go build` is authoritative, trust it over the editor.

## Next Steps

1. **Push the 11 local commits** once the user is ready (they have not asked yet —
   do NOT push without confirmation).
2. **Agent loop guardrail (still open — the fix once proposed here was tried and
   reverted).** The agent can loop writing the same file with slightly different
   contents; the repeat tracker fingerprints name+args, so changing the content
   never trips it. The fix this note used to recommend — treat repeated
   `write_file`/`edit_file` to the SAME path as a repeat regardless of content —
   was implemented and then removed again in "Tell a stuck loop apart from
   ordinary progress". Keying on the path alone cannot tell three different edits
   to one file from one edit made three times, so it fired on ordinary work, which
   for a coding agent is most of the work. **Do not reintroduce it.** `repeatKey`
   is now uniform over the full normalised arguments for every tool, and
   `internal/agent/repeat_guard_test.go` fails if `write_file`, `edit_file` or
   `vps_upload` is given a coarser key again. Those are the three names that ever
   carried a special case, and the test names each of them; it asserts nothing
   about any other tool, so a coarser key introduced for a different tool would
   pass. The loop itself is therefore still unsolved: a model that varies the
   content each time is bounded only by the ceilings below, and any replacement
   needs a signal other than the call fingerprint. This is what produced the giant
   turn that caused the OOM in item 4 — the frontend is now hardened, but the loop
   remains.

   The ceilings as they actually stand: the hard stop is 60 tool calls per segment
   (`HardStopAfter`), `grContinue` may reset it up to 4× (`maxGuardrailContinues`,
   `harness.go:477`) for five segments, and `AbsoluteMaxToolCalls: 200`
   (`config/defaults.go:137`) caps the whole run regardless — so 200 calls, not the
   ~600 this note previously claimed.
3. **Rotate exposed credentials.** The Z.ai API key and Voyage embed key were
   visible in `~/.antares/config.yaml` read during earlier sessions. Still
   outstanding; user's call.
4. **macOS release signing** in `scripts/release-build.sh`: darwin release
   binaries need `codesign --force --sign -` or they SIGKILL on other Macs.
5. **Live verification of this session's gateway/RAG work** (needs a real Discord
   bot + RAG enabled): nickname shows in prompt; `reply_mode: always` on an
   ENABLED binding answers un-addressed messages; per-user sessions stay separate;
   replies thread correctly; `rag.per_user` populates `user-discord-*` collections.
6. **Custom-provider "Add" UI** — backend fully supports multiple custom providers;
   only the dashboard "add" form is missing. Deferred by the user earlier.

## Key Files

- `internal/llm/opencode.go`, `internal/llm/client.go` — OpenCode provider + kind wiring.
- `internal/gateway/discord.go`, `telegram.go`, `binding.go` — reply_mode, replies, identity, `GatewaySessionKey`.
- `internal/agent/ragcontext.go` — `autoContext`, `indexTurn`, `indexUserTurn`, `summariseUserTurn`.
- `internal/rag/user.go` — `UserCollection`.
- `internal/config/config.go`, `schema.go`, `load.go` — `rag.per_user`, inline-cred env flags, reflected schema.
- `internal/tools/registry.go` — `none` toolset.
- `web/src/pages/ChatPage.tsx` — streaming indicator, memoised segments, per-tab session.
- `web/src/components/chat/ToolCallCard.tsx` — memoised.
