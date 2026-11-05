# Configuration

Everything lives in `~/.antares/config.yaml`. Four ways to change it, all
writing the same file:

```bash
antares config get model.default
antares config set model.default anthropic/claude-sonnet-4.5
antares config path                     # where the file is
antares config edit                     # open it in $EDITOR
```

- The **Settings page** in the dashboard is a form built from the schema, so it
  always matches the binary.
- `/config model.default` reads and `/config model.default <value>` writes, from
  any conversation.
- Environment variables override the file. `~/.antares/.env` is loaded first.

Settings are grouped into three tiers so the Settings page opens on what
matters: **essential** (a handful you will set on day one), **common**, and
**advanced**.

## Where it all lives

```
~/.antares/
  config.yaml    settings
  .env           secrets, loaded automatically
  antares.db     sessions, messages, memory, vectors, schedules
  skills/        the skill library
  browser/       the browser profile
```

`ANTARES_HOME` relocates the lot — the way to run two instances on one machine.

## Model and providers

```yaml
model:
  default: anthropic/claude-sonnet-4.5
  provider: openrouter
  auxiliary: ""            # cheap model for titles, summaries, verification
  temperature: 0.7
  max_tokens: 8192
  context_window: 0        # 0 asks the provider
  reasoning_effort: medium

providers:
  openrouter:
    kind: openai-compatible
    base_url: https://openrouter.ai/api/v1
    api_key: sk-or-…
  anthropic:
    kind: anthropic
    base_url: https://api.anthropic.com/v1
    api_key: sk-ant-…
  ollama:
    kind: openai-compatible
    base_url: http://127.0.0.1:11434/v1
```

| `kind` | Behaviour |
|---|---|
| `openai-compatible` | The default shape. OpenRouter, Together, Groq, Ollama, LM Studio, vLLM |
| `openai` | OpenAI proper |
| `anthropic` | Extended thinking and prompt caching |
| `gemini` | Thinking budgets |
| `opencode` | OpenCode Go (Zen) — routes each model to the wire format it needs |
| `custom` | Anything else with a `base_url` |

`opencode` exists because OpenCode Zen serves two wire formats from one base
URL, chosen per model: MiniMax and Qwen speak the Anthropic Messages API
(`/messages`, `x-api-key`), everything else speaks OpenAI Chat Completions
(`/chat/completions`, `Authorization: Bearer`). Every other kind picks its
format once per provider, so this one routes per request:

```yaml
providers:
  opencode:
    kind: opencode
    base_url: https://opencode.ai/zen/go/v1
    api_key: …            # or OPENCODE_API_KEY
```

Get a key at <https://opencode.ai/auth>. The model list is live — Antares reads
it from the provider's `/models` endpoint, so new models appear without a
release. A model family Antares does not recognise defaults to the OpenAI
format, which is correct for the GLM, Kimi, DeepSeek, MiMo, GPT, Grok and
Hunyuan families it currently serves.

`model.auxiliary` is worth setting. Titles, compaction summaries, verification,
and goal judging all use it, and a small model does those as well as a large one
for a fraction of the cost.

Keys can come from the environment instead:

```bash
ANTHROPIC_API_KEY=…
OPENAI_API_KEY=…
OPENROUTER_API_KEY=…
OPENCODE_API_KEY=…
GEMINI_API_KEY=…
```

## Storage

```yaml
database:
  driver: sqlite            # sqlite | postgres | memory
  dsn: ~/.antares/antares.db
  wal: true
```

```yaml
database:
  driver: postgres
  dsn: postgres://user:pass@localhost:5432/antares?sslmode=disable
  max_conns: 10
```

Both schemas are created on first run. SQLite searches conversations with FTS5,
Postgres with `tsvector`. `memory` keeps everything in RAM and is for tests.

## Tools
```yaml
tools:
  toolset: coding           # minimal | coding | research | browser | default | all
  enabled: []               # add to the toolset
  disabled: []              # remove from it
  approval_mode: prompt     # auto | prompt | deny
  max_output_chars: 60000
  timeouts:
    terminal: 300
```

The shipped defaults are `toolset: coding` (a file/edit/shell/browser
working set) and `approval_mode: prompt` (mutating tools ask first). Set
`auto` for unattended runs, `deny` to refuse mutations outright, or a
narrower `toolset` such as `research` to drop the shell entirely. See
[Tools](tools.md) for what is in each toolset.

**Web search:**

```yaml
tools:
  web_search:
    provider: duckduckgo    # duckduckgo | brave | tavily | searxng | none
    api_key: ""
    max_results: 8
```

DuckDuckGo needs no key. The others do, and are better.

**Browser:** see [Browser](browser.md).

## The terminal tool

```yaml
terminal:
  backend: local            # local | docker | ssh
  cwd: ~/antares-workspace
  timeout: 120
  shell: ""                 # empty picks the platform default
  blocked_commands: []
  allow_network: true
  docker_image: ""
  ssh_host: ""
```

The shell is persistent: `cd`, exported variables, and activated environments
survive between calls within a session.

## The agent loop

```yaml
agent:
  max_turns: 200
  workspace: ~/antares-workspace
  personality: default
  system_prompt_extra: ""
  timezone: Local
  language: auto

  repeat_limit: 3
  verify_replies: false
  verify_max: 2
  goal_max_iterations: 10
```

`system_prompt_extra` is appended to every system prompt — the place for
standing instructions about how you want it to work.

The last four are the harness. See [Harness](harness.md).

## Memory and retrieval

```yaml
memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 4000
  search_limit: 10

rag:
  enabled: false
  provider: builtin         # builtin | enowx
  embed_model: text-embedding-3-small
  chunk_size: 1200
  chunk_overlap: 150
  top_k: 8
  hybrid: true
```

