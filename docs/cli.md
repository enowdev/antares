# Command line

Everything the dashboard does day to day can also be done from a shell, so
Antares can sit inside scripts, cron jobs, git hooks, and editor integrations.
`antares help` prints the full list; this page explains each group.

Output conventions:

- Results go to **stdout**; progress, notices, and the session id go to
  **stderr**. `antares ask "…" > reply.md` captures only the reply.
- Piped output is **plain text**: no colour, no markdown punctuation, tables
  re-aligned as columns. On a terminal the same output is rendered with colour.
  Set `NO_COLOR=1` to force plain text on a terminal.
- A failure exits **non-zero** with the reason on stderr.
- Session ids can be shortened to any unambiguous prefix (`ses_29d2`).
- `--json` is available where a script would want structured data:
  `sessions list`, `sessions show`, `sessions export`, `skills list`,
  `memory list`.

## One-shot turns: `antares ask`

```sh
antares ask "what does this error mean: EADDRINUSE"
git diff | antares ask "review this change"
antares ask --attach report.pdf "summarise the risks"
antares ask --session ses_29d2 "and what should I do first?"
```

```
antares ask [--session <id>|--new] [--role r] [--model m] [--effort e]
            [--project dir] [--attach path]... [-q] ["prompt"]
```

| Option | What it does |
|---|---|
| `--session <id>`, `-s` | Continue an existing conversation |
| `--new`, `-n` | Start a new one (the default; for readability in scripts) |
| `--role <name>`, `-r` | Run as a specialist role (`antares roles` lists them); remembered on the session |
| `--model <id>`, `-m` | Use another model for this turn |
| `--effort <level>`, `-e` | Reasoning effort for this turn (`low`, `medium`, `high`, … as the model supports) |
| `--project <dir>`, `-p` | Make a new session a project session bound to that folder |
| `--attach <path>`, `-a` | Attach a file; repeat for several. Images go to the model directly; other files are copied to the uploads folder for the agent to read |
| `--quiet`, `-q` | Hide tool activity and notices on stderr |

The prompt is the rest of the arguments. If stdin is a pipe or a file it is
read too and appended after the argument prompt, so both
`echo hi | antares ask` and `cat notes | antares ask "tidy these"` work. When a
script runs `antares ask` with a stdin it never closes, redirect it:
`antares ask "…" </dev/null`.

The reply streams to stdout as it arrives. Each tool call is shown on stderr
(`→ read_file {...}`), and when the turn ends the session id is printed to
stderr (`session: ses_…`) so the next call can continue it with `--session`.
The command exits non-zero if the turn fails, and with `interrupted` on Ctrl-C.

`ask` runs the full agent in-process — the same tools, skills, roles, and MCP
servers as the server — and does not need the background server running.

### Nobody is there to answer

A one-shot run has no one to click *Approve* or answer a question, so it never
waits for one:

- **Approvals.** With `tools.approval_mode: prompt` (the default), a tool call
  that needs approval is **refused immediately** — the agent is told the call
  was refused and continues without it, and stderr says what was refused. A
  refused action never runs. To let `ask` change things unattended, choose that
  deliberately: `antares config set tools.approval_mode auto` (this also
  affects the server and the TUI).
- **Questions** (`ask_user`). The agent is answered that no reply is possible,
  told not to act on anything that depended on the answer or assume a default
  for it, and to finish what it can and say what it still needs. stderr notes
  that a question was asked. Answering rather than aborting keeps the useful
  part of the turn; the wording, plus refused approvals, keeps it from doing
  something the question was meant to check.

## Sessions

```
antares sessions [list] [--limit N] [--json]
antares sessions show <id> [--json]
antares sessions rename <id> <title>
antares sessions delete <id>... [--yes]
antares sessions export <id> [--md|--json] [-o file]
```

`list` shows the most recent 20 conversations (sub-agent and background
sessions are left out, as in the dashboard). `show` prints the transcript;
`export` writes markdown (the default) or the full JSON record to stdout, or to
a file with `-o`. `delete` asks for confirmation on a terminal and refuses to
run without `--yes` when there is no terminal to ask on.

## Skills

```
antares skills [list] [--json [--all]]    installed skills
antares skills show <name>                a skill's instructions
antares skills enable <name>              turn one on
antares skills disable <name>             turn one off
antares skills search [words]             search the hub
antares skills install <id>               install from the hub
antares skills <filter>                   installed skills matching a word
```

Listings show the everyday catalogue; a filter (or `--json --all`) also looks
through the bundled security library.

## Memory

```
antares memory [list] [--json]    recent memories
antares memory search <query>     search them
antares memory add <text>         save one; "key: text" gives it a key
antares memory forget <key|id>    delete one
```

