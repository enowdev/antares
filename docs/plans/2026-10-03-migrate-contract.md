# Migrate from another agent — contract

Lets a user moving from another personal-assistant agent bring their setup to
Antares in one step, from onboarding or later from Settings and the CLI.
Contract version **1**. The Go side of this contract is
`internal/migrate/types.go`; this file explains it and adds the HTTP, CLI and
UI parts. A change is made here and in `types.go` first, then in code.

## Decisions (from the user)

1. Antares gains global **`~/.antares/AGENTS.md`** (instructions for every
   session) and **`~/.antares/USER.md`** (facts about the user), alongside
   `SOUL.md`. They are useful on their own, not only for migration.
2. **Chat history is not migrated.**
3. **Encrypted secrets are decrypted** when the source keeps them encrypted
   (QwenPaw Fernet values, Letta/OpenHuman keychain entries, OpenClaw's
   SQLite auth). The OS keychain prompt that may appear is the user's consent.
   If decryption fails, the item becomes `needs_input`.
4. **Channels Antares has no gateway for** (DingTalk, QQ, WeCom, WeChat,
   iMessage, email, WhatsApp-via-Baileys) are listed as `unsupported` with a
   reason, and skipped.

## Sources (top 10, by GitHub stars on 2026-10-03)

| id | agent | default root | confidence |
|---|---|---|---|
| `openclaw` | OpenClaw | `~/.openclaw` (`OPENCLAW_STATE_DIR`) | medium–high |
| `hermes` | Hermes Agent | `~/.hermes` (profiles under `profiles/`) | high |
| `nanobot` | nanobot | `~/.nanobot` | high |
| `cowagent` | CowAgent | `~/.cow`, workspace `~/cow` | medium–high |
| `openhuman` | OpenHuman | `~/.openhuman` | low |
| `qwenpaw` | QwenPaw (CoPaw) | `~/.qwenpaw`, `~/.copaw` | medium |
| `zeroclaw` | ZeroClaw | `~/.zeroclaw` | low–medium |
| `nanoclaw` | NanoClaw | `~/.config/nanoclaw` | low |
| `picoclaw` | PicoClaw | `~/.picoclaw` | medium (nanobot-like) |
| `letta` | Letta Code | `~/.letta` | medium |

Formats are taken from each project's own config loader source, not from
memory; every source has fixture-based tests under
`internal/migrate/testdata/<id>/`. A source whose format could not be verified
says so in its file header and in `Detection.Summary` is still correct about
what it found.

## What maps where

