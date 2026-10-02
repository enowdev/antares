# HTTP API

`antares serve` exposes a JSON API on `:8787` and serves the dashboard from the
same port. Everything the dashboard does is available here.

## Authentication

While `server.auth_token` is empty the API is open, which is right behind a
private network and wrong anywhere else.

The mutating onboarding endpoints (`/api/setup/test` and
`/api/setup/complete`) and first dashboard-password bootstrap are additionally
restricted to a loopback client until a bearer token has been configured. This
prevents a remote first writer from claiming or reconfiguring a new instance.

```bash
antares config set server.auth_token "$(openssl rand -hex 24)"
```

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8787/api/status
```

Bearer tokens are accepted in query strings only for the explicitly documented
SSE/media routes that cannot set request headers. Ordinary JSON and mutating
routes require the `Authorization` header.

`/api/health` and `/api/version` are always open, so a health check needs no
credential.

With `server.auth_token` set, a request is authorized by any of:

- `Authorization: Bearer <server.auth_token>`;
- `Authorization: Bearer <device token>` for a paired device that has not been
  revoked (see [Devices](#devices));
- a dashboard session cookie (`antares_dash`), minted by the password login or
  by a device handoff.

The same three are accepted wherever the dashboard password gate applies.
Failed password attempts (login and pairing) are limited to 10 per 5 minutes
per client address; past that both answer `429`.

## Devices

A device token (`atd_` + 48 hex) is a long-lived credential for one client,
such as the desktop app. It is shown once, stored only as a SHA-256 hash,
listed, and revocable. Revocation done through the API applies at once; one
done from the CLI on the server applies within 30 seconds.

| | |
|---|---|
| `POST /api/devices/pair` | Create a device and return its token, once |
| `GET /api/devices` | List devices; `current` marks the caller's |
| `DELETE /api/devices/{id}` | Revoke (also ends dashboard sessions it opened) |
| `POST /api/auth/handoff` | One-time code to open the dashboard signed in |
| `GET /auth/handoff?code=&next=` | Trade the code for a session cookie, redirect |
| `GET /api/version` | `{"version", "contract", "min_desktop"}`, no auth |

```bash
curl -X POST http://localhost:8787/api/devices/pair \
  -H 'Content-Type: application/json' \
  -d '{"name": "MacBook Pro", "platform": "desktop-macos", "password": "…"}'
# {"device": {"id": "dev_…", "name": "MacBook Pro", "platform": "desktop-macos",
#             "created_at": "…"}, "token": "atd_…"}
```

Pairing authenticates itself. An already authorized request (bearer or
cookie) pairs without a password. Otherwise the dashboard password is
required when one is set; a server with only `auth_token` refuses (pair with
the token as bearer, or run `antares device pair` on the server); an open
server pairs freely. `name` is 1–64 characters; `platform` is one of
`desktop-macos`, `desktop-windows`, `desktop-linux`, `cli`, `other` (anything
else becomes `other`).

The handoff lets a client that holds a token open the dashboard in a browser
or webview without putting the token in a URL:

```bash
curl -X POST http://localhost:8787/api/auth/handoff \
  -H "Authorization: Bearer $DEVICE_TOKEN" -d '{"next": "/c/ses_123"}'
