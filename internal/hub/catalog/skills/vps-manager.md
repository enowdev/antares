---
name: vps-manager
description: Monitor and manage the user's saved VPS servers over SSH. Use when asked to check, inspect, or operate on a server.
tags: [ops, vps, ssh, sysadmin]
triggers: [vps, server, ssh, systemctl, restart service, check server, disk full, deploy]
---

# VPS manager

The user has saved VPS servers on the dashboard's VPS page. You reach them with:

| Tool | Purpose |
|---|---|
| **`vps_run`** | Run a shell command over SSH; returns stdout+stderr |
| **`vps_upload`** | Copy a local workspace file → remote path (SFTP) |
| **`vps_download`** | Copy a remote file → local workspace path (SFTP) |

There is no agent on the box — just standard SSH/SFTP.

## Pick the server first

Call `vps_run` with **no command** to list the saved servers (id, label,
user@host). Then pass `vps=<id or label>` on every call. If the user named a
server, match it to a label; if there is only one, use it.

## Timeouts

Default command timeout is **120 seconds**. `systemctl restart` / `stop` and
package upgrades often need more — pass `timeout_seconds` (up to 900). On
timeout, raise it and prefer non-interactive flags (`--no-pager`, `-y`).

## Look before you touch

Start read-only to understand the box, then act. Useful reads:

- **Health:** `uptime`, `free -h`, `df -h`, `top -bn1 | head -20`
- **Services:** `systemctl --failed`, `systemctl status <name>`, `systemctl list-units --type=service --state=running`
- **Logs:** `journalctl -u <service> -n 100 --no-pager`, `tail -n 100 /var/log/<file>`
- **Containers:** `docker ps -a`, `docker logs --tail 100 <name>`, `docker stats --no-stream`
- **Network/ports:** `ss -tulpn`, `ip -br a`
- **What's eating disk:** `du -sh /var/* 2>/dev/null | sort -rh | head`, `journalctl --disk-usage`

The dashboard's VPS page already shows CPU/RAM/disk/uptime/top-processes for a
glance — reach for `vps_run` when you need something specific or need to change
something.

## Managing

Once you know the state, operate deliberately:

- **Restart a service:** `systemctl restart <name>` (consider `timeout_seconds`
  ≥ 180) then confirm with `systemctl status <name> --no-pager`.
- **Free disk:** clear old logs (`journalctl --vacuum-time=7d`), package caches
  (`apt-get clean` / `dnf clean all`), then re-check `df -h`.
- **Update packages:** `apt-get update && apt-get -y upgrade` (Debian/Ubuntu) or
  `dnf -y upgrade` (RHEL family) with a higher timeout. Say what changed.
- **Deploy / app ops:** upload artifacts with `vps_upload`, or pull on the box;
  then build/restart. Follow the user's stated workflow.
- **Fetch logs/configs:** `vps_download` for a single file; use `vps_run` +
  `journalctl` for live service logs.

## Rules

- **Only servers the user owns.** These are their machines, added on purpose.
- **Files go through SFTP tools.** For copy to/from a saved host, use
  `vps_upload` / `vps_download` — never `rsync`, `scp`, or interactive `sftp` in
  the terminal for those hosts (credentials and host-key pinning live in the
  tools). Use `vps_run` only for remote shell work, or if the user explicitly
  wants a remote-side pull of a huge tree.
- **Read before write.** Never restart, delete, or upgrade without first showing
  what you found and, for anything risky, saying what you're about to do.
- **Destructive commands need care.** `rm -rf`, `mkfs`, `dd`, dropping a
  database, `systemctl stop` on something critical — confirm the target and
  prefer the reversible option. `vps_run` asks for approval before each command;
  do not try to batch around that.
- **One thing at a time.** Run a command, read its output, decide the next —
  don't fire a chain of mutations blind.
- **Report honestly.** Show the actual output. If a command failed, say so and
  what the error was.
