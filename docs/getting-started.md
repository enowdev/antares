# Getting started

Antares is one process. It holds the agent, the HTTP API, the dashboard, the
terminal interface, the scheduler, and the messaging bots. There is nothing else
to install and nothing to orchestrate.

## Requirements

- **Go 1.26 or newer** to build from source.
- **Node or Bun** to build the dashboard. Bun is what the Makefile reaches for
  first; npm works.
- **A model provider.** Any OpenAI-compatible endpoint, or a native adapter for
  Anthropic, OpenAI, or Gemini. A local runtime like Ollama or LM Studio works
  too, with no key at all.
- **Chrome or Chromium** if you want the browser tool. Everything else works
  without it.

## Build and run

```bash
git clone https://github.com/enowdev/antares.git
cd antares
make install     # Go modules, Air, dashboard packages
make build       # one binary with the dashboard inside it
./bin/antares
```

The first run has nothing configured, so it drops straight into setup:

```
    1  Browser   — a guided page in the dashboard
    2  Terminal  — a few questions right here
```

Both write the same `~/.antares/config.yaml`. Pick whichever is in front of you.

On a machine with no display, the browser option prints every address the setup
page can be reached on, so you can finish from a laptop on the same network.

## What setup asks

**Which provider.** Pick one and paste a key. Antares checks the key against the
provider before saving it, so a typo fails there rather than mid-conversation.
A local runtime needs no key — just confirm the address.

**Which model.** The list comes from the provider you just connected. Aggregators
publish hundreds; the picker is searchable.

**Where to keep data.** SQLite by default, in `~/.antares/antares.db`. Postgres
if you would rather. Both schemas are created on first run.

**How you want to talk to it.** The terminal interface, the web dashboard, or
both. This only sets the default; either is available at any time.

## First conversation

```bash
./bin/antares
```

That starts the API and dashboard in the background. Open
<http://localhost:8787>. The chat there is the same agent over the same
sessions and the same memory as the terminal UI.

For the terminal interface:

```bash
./bin/antares tui
```

Type a message and press Enter. `Alt+Enter` makes a new line, `Ctrl+C`
interrupts a turn and quits on a second press. Type `/` for the command
palette — `/help` lists everything, `/status` shows what is configured,
`/model` changes the model without leaving the conversation.

## Reaching it from another machine

Antares binds `0.0.0.0` by default, so a private-network address works with no
further setup:

```
http://<host-or-tailscale-ip>:8787
```

The dashboard is left open while `server.auth_token` is empty, which is the right
default behind a private network and the wrong one anywhere else. To require a
token:

```bash
antares config set server.auth_token "$(openssl rand -hex 24)"
```

Clients then send `Authorization: Bearer <token>`. The dashboard prompts for it
and remembers it.

Before a bearer token exists, the setup wizard's mutating requests and the first
dashboard-password operation accept only loopback connections. Use the local
dashboard, an SSH tunnel, or configure the bearer token from the CLI before
bootstrapping remotely.

## Where things live

```
~/.antares/
  config.yaml       every setting
  .env              secrets, loaded automatically
  antares.db        sessions, messages, memory, vectors, schedules
  skills/           the skill library
  browser/          the browser profile, so logins survive a restart
  screenshots/      captures the browser tool wrote
  logs/             if logging.file is set
```

`ANTARES_HOME` moves all of it somewhere else, which is how you run two
independent instances on one machine.

## Next

- [Configuration](configuration.md) — every setting and where it can be set.
- [Tools](tools.md) — what the agent can actually do.
- [Commands](commands.md) — the slash commands.