`antares remember <text>` and `antares forget <key>` are the same as `memory
add` and `memory forget`.

## Identity: `antares soul`

```
antares soul            print SOUL.md, the agent's identity
antares soul edit       open it in $VISUAL or $EDITOR (vi if neither is set)
antares soul set <file> replace it from a file (- reads stdin)
antares soul reset      go back to the default, which re-runs the first-conversation interview
antares soul path       where the file lives
```

This is the same file the dashboard's Persona page (Agent › Persona, Soul tab)
edits. It is read fresh for every turn, so a change applies to the next
message, even on a running server.

### Global instructions and facts about you: `AGENTS.md` and `USER.md`

Two more files sit beside `SOUL.md` in the Antares home (`$ANTARES_HOME`,
`~/.antares` by default):

- `AGENTS.md` holds standing instructions the agent follows in every
  conversation ("always answer in Indonesian", "never push without asking").
  It goes into the system prompt as `## Your instructions`, right after the
  soul.
- `USER.md` holds what the agent knows about you: name, role, time zone,
  preferences. It goes in as `## About the user`.

Both start absent, and an absent or empty file adds nothing to the prompt.
They apply to every session, channel gateways included, but not to delegated
sub-agents and background tasks, which get a narrowed prompt from their
parent. Each is capped at 16 KB in the prompt; anything longer is cut with a
note. In a project session the project's own `AGENTS.md`/`CLAUDE.md` are added
after these and win where they conflict.

Edit them with any editor, or on the dashboard's Persona page (the
Instructions and About you tabs). Saving a tab empty removes the file. Like
`SOUL.md`, they are read for every turn, so changes apply to the next message.
Over HTTP, `GET`/`POST /api/persona/{soul|agents|user}` read and write them as
`{content, path}`; `/api/soul` remains as the older alias for the soul.

## Server log: `antares logs`

```
antares logs            the last 50 lines of the background server's log
antares logs -n 200     the last 200
antares logs -f         keep following new lines, like tail -f (Ctrl-C stops)
antares logs --path     print where the log is
```

The log is `~/.antares/logs/daemon.log` (under `ANTARES_HOME` if set). It is
written by the background server started with `antares`; a server run with
`--foreground` logs to its terminal instead.

## Commands shared with the chat

These run the same code as the slash command of the same name (see
[commands.md](commands.md)):

| Command | What it does |
|---|---|
| `antares usage [days]`, `antares cost [days]` | Tokens and cost by model |
| `antares models [provider]` | Models the provider offers |
| `antares toolset [name]` | Show or switch the toolset |
| `antares reasoning [on\|off]` | Toggle reasoning display |
| `antares tools` | Tools available to the agent |
| `antares mcp` | MCP server status |
| `antares mcp search [words]`, `antares mcp install <id>` | The MCP catalogue |
| `antares roles` | The specialist roles |
| `antares team` | How the roles have performed |
| `antares panel <question>` | Ask several models and synthesise one answer |
| `antares web` | The dashboard address |

`antares tools` and `antares mcp` (status) connect the configured MCP servers to
report on them, so they start those processes; every other command here works
from the configuration and the database alone.

Slash commands that act on an open conversation — `/title`, `/undo`, `/fork`,
`/export`, `/goal`, `/steer`, `/role`, `/compact`, `/rollback`, `/learn` — and the
ones a screen carries out (`/new`, `/clear`, `/copy`, `/resume`, …) are not
available here; `antares <name>` says so. Use `antares sessions` for renaming
and exporting. Dedicated subcommands (`config`, `model`, `provider`, `status`,
`version`, `help`) keep their own meaning.

## Devices

```
antares device pair [--name N] [--platform P] [--json]
antares device list [--json]
antares device revoke <id>
```

`pair` creates a device token straight in the local database, so it works
whether or not the server is running; having access to `~/.antares` is the
proof. The name defaults to the host name and the platform to `cli`. The token
is printed once. `--json` prints
`{"device": {...}, "token": "atd_…", "url": "http://127.0.0.1:8787"}`, where
`url` is the running server's address, or the configured `server.host:port`
when it is not running. The desktop app pairs its local connection this way.

`revoke` keeps the row (shown as revoked in the list and in Settings ›
Devices); a running server stops accepting the token within 30 seconds.

## Changes and a running server

`antares config set`, `toolset`, `reasoning`, `skills enable|disable`,
`mcp install`, and the like write `config.yaml`. A background server that is
already running keeps its loaded configuration until it restarts; when one is
running, these commands remind you:

```
note: the running server (pid 4242) picks this up on restart: antares stop && antares
```

Sessions, memories, `SOUL.md`, `AGENTS.md` and `USER.md` are read live and need no restart.
