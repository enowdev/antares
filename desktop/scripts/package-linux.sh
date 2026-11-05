#!/usr/bin/env bash
# Package the Linux desktop shell with the antares server beside it, as a
# .deb (installs to /opt/antares, a launcher entry and an icon) and a
# .tar.gz (unpack anywhere and run ./antares-desktop).
#
#   scripts/package-linux.sh <shell-binary> <antares-binary> <version> <amd64|arm64> <out-dir>
#
# Writes <out-dir>/Antares-linux-<x64|arm64>.deb and .tar.gz. Needs dpkg-deb.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
shell_bin="$1"; server_bin="$2"; version="${3#v}"; arch="$4"; out="$5"
case "$arch" in amd64) label=x64 ;; arm64) label=arm64 ;; *) echo "arch must be amd64 or arm64" >&2; exit 1 ;; esac
# Debian versions must start with a digit; keep the leading numbers.
deb_version="$(printf '%s' "$version" | sed -E 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/')"
[[ "$deb_version" =~ ^[0-9] ]] || deb_version="0.0.0"

mkdir -p "$out"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# ---- .tar.gz -----------------------------------------------------------------
tdir="$work/antares"
mkdir -p "$tdir"
install -m 755 "$shell_bin" "$tdir/antares-desktop"
install -m 755 "$server_bin" "$tdir/antares"
install -m 644 "$here/build/linux/antares.png" "$tdir/antares.png"
install -m 644 "$here/build/linux/antares.desktop" "$tdir/antares.desktop"
tar -C "$work" -czf "$out/Antares-linux-$label.tar.gz" antares

# ---- .deb --------------------------------------------------------------------
root="$work/deb"
install -d "$root/DEBIAN" "$root/opt/antares" "$root/usr/bin" \
  "$root/usr/share/applications" "$root/usr/share/icons/hicolor/512x512/apps"
install -m 755 "$shell_bin" "$root/opt/antares/antares-desktop"
install -m 755 "$server_bin" "$root/opt/antares/antares"
ln -s /opt/antares/antares-desktop "$root/usr/bin/antares-desktop"
install -m 644 "$here/build/linux/antares.desktop" "$root/usr/share/applications/antares.desktop"
install -m 644 "$here/build/linux/antares.png" "$root/usr/share/icons/hicolor/512x512/apps/antares.png"
size="$(du -sk "$root" | cut -f1)"
cat > "$root/DEBIAN/control" <<CONTROL
Package: antares-desktop
Version: $deb_version
Architecture: $arch
Maintainer: enowdev <noreply@enowx.ai>
Installed-Size: $size
Depends: libgtk-3-0, libwebkit2gtk-4.1-0
Section: devel
Priority: optional
Homepage: https://antares.enowx.ai
Description: Antares desktop app
 A window onto an Antares dashboard, on this machine or a remote server.
 The antares server ships beside it in /opt/antares.
CONTROL
dpkg-deb --root-owner-group --build "$root" "$out/Antares-linux-$label.deb" >/dev/null
echo "packaged $out/Antares-linux-$label.deb and .tar.gz"
