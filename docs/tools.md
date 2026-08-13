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

Mutating `cursor_agent` actions are the exception to `auto`: both `auto` and
`prompt` require an explicit approval before start, follow-up, or cancellation.
`deny` remains deny — it refuses immediately without creating a pending
approval.

## Cursor Cloud Agents

`cursor_agent` delegates coding work to a configured Cursor Cloud Agent. It can
start a run, follow up on an existing agent, or request cancellation. Starting,
following up, and cancelling require explicit approval in `auto` and `prompt`
because they create or change remote work; `deny` refuses them immediately. It
defaults to `wait: true`. With `wait: false`, it returns the agent ID, run ID,
and Cursor URL immediately; use `cursor_agent_status` later instead of
busy-polling.

`cursor_agent_status` is read-only and needs no approval. It defaults to
`wait: false` and returns one snapshot; `wait: true` streams until terminal
status. Cancelling local waiting does not cancel the remote Cursor run. Use the
status tool to inspect an agent/run returned by `cursor_agent`, rather than
repeatedly starting new work. Both tools need the Cursor provider enabled and a
resolved key; it ships disabled, so see [Configuration](configuration.md).

`cursor_agent` also takes an optional `model_params` — the exact parameter array
of one variant from Cursor's catalogue. Omit it and Cursor's own default for
that model applies; supply it and it must match a real variant exactly.

### Direct Cursor mode

The dashboard composer can send a turn straight to a Cursor Cloud Agent instead
of asking an Antares model to manufacture a `cursor_agent` call. Cursor models
appear in the composer's model search under **Cursor Cloud Agents**, alongside
(and clearly separated from) chat models. Picking one changes only where the
next message goes — the active Antares chat model is untouched.

**Exact variants.** Antares uses Cursor's live catalogue as the sole authority.
Selecting a model picks its `isDefault` variant, and the options popover
(reasoning/effort/thinking, Context, Fast, and whatever else that model
publishes) *filters* the concrete variants Cursor returned. A choice commits
only when exactly one real variant matches. Antares never assembles a
combination Cursor did not list, and it copies the whole parameter array —
including parameters Cursor does not show as user-facing, such as
`cyber=false`. A selection that has gone stale triggers one catalogue refresh
and then an actionable reselect error, never a guess.

**Approval.** Every paid or state-changing Cursor operation — start, follow-up,
and cancel — needs an explicit human decision, in `auto` as much as in `prompt`.
No request reaches Cursor before you approve. The card shows the operation,
whether it creates a new agent or follows up, the model and every parameter,
repository and starting ref, mode, auto-create-PR, a bounded prompt preview,
and the attachment count. What executes is the request the server retained, not
the card's contents: editing the composer after the card appears cannot change
what you are approving.

**Stop and Cancel are different.** Local **Stop** detaches your view — it stops
the local stream and tells you the remote run may still be active. It never
cancels remote work, and once the non-idempotent create request is in flight
Stop can only record the detachment. **Cancel** is a separate, approval-gated
action that asks Cursor to stop the run. Closing the tab or losing the network
likewise only detaches; reattaching replays from the live run, and after a
daemon restart Antares recovers from the persisted agent and run IDs.

**Follow-up reuse identity.** A consecutive Cursor turn reuses the same Cursor
agent — so the remote conversation keeps its context — as long as the model,
variant parameters, repository, starting ref, and auto-create-PR setting are
unchanged. Change any of them and the next approved turn starts a *new* agent.
Cursor mode (Agent or Plan) may change per follow-up, because Create Run
accepts a mode override. Starting a new Antares chat, or switching to a chat
model and back, also starts a new agent: the intervening messages were never
part of the remote conversation.

**Attachments.** Cursor mode accepts up to **five images**, each at most
**15 MiB decoded**, as `image/png`, `image/jpeg`, `image/gif`, or `image/webp`.
The declared MIME type must match the file's actual signature. Anything over
the limit or of an unsupported type fails *before* approval, so a rejected
attachment never costs a run. Non-image attachments are refused with an
explanation rather than silently dropped — a local file path means nothing to a
cloud VM that cannot read your disk.

Only one direct Cursor turn runs per session at a time.

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
