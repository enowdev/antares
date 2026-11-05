# Installation

Antares comes two ways, both from the project's **GitHub Releases** with no
build tools required:

- **The command line**: one binary, `antares`, with the dashboard embedded.
  Installed with a one-line script.
- **The desktop app**: a window onto the dashboard, with the `antares` server
  bundled. Downloaded from [antares.enowx.ai](https://antares.enowx.ai/#download).

Both run on **Linux**, **macOS** and **Windows**, on amd64 and arm64.

## What gets installed

One executable, `antares`: the whole CLI, with the dashboard embedded inside
it. After install, every command works from anywhere on your PATH: `antares`
(server in the background), `antares tui`, `antares setup`, `antares doctor`,
and the rest.

| OS | Installed to | PATH |
|---|---|---|
| Linux / macOS | `~/.local/bin/antares` (override with `PREFIX`) | added to your shell rc automatically, unless already present |
| Windows | `%LOCALAPPDATA%\Antares\bin\antares.exe` | added to your user PATH automatically |

Open a new terminal after installing so the PATH change is picked up.

## One-line install

### Linux / macOS

```bash
curl -fsSL https://antares.enowx.ai/install.sh | bash
```

Finds the latest release, downloads the binary for your platform, checks it
against the release's `checksums.txt`, installs it to `~/.local/bin/antares`
and adds that directory to your PATH (in `.bashrc`/`.zshrc`/`.profile`/fish,
once) if it is not already there. Open a **new** terminal afterwards. Knobs:

```bash
# specific version, and a system-wide location
curl -fsSL https://antares.enowx.ai/install.sh | ANTARES_VERSION=v0.6.0 PREFIX=/usr/local bash

# don't touch my shell rc, just tell me the line to add
curl -fsSL https://antares.enowx.ai/install.sh | ANTARES_NO_MODIFY_PATH=1 bash
```

### Windows (PowerShell)

```powershell
irm https://antares.enowx.ai/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Antares\bin\antares.exe` after the same checksum
check and adds it to your user PATH. Open a **new** terminal afterwards. Pin a
version with `$env:ANTARES_VERSION='v0.6.0'` before running.

> Piping a script to your shell runs code from the internet. To read it first,
> open the URL in a browser, or run `scripts/install.sh` / `install.ps1` from
> this repository; the website serves the same files.

## Desktop app

Download it from [antares.enowx.ai](https://antares.enowx.ai/#download), or
straight from the latest release:

| OS | File | Installs to |
|---|---|---|
| macOS, Apple Silicon | `Antares-macos-arm64.dmg` | drag to Applications |
| macOS, Intel | `Antares-macos-x64.dmg` | drag to Applications |
| Windows x64 / ARM | `Antares-windows-x64-setup.exe` / `-arm64-setup.exe` | `%LOCALAPPDATA%\Programs\Antares`, no administrator prompt |
| Linux x64 / ARM (Debian, Ubuntu) | `Antares-linux-x64.deb` / `-arm64.deb` | `/opt/antares`, `sudo apt install ./Antares-linux-x64.deb` |
| Linux x64 / ARM (other) | `Antares-linux-x64.tar.gz` / `-arm64.tar.gz` | unpack anywhere, run `./antares-desktop` |

Each URL is `https://github.com/enowdev/antares/releases/latest/download/<file>`.
On Linux the app needs GTK 3 and WebKitGTK 4.1 (`libwebkit2gtk-4.1-0`); the
.deb pulls them in.

The app is not signed with an Apple or Microsoft certificate yet. On macOS,
open it once with right-click › Open, or allow it in System Settings ›
Privacy & Security › Open Anyway. On Windows, SmartScreen may show "Windows
protected your PC": More info › Run anyway. See [Desktop app](desktop.md).

## First run

```bash
antares setup      # configure a provider (browser or terminal wizard)
antares            # start API + dashboard in the background on http://localhost:8787
antares tui        # open the terminal UI
```

`antares setup` writes `~/.antares/config.yaml` (Windows:
`%USERPROFILE%\.antares\config.yaml`). See [Getting started](getting-started.md)
for what setup asks.

## Upgrading

Re-run the installer: it overwrites the binary in place. Pass
`ANTARES_VERSION` to move to a specific release. For the desktop app, download
and install the new file over the old one; your data in `~/.antares` stays.

## Build from source

You only need this to develop Antares or to run an unreleased commit. It needs
**Go 1.26+** (the Go toolchain auto-fetches the exact version the project pins),
**Bun or npm**, and **git**.

```bash
git clone https://github.com/enowdev/antares.git
cd antares
make install       # toolchain deps (Go modules, Air, dashboard packages)
make build         # → ./bin/antares (dashboard embedded)
make install-cli   # build and install to ~/.local/bin (PREFIX overrides)
```

## Cutting a release (maintainers)

Write the notes to `docs/releases/<tag>.md`, then push a tag:

```bash
git tag -a v0.6.0 -m v0.6.0 && git push origin v0.6.0
```

`.github/workflows/release.yml` builds the CLI for six targets
(`scripts/release-build.sh`), the desktop app for macOS, Windows and Linux,
tests the installers, writes `checksums.txt` and publishes the release with
those notes. Run the workflow by hand (Actions › Release › Run workflow) to
build everything without publishing.

The CLI asset names are what `install.sh`/`install.ps1` expect:
`antares_<version>_<os>_<arch>[.exe]`. The desktop files have no version in
their names, so `releases/latest/download/<file>` always gives the newest.

## Uninstall

```bash
# Linux / macOS
rm ~/.local/bin/antares
rm -rf ~/.antares            # optional: config, sessions, memory

# Windows (PowerShell)
Remove-Item "$env:LOCALAPPDATA\Antares\bin\antares.exe"
Remove-Item -Recurse "$env:USERPROFILE\.antares"   # optional
```

## Troubleshooting

- **download fails** - check that github.com is reachable, or pin a version
  that exists with `ANTARES_VERSION`.
- **`checksum mismatch`** - the download was cut off or altered; run the
  installer again.
- **`antares: command not found` after install** - the install dir is not on
  your PATH. The installer prints the line to add; on Windows, open a new
  terminal.
- **macOS: "cannot be opened because the developer cannot be verified"** - the
  installer clears the quarantine flag; if you moved the binary yourself, run
  `xattr -d com.apple.quarantine <path>`.
- **Windows: "running scripts is disabled"** - allow the current session:
  `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass`, then re-run.

Found a rough edge? This is an early release - please
[open an issue](https://github.com/enowdev/antares/issues) or send a PR.
