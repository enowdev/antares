# Tools

A tool is a function the model can call. Antares registers sixteen, plus
whatever MCP servers add.

## The surface

### Files

| Tool | What it does |
|---|---|
| `read_file` | Read a text file as `NUMBER|CONTENT` lines, with offset and limit for large ones |
| `write_file` | Create or overwrite, making parent directories |
| `edit_file` | Replace an exact string, which must appear exactly once unless told otherwise |
| `list_files` | Directory entries, optionally recursive |
| `glob` | Find files by pattern, newest first |
| `grep` | Regular-expression search returning matching lines with paths and numbers |

Paths are relative to the workspace and cannot escape it.

`edit_file` requiring a unique match is deliberate: an edit that silently hits
the wrong occurrence is worse than one that fails.

`read_file` prints each line as `NUMBER|CONTENT`. The number and `|` are
metadata for the model — they are not part of the file. `edit_file` matches
line endings to the file automatically (so a paste from `read_file` works on
CRLF files) and will strip a whole-block paste of `NUMBER|` prefixes if the
model includes them by mistake. Tabs and spaces must still match exactly.

### Terminal

| Tool | What it does |
|---|---|
| `terminal` | Run a shell command in a persistent session |

The session survives between calls — `cd`, exported variables, and activated
environments persist. Backends are local, Docker, or SSH.

### VPS (saved servers over SSH)

| Tool | What it does |
|---|---|
| `vps_run` | Run a shell command on a dashboard-saved VPS; omit command to list servers |
| `vps_upload` | SFTP upload a local workspace file to the VPS |
| `vps_download` | SFTP download a remote file into the workspace |

Default command timeout is 120 seconds (raise `timeout_seconds` up to 900 for
`systemctl restart` / upgrades). Transfers are capped at 256 MiB. Credentials
stay encrypted at rest; host keys are pinned on first connect (TOFU).

### Web

| Tool | What it does |
|---|---|
| `web_search` | Ranked results with titles, URLs, and snippets |
| `web_fetch` | Fetch a URL as readable text, HTML stripped |
| `browser` | Drive a real Chromium — [its own guide](browser.md) |

`web_fetch` is faster and cheaper. Reach for `browser` when the page needs
JavaScript, a login, or a form.

### Memory and retrieval

| Tool | What it does |
|---|---|
| `memory` | Save, search, list, and delete durable facts |
| `session_search` | Full-text search across past conversations |
| `rag_search` | Semantic search over indexed documents |
| `rag_index` | Add a file or directory to the index |

See [Memory and RAG](memory-and-rag.md).

### Working

| Tool | What it does |
|---|---|
| `todo` | A task list for the session, so progress stays visible |
| `skill` | List and read the skill library, and write new entries |
| `delegate_task` | Hand a self-contained subtask to an isolated sub-agent |

A sub-agent cannot see the conversation it came from, so its prompt has to stand
alone. It returns only its final answer, which is the point: research that would
flood the main context happens elsewhere.

## Toolsets

A toolset is a named group. Give the model fewer tools and it chooses better;
give it none it should not have and a whole class of mistakes cannot happen.

| Toolset | Contents |
|---|---|
| `minimal` | read_file, list_files, grep, todo |
| `coding` | files, terminal, todo, skill, delegate_task |
| `research` | read_file, web_search, web_fetch, browser, grep, todo, memory, session_search, rag_search, skill |
| `browser` | browser, web_search, web_fetch, read_file, write_file, todo |
| `default` | everything above, combined |
| `all` | every registered tool, including MCP |

```bash
antares config set tools.toolset research
```

Or per conversation, without saving:

```
/toolset coding
```

Adjust a toolset rather than replacing it:

```yaml
tools:
  toolset: coding
  enabled: [web_search]      # add
  disabled: [terminal]       # remove
```

`disabled` wins over `enabled`.

## Per-platform toolsets

A Telegram thread rarely wants a shell:

```yaml
tools:
  platform_toolsets:
    telegram: research
    discord: research
```

## Approval

```yaml
tools:
  approval_mode: auto      # auto | prompt | deny
```

- `auto` — tools run.
- `prompt` — tools that mutate state ask first.
- `deny` — mutating tools are refused; reading still works.

Tools declare whether they mutate. `read_file` does not; `write_file`,
`terminal`, and `browser` do.

## Limits

```yaml
tools:
  max_output_chars: 30000
  timeouts:
    terminal: 120
    web_fetch: 60

tool_loop_guardrails:
  warn_after: 24
  hard_stop_after: 32
```

Output past the limit is truncated with a note saying how much was cut. The
guardrails bound how many calls one turn may make; past the hard stop the model
is told to summarise and finish. The [repetition guard](harness.md) is separate
and catches a different failure — the same call over and over.

## MCP tools

Tools from MCP servers are namespaced `mcp__<server>__<tool>` and appear
alongside the built-ins. A server that fails to start is reported and skipped;
it never stops Antares from running. See [MCP](mcp.md).

## Seeing what is active

```
/tools
```

Lists what the model has this turn and which toolset it came from. The dashboard
has the same on its Tools page, with per-tool switches.

## Writing one

```go
type weatherTool struct{}

func (weatherTool) Name() string { return "weather" }

func (weatherTool) Description() string {
    return "Current conditions for a city. Use when asked about weather."
}

func (weatherTool) Schema() map[string]any {
    return schema(map[string]any{
        "city": prop("string", "City name."),
    }, "city")
}

func (weatherTool) Execute(ctx context.Context, in Input) Result {
    var args struct{ City string `json:"city"` }
    if err := in.Bind(&args); err != nil {
        return Errorf("%v", err)
    }
    return Text("…")
}
```

Register it in `internal/tools/register.go` and add it to whichever toolsets
should have it.

The description is prompt text, not documentation — it is how the model decides
whether to reach for the tool. Say what it does and when to use it, and mention
the tool it should be preferred over if there is one.

Add `RequiresApproval() bool` for a tool that mutates state.
