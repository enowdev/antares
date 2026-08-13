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
  reasoning_effort: ""     # Auto — see Reasoning below

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

### Cursor Cloud Agents

Cursor is an agent integration, not a primary Antares model provider. Configure
the deployment-owned key through the environment:

```yaml
providers:
  cursor:
    kind: cursor-agent
    base_url: https://api.cursor.com
    api_key_env: CURSOR_API_KEY
    enabled: false
    timeout_seconds: 900
```

Cursor is a built-in cloud-only agent integration. Existing configuration and
providers need no migration and retain their current LLM behavior. It ships
**disabled**, like every other provider you must bring a key for: Cursor runs
cost money, so having `CURSOR_API_KEY` in the environment must not by itself be
enough to spend it. Turn it on from the Providers page, or set `enabled: true`.
`CURSOR_API_KEY` then supplies the key without writing one to YAML. One
deployment key and its quota are shared by every user who can reach it.

Cursor never becomes an active chat provider. `model.provider`,
`model.default`, `/api/model/set`, the `/model` and `/provider` commands, the
TUI picker, and `llm.New` all refuse it — it is reachable only through the
Cursor tools and the composer's direct Cursor target.

**Repository state.** A repository-backed run uses what Cursor's cloud VM can
fetch from the remote, so **uncommitted changes and local-only commits are not
part of the run**. Antares reads your project's `origin`, normalises SSH forms
like `git@github.com:owner/repo.git` to `https://github.com/owner/repo`,
proposes the current branch as the starting ref, and warns on the approval card
when the worktree is dirty or ahead of that ref. You can edit the repository and
ref before sending; the server normalises and validates them again. Local
paths, credentials embedded in remote URLs, non-GitHub repositories, and
non-HTTPS destinations are rejected. A chat with no project bound runs with no
repository.

See [Tools](tools.md) for direct Cursor mode, approval, and attachment limits.

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
CURSOR_API_KEY=…
```

## Reasoning

```yaml
model:
  reasoning_effort: ""      # Auto
agent:
  reasoning_effort: ""      # Auto
```

Reasoning is **model-aware**. There is no global `none|low|medium|high` ladder
any more: Antares asks the selected provider and model what it actually
supports and offers exactly those values.

**Auto** is the default and is always available. Auto sends *no* reasoning
field at all, so the model applies its own default — which for the adaptive
families is better than any fixed value Antares could pick. It is also the only
safe default for OpenRouter, where one model name can route to backends with
completely different ladders.

The values themselves are **opaque provider strings**, passed through
unchanged. `extra-high` is not rewritten to `xhigh`, and two providers that
happen to share a label do not necessarily share a wire format.

| Provider | Semantics |
|---|---|
| Anthropic | Adaptive-thinking models send `thinking: {type: adaptive}` with `output_config.effort`; legacy models keep fixed token budgets |
| Gemini | Published `thinkingLevel` values. `minimal` means Minimal, not Off — models that cannot disable dynamic thinking show no Off |
| OpenAI / Codex | Documented effort ladder per family; chat requests use the effort field, Responses/Codex the nested reasoning object |
| OpenRouter | `supported_efforts`, `default_effort`, `mandatory` from model metadata are authoritative; a chosen Off sends an explicit disable rather than omitting the field |
| Other OpenAI-compatible | Auto only, unless the provider publishes reasoning metadata |

**Off** appears only where reasoning can genuinely be disabled. A model with
mandatory reasoning offers no Off, and a provider that merely lowers its budget
does not get to call it Off.

The dashboard stores your choice per `provider/model`, so switching models
restores that model's own last valid value rather than carrying a stale one
across. A value the newly selected model does not support falls back to Auto.

`reasoning_effort` stays a string, so existing YAML and automation keep working.
An unsupported value in a **stored config** resolves to Auto with a one-time
notice; an unsupported value in a **new API request** is rejected outright
rather than silently rewritten.

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
  toolset: default          # minimal | coding | research | browser | default | all
  enabled: []               # add to the toolset
  disabled: []              # remove from it
  approval_mode: auto       # auto | prompt | deny
  max_output_chars: 30000
  timeouts:
    terminal: 120
```

See [Tools](tools.md) for what is in each toolset.

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
  auto_create: true
  creation_nudge_interval: 20
```

`dirs` is searched in order and later directories win, so a personal copy can
override a shared one. See [Skills](skills.md).

## Server

```yaml
server:
  host: 0.0.0.0
  port: 8787
  auth_token: ""            # empty leaves the dashboard open
  cors_origins: []
  public_url: ""
  trust_proxy: false
```

Set `auth_token` for anything reachable beyond a private network. Set
`public_url` when behind a reverse proxy so generated links are right.

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
