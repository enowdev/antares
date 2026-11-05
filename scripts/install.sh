#!/usr/bin/env bash
# Antares installer for Linux and macOS.
#
# Downloads the prebuilt `antares` binary for your platform from the project's
# GitHub Releases, checks it against the release's checksums.txt and installs
# it. No build tools required.
#
# Usage:
#   curl -fsSL https://antares.enowx.ai/install.sh | bash
#
# Env knobs:
#   PREFIX=/usr/local       install dir root (binary lands in $PREFIX/bin); default ~/.local
#   ANTARES_VERSION=v0.6.0  install a specific release; default: latest
#   ANTARES_REPO=owner/name GitHub repo; default enowdev/antares
#   ANTARES_NO_MODIFY_PATH=1  never edit shell rc files, just print the line

set -euo pipefail

REPO="${ANTARES_REPO:-enowdev/antares}"
VERSION="${ANTARES_VERSION:-latest}"
PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# ---- detect platform --------------------------------------------------------
os="$(uname -s)"; arch="$(uname -m)"
case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) die "unsupported OS '$os' - on Windows run: irm https://antares.enowx.ai/install.ps1 | iex" ;;
esac
case "$arch" in
  x86_64|amd64)  goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) die "unsupported architecture '$arch'" ;;
esac
info "platform: $goos/$goarch"

# The asset name matches scripts/release-build.sh output.
asset_for() { echo "antares_${1}_${goos}_${goarch}"; }

mkdir -p "$BINDIR"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
dest="$tmp/antares"

# ---- fetch ------------------------------------------------------------------
have curl || die "need curl to download."
ver="$VERSION"
if [ "$ver" = "latest" ]; then
  # /releases/latest redirects to /releases/tag/<tag>; no API, no rate limit.
  ver="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")" \
    || die "could not reach github.com to find the latest release."
  ver="${ver##*/}"
  case "$ver" in v*) ;; *) die "could not find the latest release of $REPO." ;; esac
fi
base="https://github.com/$REPO/releases/download/$ver"
asset="$(asset_for "$ver")"
info "downloading $asset ($ver)"
curl -fSL --progress-bar "$base/$asset" -o "$dest" \
  || die "download failed: $base/$asset"

[ -s "$dest" ] || die "downloaded file is empty."

# ---- verify -----------------------------------------------------------------
if curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
  want="$(awk -v a="$asset" '$2 == a || $2 == "*"a { print $1 }' "$tmp/checksums.txt")"
  if have sha256sum; then got="$(sha256sum "$dest" | awk '{print $1}')"
  elif have shasum; then got="$(shasum -a 256 "$dest" | awk '{print $1}')"
  else got=""; fi
  if [ -z "$want" ] || [ -z "$got" ]; then
    warn "could not verify the checksum; continuing."
  elif [ "$want" != "$got" ]; then
    die "checksum mismatch for $asset (expected $want, got $got)."
  else
    info "checksum ok"
  fi
else
  warn "no checksums.txt in $ver; skipping verification."
fi

# ---- install ----------------------------------------------------------------
chmod +x "$dest"
mv -f "$dest" "$BINDIR/antares"
# macOS Gatekeeper quarantines downloaded binaries; clear it so it runs.
if [ "$goos" = "darwin" ] && have xattr; then
  xattr -d com.apple.quarantine "$BINDIR/antares" 2>/dev/null || true
fi
info "installed $BINDIR/antares"
"$BINDIR/antares" --version 2>/dev/null || true

# ---- PATH --------------------------------------------------------------------
# Add $BINDIR to PATH automatically (matching the Windows installer). We append
# an export to the shell rc files that exist, guarded by a marker so re-running
# never duplicates it. Set ANTARES_NO_MODIFY_PATH=1 to skip and just be told.
add_path_line() {
  local rc="$1" marker="# added by antares installer"
  [ -e "$rc" ] || return 0
  grep -qF "$marker" "$rc" 2>/dev/null && return 0
  {
    printf '\n%s\n' "$marker"
    printf 'export PATH="%s:$PATH"\n' "$BINDIR"
  } >> "$rc"
  info "added $BINDIR to PATH in $rc"
}

case ":$PATH:" in
  *":$BINDIR:"*)
    info "run 'antares' from anywhere (open a new shell if not found yet)"
    ;;
  *)
    if [ "${ANTARES_NO_MODIFY_PATH:-0}" = "1" ]; then
      warn "$BINDIR is not on your PATH. Add it:"
      echo "    echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
    else
      touched=0
      # bash and zsh: both .profile-style and shell-specific rc files.
      for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        [ -e "$rc" ] && { add_path_line "$rc"; touched=1; }
      done
      # zsh with no .zshrc yet (common on macOS): create one so it takes effect.
      if [ -n "${ZSH_VERSION:-}" ] || [ "${SHELL##*/}" = "zsh" ]; then
        [ -e "$HOME/.zshrc" ] || { add_path_line "$HOME/.zshrc"; touched=1; }
      fi
      # fish keeps PATH differently.
      if [ -d "$HOME/.config/fish" ]; then
        fish_cfg="$HOME/.config/fish/config.fish"
        if ! grep -qF "# added by antares installer" "$fish_cfg" 2>/dev/null; then
          { printf '\n# added by antares installer\n'; printf 'set -gx PATH %s $PATH\n' "$BINDIR"; } >> "$fish_cfg"
          info "added $BINDIR to PATH in $fish_cfg"
          touched=1
        fi
      fi
      if [ "$touched" = "1" ]; then
        warn "PATH updated - open a NEW terminal (or 'source' your shell rc) for 'antares' to be found."
      else
        warn "$BINDIR is not on your PATH and no shell rc file was found. Add it:"
        echo "    echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
      fi
    fi
    ;;
esac

echo
info "next: run 'antares setup' to configure a provider, then 'antares' to start it in the background (or 'antares tui' for the terminal UI)"
