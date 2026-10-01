#!/bin/sh
# mikan: one-line install.
#
#   curl -fsSL https://github.com/Romenchi/mikan-plus/releases/latest/download/install.sh | sudo bash
#
# Downloads the installer for this server's architecture from the latest release, checks
# it against the release manifest and starts it with the arguments given after "-s --":
# "... | sudo bash -s -- --join KEY" installs a node of an existing panel. The installer
# checks the manifest's signature itself before it pulls anything else.
set -eu

REPO="Romenchi/mikan-plus"
BASE="https://github.com/$REPO/releases/latest/download"

fail() {
  echo "mikan: $*" >&2
  exit 1
}

[ "$(id -u)" = 0 ] || fail "run as root: curl -fsSL $BASE/install.sh | sudo bash"
case "$(uname -m)" in
  x86_64 | amd64) arch=x86_64 ;;
  aarch64 | arm64) arch=aarch64 ;;
  *) fail "unsupported architecture $(uname -m): mikan runs on x86_64 and aarch64" ;;
esac
command -v curl >/dev/null || fail "curl is missing"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
curl -fsSL --retry 3 "$BASE/manifest.json" -o "$tmp/manifest.json" || fail "cannot download the release manifest"
curl -fsSL --retry 3 "$BASE/mikan-$arch" -o "$tmp/mikan" || fail "cannot download the installer"

# The manifest lists "<arch>": { "url": …, "sha256": "…" }; no jq on a fresh server.
want=$(tr -d '\n ' <"$tmp/manifest.json" | sed -n "s/.*\"$arch\":{\"url\":\"[^\"]*\",\"sha256\":\"\([0-9a-f]\{64\}\)\".*/\1/p")
got=$(sha256sum "$tmp/mikan" | cut -d' ' -f1)
[ -n "$want" ] && [ "$want" = "$got" ] || fail "the installer does not match the release manifest"

install -m 755 "$tmp/mikan" /usr/local/bin/mikan
# The installer is interactive; curl | bash leaves stdin on the script, so it reads the
# terminal. Without one (a script over ssh) it runs on its flags alone.
if [ -t 0 ] || ! (exec </dev/tty) 2>/dev/null; then
  exec /usr/local/bin/mikan install "$@"
fi
exec /usr/local/bin/mikan install "$@" </dev/tty
