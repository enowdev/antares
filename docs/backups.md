# Backups

Everything Antares keeps lives in one directory, so a backup is a tarball of
that directory. The reason it is a command rather than advice to run `tar` is
that a SQLite file copied mid-write restores into a corrupt database — the
backup settles it first.

```bash
antares backup                    # write one
antares backup list               # what exists
antares backup restore <file>     # put one back
antares backup prune 5            # keep the newest five
```

Archives land in `~/.antares/backups`.

## What is in one

Kept: the configuration, the database, skills, plugins, and the `.env`.

Skipped: temporary files, the browser profile, screenshots, exports,
checkpoints, previous backups, and the SQLite sidecar files, which are
meaningless without the moment they belong to.

**The database is included wherever it lives.** A DSN pointing at another
volume is common, and a backup that quietly omitted the database would be worse
than none — it would look like one. Files from outside the state directory are
archived under a marked prefix and restored to the path they came from.

On Postgres the database is not in the archive at all, and the command says so.
`pg_dump` is the tool for that; everything else still gets backed up.

## Restoring

```bash
antares backup restore antares-2026-01-15-093000.tar.gz
```

A bare filename is looked for in the backups directory; anything with a path
separator is taken as given.

Restoring refuses to overwrite a directory that already holds a database, and
refuses to overwrite a database outside it. Stop Antares and pass `--force` when
you mean it — restoring onto a running instance would replace the state it is
using.

```bash
antares backup restore <file> --force
```

## Scheduling one

A cron job is the simplest way to keep them current:

```
0 3 * * *   /opt/antares/antares backup && /opt/antares/antares backup prune 7
```

Or from inside Antares, on the Automation page:

> Every night at 3am, run `antares backup` and then `antares backup prune 7`,
> and tell me only if either fails.

## Moving to another machine

```bash
# on the old one
antares backup

# on the new one
export ANTARES_HOME=/var/lib/antares
antares backup restore /path/to/antares-….tar.gz
antares
```

Sessions, memory, skills, schedules, and settings all come across. Check the
database DSN afterwards if it was an absolute path — the file is restored where
it came from, which may not exist on the new machine.