| Category | Antares target | Notes |
|---|---|---|
| `provider` | `config.Providers[id]` (`Kind`, `BaseURL`, `APIKey` inline, `Headers`, `Models`, `Label`, `Enabled: true`) | Source vendor names map to Antares kinds/catalogue ids (`openrouter`, `anthropic`, `openai`, `gemini`, …); unknown OpenAI-compatible endpoints become custom providers via `server.CustomProviderID`. Conflict when the id exists with a different key/base URL; identical → `ready` no-op. |
| `model` | `config.Model.Default`, `Model.Provider`, `Model.Fallback` | One item. Conflict when Antares already has a working default. |
| `soul` | `~/.antares/SOUL.md` | OpenClaw SOUL.md + IDENTITY.md concatenated; CowAgent AGENT.md; Letta persona block. Conflict when the current file is `custom`. |
| `agents_md` | `~/.antares/AGENTS.md` | Source AGENTS.md / RULE.md / BOOT.md (instructions). |
| `user_md` | `~/.antares/USER.md` | Source USER.md / PROFILE.md / Letta human block. Hermes `memories/USER.md` entries go here too. |
| `memory` | `store.Memory{Scope:"global", Key, Content, Tags, Source:"import:<id>"}` | One item per entry (MEMORY.md split by the source's separator: Hermes `\n§\n`, others by top-level bullet/paragraph). Dedupe by content hash. |
| `knowledge` | RAG collection `import-<id>`, doc id = source-relative path | Daily notes `memory/YYYY-MM-DD.md`, `knowledge/`, Markdown vaults. Skipped silently when RAG is disabled (item `unsupported`, reason "RAG is off"). |
| `skill` | `~/.antares/skills/<name>/` (folder copied whole) | SKILL.md folders only. Conflict on an existing name. |
| `mcp` | `config.MCP.Servers[name]` | `sse`/`streamable_http` → `http`. |
| `cron` | `store.CronJob{Name, Schedule, Prompt, Timezone, Enabled, Meta{"imported_from":id}}` | Intervals → `@every <dur>`; one-shot (`once`/`at`) → `unsupported`. Validated with `cron.Parse`. Imported jobs are **disabled** by default. |
| `channel` | `config.Gateway.<platform>` fields | telegram, discord, slack, matrix, signal, feishu, whatsapp (Meta Cloud API only). Conflict when the platform already has a token. Warn when the source is running. |
| `role` | `~/.antares/roles/<name>.md` | OpenClaw extra agents, CowAgent subagents, Letta agents. |

Never migrated: chat history/sessions, OAuth/subscription logins (Codex,
Copilot, Nous, OpenClaw OAuth profiles — `unsupported`, "sign in again in
Antares"), device-bound WhatsApp (Baileys), one-shot schedules.

## Apply rules

- **Backup first**: `~/.antares/backups/migrate-<source>-<UTC timestamp>/`
  holds copies of `config.yaml`, `SOUL.md`, `AGENTS.md`, `USER.md` and any
  skill/role file about to be replaced, plus `manifest.json` listing every
  row/file created (memory ids, cron job ids, RAG doc ids, new skill/role
  paths). `Report.Backup` points at it.
- **Undo**: `POST /api/migrate/undo {backup}` / `antares migrate undo <dir>`
  restores the files and deletes what the manifest lists as created.
- Only items in the request's `items` are applied; `unsupported` items are
  never applied; a `conflict` without a resolution is skipped; a
  `needs_input` without input is skipped.
- Resolutions: `skip` (default), `replace`, `rename` (providers, skills, MCP,
  roles: next free id/name with `-2`, `-3`…), `append` (soul, agents_md,
  user_md: imported text added under `## Imported from <Agent>`).
- Config is written once (load → mutate → `config.Save`), then the server's
  reload runs (gateway/MCP pick up changes). Store writes use the store API.
- Secrets never appear in API responses, logs, the manifest or errors.

## Global AGENTS.md and USER.md (decision 1)

- Paths: `config.AgentsMDPath()` = `<home>/AGENTS.md`, `config.UserMDPath()`
  = `<home>/USER.md`; `config.LoadAgentsMD()`, `config.SaveAgentsMD(s)`,
  same for USER (already in `internal/config/persona.go`). Missing file =
  empty, no placeholder; saving empty removes the file.
- System prompt (`internal/agent/prompt.go`): after the soul block, add
  `## Your instructions` with AGENTS.md and `## About the user` with USER.md
  when non-empty, for every session including channel sessions, but not for
  subordinate runs that already get a narrowed prompt (follow how
  `isSubordinateRun` treats the soul). Project `AGENTS.md`/`CLAUDE.md` still
  apply on top, after these (project wins on conflict, say so in the prompt).
- Size cap: 16 KB each in the prompt (truncate with a note).
- API: `GET /api/persona/{file}` and `POST /api/persona/{file}` for
  `file` ∈ `soul|agents|user` → `{content, path}`; existing `/api/soul` stays
  as an alias. POST requires `requireDashboardPassword`-level auth like other
  config writes do today for soul.
- Dashboard: the Soul page (Agent › Soul) becomes **Persona** with three tabs
  — Soul, Instructions (AGENTS.md), About you (USER.md) — each an editor with
  save. i18n keys in en + id.

## HTTP API

All under `/api/migrate`, authorized like other config endpoints, and
`requireDashboardPassword` for plan/apply (secrets). JSON.

- `GET /api/migrate/sources` →
  `{ "sources": [ {"id","name","detected":[Detection…]} … ] }` — every
  registered source, with what Detect found (empty list = not installed).
- `POST /api/migrate/plan` `{ "source": "hermes", "root": "" , "profile": "" }`
  → `Plan` (items without payloads). Plans are cached server-side for 10
  minutes under `{plan_id}`; the response adds `"plan_id"`.
- `POST /api/migrate/apply` `{ "plan_id": "…", "items": [Choice…] }` →
  `Report`. `410` if the plan expired (client re-plans).
- `POST /api/migrate/undo` `{ "backup": "<dir name>" }` → `{ "ok": true }`.

## CLI

```
antares migrate                      # list detected agents
antares migrate <source> [--root DIR] [--profile P] [--dry-run]
                [--only provider,skill,…] [--yes] [--conflict skip|replace|rename|append]
antares migrate undo <backup-dir>
```
Interactive by default: prints the plan grouped by category, asks to confirm,
prompts for `needs_input` keys with hidden input. `--dry-run` prints the plan
only. `--yes` applies all ready items and resolves conflicts with
`--conflict` (default skip).

## Onboarding and Settings

- **Setup wizard**: a first step "Coming from another agent?" shown when
  `GET /api/migrate/sources` detects anything (otherwise skipped, with a small
  "import from another agent" link on the provider step). Picking one shows
  the plan grouped by category with checkboxes, conflict selectors and key
  fields for `needs_input`, a warning banner if the source is running, then
  Apply → result summary. If after apply `needs_setup` is false, the wizard
  skips the provider/model steps.
- **Settings › Migrate** (System hub): the same picker/plan/apply UI any time,
  plus the list of past migrations (backup dirs) with Undo.
- Soft design: rounded panels, pill controls, monochrome, `data-reveal` on
  sections.

## Tests

- Each source: fixtures (a fake home dir built from the project's documented
  shapes/loader source) → Detect and Plan golden tests; secrets redacted in
  goldens.
- Apply: temp `ANTARES_HOME` + sqlite store; every category, each resolution,
  backup + undo round-trip, idempotence (re-applying a ready provider is a
  no-op), secrets absent from report/manifest/logs.
- API: plan cache expiry, auth, item filtering.
- Decryption: QwenPaw Fernet with a fixture master key; keychain reads go
  through `migrate.Keychain` / `migrate.DefaultKeychain`
  (`internal/migrate/keychain.go`, go-keyring), replaced by a fake in tests.
