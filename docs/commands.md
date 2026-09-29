# Slash commands

A command is answered by Antares itself rather than by the model. `/status`
costs nothing and returns instantly; asking the model the same question would
cost a turn and might be wrong.

Every command is defined once, in `internal/commands`, and the terminal
interface, the web chat, and the messaging gateways all dispatch through it. A
command therefore means the same thing wherever it is typed, and adding one is a
single registration rather than three.

## Using them

**In the terminal.** Type `/`. The palette filters as you type; arrow keys move,
Tab completes, Enter runs a command that is already whole.

**In the web chat.** The same, in the composer. Command output appears as its
own block in the transcript, marked so it is never mistaken for the model
talking.

**In Telegram or Discord.** Send the command as a message. A leading slash is
intercepted before the message reaches the model.

## Everything there is

### Getting your bearings

| Command | What it does |
|---|---|
| `/help` | Every command available on this surface |
| `/status` | Model, provider, toolset, database, counts, turns in flight |
| `/version` | Which build this is |
| `/web` | The dashboard address |

### Model and provider

| Command | What it does |
|---|---|
| `/model` | Show the active model |
| `/model <id>` | Switch to another, saved and applied immediately |
| `/models [provider]` | What the provider offers |
| `/provider` | List configured providers and which have keys |
| `/provider <id>` | Switch provider |

### Tools and skills

| Command | What it does |
|---|---|
| `/tools` | The tools active this turn, with the toolset they came from |
| `/toolset [name]` | Show or change which tools the model gets |
| `/skills [query]` | Installed skills |
| `/skills search [words]` | Browse the hub — also takes `owner/repo` or a URL |
| `/skills install <id>` | Install one |
| `/mcp` | Configured MCP servers and their connection state |
| `/mcp search [words]` | Browse the MCP catalogue |
| `/mcp install <id>` | Add one to the configuration |

### Memory

| Command | What it does |
|---|---|
| `/memory` | Recent long-term memories |
| `/memory <query>` | Search them |
| `/remember <text>` | Save something. `key: value` sets an explicit key |
| `/forget <key>` | Delete one |

### The session

| Command | What it does |
|---|---|
| `/new` | Start fresh |
| `/clear` | Clear the transcript |
| `/sessions` | Recent conversations |
| `/resume <id>` | Reopen one — a prefix is enough |
| `/compact` | Start the next turn from a summary of this one |
| `/retry` | Resend the last message |
| `/stop` | Interrupt the turn in flight |
| `/copy` | Copy the last reply |
| `/title [name]` | Show or rename this conversation |
| `/fork [name]` | Copy it, to try another direction without losing this one |
| `/undo` | Remove the last exchange |
| `/export [markdown\|json]` | Write it to a file |

### Long work

| Command | What it does |
|---|---|
| `/goal <text>` | Set a goal to keep working towards across turns |
| `/goal status` | Where the goal stands, and how many iterations it has taken |
| `/goal pause` / `/goal resume` | Hold it, or pick it back up |
| `/goal clear` | Drop it |
| `/steer <instruction>` | Redirect a run that is already going |
| `/learn [focus]` | Turn this session into a reusable skill |
| `/rollback` | List the files this session changed |
| `/rollback all` \| `/rollback <path>` | Put them back |
| `/panel <question>` | Ask several models and synthesise one answer |
| `/team` | How the specialist roles have performed |

See [the harness](harness.md) for what these actually do.

### Roles and specialists

| Command | What it does |
|---|---|
| `/roles` | List the specialist roles |
| `/role [name]` | Run this conversation as a role |
| `/panel <question>` | Ask several models and synthesise one answer |
| `/team` | How the specialist roles have performed |

### Authorized security testing

| Command | What it does |
|---|---|
| `/findings [remove\|clear] [id]` | The current engagement's findings |
| `/report [title]` | Compile the findings into a report |
| `/engagement [intel]` | The assessment's phase progress |

See [roles](roles.md) for what these do.

### Settings and accounting

| Command | What it does |
|---|---|
| `/config <path>` | Read a setting |
| `/config <path> <value>` | Change one |
| `/reasoning [on\|off]` | Show or hide the model's reasoning |
| `/usage [days]` | Tokens and cost, by model |
| `/cost [days]` | The same thing |
| `/setup` | Open the setup wizard |
| `/quit` | Leave the terminal interface |

## Which work where

Most commands work on all three surfaces. The exceptions are the ones a surface
cannot carry out: `/quit` only means something in a terminal, and `/resume` and
`/copy` need a screen. `/help` lists what is available where you are, so it is
always accurate.

## From the shell

The commands that do not need an open conversation also run straight from a
shell, without the slash: `antares usage 30`, `antares roles`, `antares skills`,
`antares memory search editor`, `antares mcp search github`. They print plain
text when piped and rendered markdown on a terminal. Commands tied to a
conversation (`/title`, `/undo`, `/goal`, `/role`, …) and the ones a screen
carries out (`/new`, `/copy`, …) say so instead of running. See
[cli.md](cli.md) for the full list and for the shell-only subcommands such as
`antares ask` and `antares sessions`.

## Adding one

```go
register(Spec{
    Name:     "weather",
    Args:     "<city>",
    Summary:  "Current conditions",
    Surfaces: anywhere,
}, func(ctx context.Context, d Deps, in Input) (Result, error) {
    return Result{Output: "..."}, nil
})
```

`Deps` carries the config, the agent, the store, the skill library, and the MCP
manager. Any of them may be nil — a command that needs one that is missing
should say so rather than panic.

Return `Action` when only the calling surface can finish the job:

```go
return Result{Action: Action{Kind: "resume", Value: sessionID}}, nil
```

The terminal, the browser, and the gateways each translate the actions they
understand and ignore the rest.