# {"code": "ahc_…", "url": "/auth/handoff?code=ahc_…&next=%2Fc%2Fses_123", "expires_in": 60}
```

The code is single use and valid for 60 seconds. Opening `url` sets the
session cookie and redirects to `next`; `next` must be a same-origin path, and
anything else becomes `/`. An expired or used code redirects to
`/login?handoff=expired`.

## Chat

### `POST /api/chat`

Streams a turn as server-sent events.

```bash
curl -N -X POST http://localhost:8787/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"message": "what changed in this repo today?", "session_id": ""}'
```

An empty `session_id` starts a new conversation; the `session` event carries the
id assigned.

| Event | Fields | Meaning |
|---|---|---|
| `session` | `id`, `title` | Which conversation this is |
| `turn` | `turn` | A new model call began |
| `text` | `delta` | Reply text |
| `reasoning` | `delta` | Reasoning, when the model exposes it |
| `tool_call` | `id`, `name`, `arguments` | A tool is about to run |
| `tool_progress` | `id`, `chunk` | Incremental output from it |
| `tool_result` | `id`, `content`, `is_error` | What it returned |
| `usage` | `input_tokens`, `output_tokens` | Running totals |
| `notice` | `message` | Compaction, steering, goal, guard |
| `error` | `error` | The turn failed |
| `done` | | Terminal — always last |

Terminal failures are also stored as assistant rows with `meta.is_error=true`.
Their `content` is a one-field JSON object (`{"error":"..."}`), so the
dashboard can render the failure after a reload and retry the preceding prompt.

### `POST /api/chat/interrupt`

```json
{"session_id": "sess_…"}
```

## Sessions

| | |
|---|---|
| `GET /api/sessions` | List, paginated |
| `GET /api/sessions/search?q=` | Full-text search |
| `GET /api/sessions/{id}` | One session with its messages |
| `DELETE /api/sessions/{id}` | Delete |
| `POST /api/sessions/{id}/title` | Rename |
| `GET /api/sessions/empty/count` | How many are empty |
| `POST /api/sessions/empty/delete` | Remove those |
| `POST /api/sessions/prune` | Delete older than a cutoff |

## Commands

| | |
|---|---|
| `GET /api/commands?surface=web` | The catalogue for a surface |
| `POST /api/commands/run` | Run one |

```json
{"input": "/status", "session_id": "sess_…", "surface": "web"}
```

Returns `{"ok": true, "output": "…", "action": {...}}`. A command that fails
comes back with `ok: false` and an error — a failed command is a normal outcome,
not a transport error.

## Models and providers

| | |
|---|---|
| `GET /api/model/options` | Configured providers and the active model |
| `GET /api/model/list?provider=` | What a provider offers |
| `POST /api/model/set` | `{"model": "...", "provider": "..."}` |
| `POST /api/providers/{id}/key` | Verify a key against the provider, then save |

## The hub

| | |
|---|---|
| `GET /api/hub/skills?q=` | Search skills |
| `POST /api/hub/skills/install` | `{"id": "builtin/code-review"}` |
| `GET /api/hub/mcp?q=` | Search MCP servers |
| `POST /api/hub/mcp/install` | `{"id": "github"}` |

## Skills, memory, retrieval

| | |
|---|---|
| `GET /api/skills` | List |
| `POST /api/skills` | Create |
| `GET /api/skills/{name}` | Read the body |
| `POST /api/skills/toggle` | Enable or disable |
| `DELETE /api/skills/{name}` | Delete |
| `GET /api/memory` | List |
| `POST /api/memory` | Add |
| `GET /api/memory/search?q=` | Search |
| `DELETE /api/memory/{id}` | Delete |
| `GET /api/rag/status` | Backend state and collections |
| `POST /api/rag/index` | Index a path |
| `POST /api/rag/search` | Query |
| `DELETE /api/rag/collections/{name}` | Drop a collection |

Skill list/get responses include `read_only`. Automatically discovered skills can
be read and toggled; toggles persist exact names in `skills.disabled`, not source
files. Save and delete return HTTP 403 without changing borrowed content or
creating a configured override. Toggle persistence/reload failures return HTTP 500;
success follows both persistence and live publication. These endpoints use the startup catalog;
`POST /api/commands/run` with `/skills` and a `session_id` uses that session's
persisted project binding. See [skill sources and precedence](skills.md#where-they-live).

## Scheduling and channels

| | |
|---|---|
| `GET /api/cron/jobs` | List |
| `POST /api/cron/jobs` | Create |
| `DELETE /api/cron/jobs/{id}` | Delete |
| `POST /api/cron/jobs/{id}/toggle` | Pause or resume |
| `POST /api/cron/jobs/{id}/run` | Run now |
| `GET /api/cron/jobs/{id}/runs` | History |
| `GET /api/cron/validate?schedule=` | Check an expression, preview run times |
| `GET /api/channels` | Channels and pairings |
| `POST /api/channels/{id}/toggle` | Enable or disable |
| `POST /api/channels/{id}/token` | Verify a bot token, then save |
| `POST /api/pairing/approve` | Approve a pending user |
| `POST /api/pairing/revoke` | Revoke one |

## Configuration and diagnostics

| | |
|---|---|
| `GET /api/config` | Current settings, secrets redacted |
| `POST /api/config` | Set one path |
| `GET /api/config/raw` | The YAML file |
| `POST /api/config/raw` | Replace it |
| `GET /api/config/schema` | Field metadata that drives the Settings form |
| `GET /api/status` | Version, uptime, model, counts |
| `GET /api/system/stats` | Host CPU, memory, disk |
| `GET /api/analytics` | Token and cost series |
| `GET /api/logs` | Recent log lines |
| `GET /api/logs/stream` | Live tail, SSE |
| `GET /api/tools` | Registered tools and their toolsets |
| `GET /api/mcp` | MCP servers and connection state |
| `GET /api/files` | Browse the workspace |
| `GET /api/files/read` | Read a file from it |
| `GET /api/ui/modules` | Dashboard modules shown in the sidebar |
| `POST /api/ui/modules` | Change them |

Both module endpoints return `{"modules": ["automation"], "preset": "coding"}`.
`modules` is `null` when `display.modules` is absent from `config.yaml`, which
means every module is on; `preset` is then `"full"`. `POST` takes the same
shape, requires both fields, and answers with what was saved, the list in the
fixed order `automation`, `security`, `studio`. An unknown module id, a
duplicate, or a preset other than `general`, `coding`, `security`, `creator`,
`full` or `custom` is a 400. See [Dashboard modules](configuration.md#dashboard-modules).

## Setup

| | |
|---|---|
| `GET /api/setup/status` | Whether setup is needed, and the provider catalogue |
| `POST /api/setup/test` | Try a provider and key |
| `POST /api/setup/complete` | Write the configuration |

`POST /api/setup/complete` also takes optional `modules` (a list of module
ids) and `preset`, validated as for `POST /api/ui/modules`. With `modules`
omitted, `display.modules` stays absent and every module is shown; `[]` stores
the General preset. A `preset` without `modules` is a 400.

## Content Creator

| Method and path | Purpose |
|---|---|
| `GET /api/content-creator/projects` | List saved projects |
| `POST /api/content-creator/projects` | Create a project with title and brief |
| `GET /api/content-creator/projects/{id}` | Fetch current project, artifacts, and run status |
| `PUT /api/content-creator/projects/{id}` | Update the plan using its current `revision` |
| `POST /api/content-creator/projects/{id}/action` | Generate a reference/keyframe/clip, poll video, assemble, or explicitly reset |
| `POST /api/content-creator/projects/{id}/run` | Start the Content Creator agent at a requested stage; returns `session_id` |
| `POST /api/content-creator/projects/{id}/reconcile` | Operator verifies an uncertain upload was not published before another attempt |
| `GET /api/content-creator/projects/{id}/artifact?path=...` | Read registered project media; supports video range requests |
| `GET /api/content-creator/settings` | Read media configuration and FFmpeg availability without credentials |
| `POST /api/content-creator/settings` | Save image/video configuration; blank API keys preserve existing keys |

Media actions take `{ "action": "generate_video", "target_id": "shot-id" }`.
Available actions: `generate_reference`, `generate_keyframe`, `generate_video`,
`poll_video`, `assemble`, `reset_reference`, `reset_shot`. Resets also require
`confirmed: true`. Video creation persists the provider job ID; poll it until
complete instead of creating another paid job. Run requests take `stage` and
`publish_mode`; a publish stage in draft mode is rejected.

## Errors

```json
{"error": "no provider named \"nope\" is configured"}
```

`400` for a bad request, `401` for a missing or wrong token, `404` for an
unknown path, `409` when a session is already busy or setup has already
completed, `429` when a request would exceed `max_concurrent_sessions`
(the running top-level turn budget; `0` means unlimited), and `500` for a
failure inside. Endpoints where failure is an ordinary outcome —
installing, verifying a key, running a command — return `200` with
`ok: false` instead, so the caller can show the message rather than a
banner.
