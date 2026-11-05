# Skills

A skill is a written procedure the agent follows: a Markdown file with YAML
front matter. Not code, not a plugin — instructions.

The point is that the agent gets better at *your* work. Solve something
non-obvious once, keep the procedure, and the next time it takes minutes.

## The shape

```markdown
---
name: deploy-homeserver
description: Deploy this project to the home server. Use when asked to deploy, ship, or release.
tags: [deployment, ops]
triggers: [deploy, ship, release]
---

# Deploying to the home server

1. Run the tests. Do not deploy on red.
2. `make build` — the binary lands in `bin/`.
3. `rsync bin/antares homeserver:/opt/antares/`
4. `ssh homeserver systemctl restart antares`
5. Check `curl homeserver:8787/api/health` before saying it worked.

## When it goes wrong

Port 8787 already in use usually means the old process did not exit. Check
`systemctl status antares` before restarting again.
```

| Field | Meaning |
|---|---|
| `name` | Unique, kebab-case. How it is referred to |
| `description` | **The most important line.** How the agent decides whether this is relevant |
| `tags` | For your own browsing |
| `triggers` | Words that make it more likely to surface |

The description does the work. "Deployment stuff" will not get picked; "Deploy
this project to the home server. Use when asked to deploy, ship, or release."
will.

## How they reach the model

Only names and descriptions go into the system prompt — the catalogue. Bodies
are fetched on demand with the `skill` tool.


Disabled names are omitted from new prompts and from the skill tool's list,
search, read, and chain results. Re-enabling restores access. Already-sent model
context cannot be retracted, and this preference does not restrict generic
filesystem tools. Saving skill content does not enable a disabled name.
Twenty skills therefore cost a few hundred tokens per turn rather than tens of
thousands, and adding more does not degrade the conversation.

## Where they live

```yaml
skills:
  enabled: true
  dirs: [~/.antares/skills]
```

Configured `dirs` remain writable; new skills save to the first nonblank directory.
Native `~/.antares` paths follow `ANTARES_HOME`. Flat `.md` files still work there.

Antares also discovers these directories automatically, in the order shown:

| Under the OS user home | Under the selected project |
|---|---|
| `.agent/skills` | `.agent/skills` |
| `.agents/skills` | `.agents/skills` |
| `.claude/skills` | `.claude/skills` |
| `.codex/skills` | `.codex/skills` |
| `.config/opencode/skills` | `.opencode/skills` |
| `.omp/agent/managed-skills` | `.github/skills` |

Automatic roots use the OS home independently of `ANTARES_HOME`; the OpenCode
home path does not follow `XDG_CONFIG_HOME`. Project roots are beneath the chat's
persisted project folder, without searching parent directories. Resumed chats keep
that binding. The dashboard and chats without a project use the startup directory;
relative project paths resolve against that startup directory.

Automatic sources accept only `SKILL.md` (case-insensitive), recursively. Supporting
Markdown and hidden descendants are ignored. A missing name uses the logical parent
folder name. Symlinks are followed with cycle detection; missing roots are not created.

For duplicate names, priority from lowest to highest is bundled security pack,
automatic user roots, automatic project roots, then configured `dirs`. Later roots
within each group win; files within a root are visited in lexical order.

Automatically discovered skill content is read-only through skill management:
save and delete refuse to modify or shadow it. The toggle API changes only Antares
configuration, including for these borrowed skills. Edit the original file to
change its content.
An explicitly configured copy wins and remains writable, including when its directory
is also an automatic root. Hub installs and `/learn` still write an Antares copy
to their configured/native destination.

The running manager rescans every five seconds, including when skills are disabled
for the agent. Additions, normal edits, removals, and symlink retargets are visible
on the next scan without restarting. Edits preserving file identity, size, and
mtime are reparsed every twelve ticks (about one minute). A new prompt uses the
current catalog; a prompt already sent to a model is not rewritten.

## Getting them

**Bundled.** Eight are written on first run. See [the hub](hub.md).

**From the hub.**

```
/skills search debugging
/skills install owner/repo
```

**Written by you.** Drop a file in the skills directory, or use the Skills page.

**Written by the agent.** With `skills.auto_create` on, it writes one after
solving something non-obvious.

**Learned from a session.**

```
/learn
/learn the deploy sequence
```

The transcript goes to a model with one instruction: write the procedure someone
would want next time, with the commands, paths, and gotchas that actually came
up, and skip everything particular to this conversation. If nothing general was
learned it says so and writes nothing.

## Managing them

```
/skills                 what is installed
/skills deploy          filter
```

`/skills` uses the current session's project catalog, including for hub-installed
checks. A project session sees shared user/configured skills and its own project
skills, not the startup project's or another chat project's procedures.

The dashboard's Skills page shows the startup catalog and polls every five seconds
while visible. Imported cards show a Read-only content badge and open a viewer
with the source path and procedure. Their switches update Antares configuration;
editing/deletion controls are omitted. Close and reopen the viewer to read a
refreshed body. Polling does
not replace an unsaved draft in a writable skill editor. Browse opens the hub.

Switches save exact, case-sensitive skill names in `skills.disabled` in the active
profile's configuration; they never rewrite skill files. Preferences remain when
a file is removed or reinstalled. `skills.enabled` is the separate global gate.

On the first startup with this setting, Antares imports `enabled: false` from
selected files in configured skill directories. It records completion in
`skills.frontmatter_migrated`. Later header changes do not affect enablement.
An unreadable or malformed configured source aborts that initial import; repair
the source and restart to retry. The dashboard and `/skills` still show off entries.

```yaml
skills:
  auto_create: true
  creation_nudge_interval: 20   # turns between suggestions
```

## Writing a good one

**Be specific.** The actual command, the actual path, the actual flag. A skill
that says "build the project" is worth nothing; one that says `make build -j2`
because `-j` alone runs the machine out of memory is worth a lot.

**Write the failure modes.** What went wrong the first time, and what it meant.
That is the part that is expensive to rediscover.

**One procedure per skill.** "Deploy" and "roll back" are two skills. A skill
that covers four unrelated things will surface for the wrong one.

**Say when to use it.** The description is a matching problem. Include the words
someone would actually use.

**Leave out this-time details.** No session ids, no timestamps, no "the user
asked me to". A skill is for next time.

## Safety

Skills installed from the hub are scanned first. A skill is prompt text the
model follows, so a file telling it to pipe a download into a shell or read
credentials and post them somewhere is the whole attack — there is no sandbox to
catch it later.

Files you write yourself are not scanned. You wrote them.

See [the hub](hub.md) for what is refused and why.