See [Memory and RAG](memory-and-rag.md).

## Compaction

```yaml
compression:
  enabled: true
  threshold: 0.75           # fraction of the context window
  target_ratio: 0.5
  protect_last_n: 6
  protect_first_n: 2
```

As a conversation approaches the context window, older turns are summarised
while recent ones stay verbatim. A tool call is never separated from its result,
which would leave the model reading a call with no answer.

## Skills

```yaml
skills:
  enabled: true
  dirs: [~/.antares/skills]
  disabled: []
  auto_create: true
  creation_nudge_interval: 20
```

`dirs` is searched in order and later directories win, so a personal copy can
override a shared one. See [Skills](skills.md).

`disabled` contains exact, case-sensitive names turned off for this profile,
including names whose files are temporarily absent. Dashboard switches update
this list without changing source files. Legacy configured opt-outs are imported
once; see [Managing skills](skills.md#managing-them).

## Server

```yaml
server:
  host: 127.0.0.1
  port: 8787
  auth_token: ""            # empty leaves the dashboard open on loopback
  auth_disabled: false      # explicit opt-out for public unauthenticated binds
  cors_origins: []
  public_url: ""
  trust_proxy: false
```

The default `127.0.0.1` binding leaves the dashboard open, which is right
behind a private network. A non-loopback `host` refuses to start unless one
of these is true: `auth_token` is set, a dashboard password has been
configured, or `auth_disabled: true` is set explicitly — the last is the
documented opt-out for the rare case you really want a public
unauthenticated bind. Set `public_url` when behind a reverse proxy so
generated links are right.

## Dashboard modules

```yaml
display:
  modules: [automation]   # optional dashboard modules shown in the sidebar
  preset: coding          # label of the preset last chosen; display only
```

`modules` lists which optional modules appear in the dashboard sidebar. The
ids are `automation`, `security` and `studio`; an unknown id or a duplicate is
rejected on save. A hidden module is only left out of the sidebar: its pages
still load and the command palette still lists them.

- **Key absent** means every module is on. Installs from before modules
  existed have no key, so upgrading hides nothing, and re-saving the file for
  any other reason keeps the key absent.
- **`modules: []`** is a deliberate choice, the General preset: no optional
  modules.

`preset` is one of `general`, `coding`, `security`, `creator`, `full` or
`custom` (a set that matches no preset). It is only a label; `modules` alone
decides what is shown. Setup writes both keys from the preset picked there, and
Settings changes them through `POST /api/ui/modules`. Neither key appears in
the generic Settings form.

## Scheduling, channels, MCP

```yaml
cron:
  enabled: true
  timezone: Local
  max_concurrent: 2

gateway:
  enabled: false
  telegram:
    enabled: false
    bot_token: ""
    require_pairing: true
  discord:
    enabled: false
    bot_token: ""

mcp:
  enabled: false
  servers: {}
```

See [Scheduling](scheduling.md), [Channels](channels.md), and [MCP](mcp.md).

## Persisted vs. effective configuration

The YAML file is authoritative on disk: every write from the dashboard,
`antares config set`, or a `/config` command normalises the struct and
writes it back through `config.Save`; the raw-editor endpoint validates
then replaces the file via `config.SaveRaw`. What the running process
actually uses is a separate view. `config.Effective(current, desired)`
takes the on-disk configuration and, for every field classified as
`restart_required`, keeps the running value — the desired value stays on
disk and takes effect at the next restart, and the affected paths surface
as a "pending restart" banner on the dashboard.

`config.ReloadMode(path)` is the single classifier:

| Class | Behaviour | Paths |
|---|---|---|
| `restart_required` | Kept from the boot config until the next restart | `database.*`, `logging.*`, `social.*`, `tools.browser.*`, `tools.http.*`, `server.host`, `server.port` |
| `reconciled` | Applied by asking the owning subsystem to rebuild from the new value | `terminal.*`, `cron.*`, `gateway.*`, `mcp.*`, `rag.*`, `plugins.*`, `roles.*`, `skills.dirs` |
| `live` | Read on every use; the write takes effect immediately | everything else (model, agent, memory, compression, prompt caching, most of `tools.*`) |

Reconciled subsystems own long-lived state that cannot be swapped out
mid-request — terminal shell lifetimes, cron schedules, MCP transports,
RAG indexes, plugin/role/skill scanners, gateway connections — so a save
triggers a targeted re-application rather than a process bounce. The
dashboard's Settings page reads each field's `Reload` label from
`config.Schema()` and shows it next to the field, so you can tell before
saving whether the change is live, will be reconciled, or requires a
restart.

## Environment overrides

| Variable | Overrides |
|---|---|
| `ANTARES_HOME` | Where everything lives |
| `ANTARES_CONFIG` | The config file path |
| `ANTARES_MODEL` | `model.default` |
| `ANTARES_PROVIDER` | `model.provider` |
| `ANTARES_API_KEY` | `model.api_key` |
| `ANTARES_HOST` / `ANTARES_PORT` | `server.host` / `server.port` |
| `ANTARES_AUTH_TOKEN` | `server.auth_token` |
| `ANTARES_DB_DRIVER` / `ANTARES_DB_DSN` | `database.*` |
| `DATABASE_URL` | `database.dsn` |
| `ANTARES_LOG_LEVEL` | `logging.level` |
| `ANTARES_WORKSPACE` | `agent.workspace` |
| `ANTARES_RAG_ENABLED` / `ANTARES_RAG_PROVIDER` | `rag.*` |
| `GITHUB_TOKEN` | Raises the hub's GitHub rate limit |

## Checking it

```bash
antares doctor
```

One pass over the config file, workspace, database, provider credentials, and
retrieval backend, reporting what is wrong and what to do about it.
