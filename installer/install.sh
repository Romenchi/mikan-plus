#!/bin/sh
# mikan: one-line install.
#
#   curl -fsSL https://github.com/Romenchi/mikan-plus/releases/latest/download/install.sh | sudo bash
#
# Downloads the release manifest and its signature, checks the signature against the
# release key built into this script, then downloads the installer for this server's
# architecture from the address the manifest names and checks its sha256 against the
# manifest, and starts it with the arguments given after "-s --":
# "... | sudo bash -s -- --join KEY" installs a node of an existing panel.
#
# What the check proves: the manifest and the binary come from whoever holds the release
# key, even if they were served by a mirror, a cache or a stolen GitHub account. What it
# cannot prove: this script is as trustworthy as the place it came from (it carries the
# key), so the first install rests on TLS and on GitHub for the script itself. Every update
# after that is checked by the installer against the key it carries.
set -eu

REPO="Romenchi/mikan-plus"
BASE="https://github.com/$REPO/releases/latest/download"

# The public half of the key that signs every release's manifest.json: the same key as
# internal/release.PublicKey (installer/src/release.rs checks that this text matches it).
PUBKEY='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAZ3wSIPBSaJxh5CsGO8eINI0aM0kyrQ46EcJSNeH85W8=
-----END PUBLIC KEY-----'

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
command -v sha256sum >/dev/null || fail "sha256sum is missing"
command -v base64 >/dev/null || fail "base64 is missing"

# The signature is Ed25519: openssl 3.0 or newer checks it (Debian 12, Ubuntu 22.04 and
# 24.04, Alpine 3.17+ all have it). A fresh server may not have the command at all.
if ! command -v openssl >/dev/null 2>&1; then
  echo "mikan: openssl is needed to check the release's signature: installing it" >&2
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y openssl >&2 || true
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache openssl >&2 || true
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y openssl >&2 || true
  fi
fi
command -v openssl >/dev/null 2>&1 || fail "openssl is missing and could not be installed: install it and run again"
case "$(openssl version 2>/dev/null)" in
  "OpenSSL 3."* | "OpenSSL 4."*) ;;
  *) fail "this openssl ($(openssl version 2>/dev/null)) cannot check Ed25519 signatures: openssl 3.0 or newer is needed" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
get() {
  curl -fsSL --proto '=https' --tlsv1.2 --retry 3 "$1" -o "$2"
}
get "$BASE/manifest.json" "$tmp/manifest.json" || fail "cannot download the release manifest"
get "$BASE/manifest.json.sig" "$tmp/manifest.json.sig" || fail "cannot download the manifest's signature"

# The signature first: nothing in the manifest is read before it is known to be the release's.
printf '%s\n' "$PUBKEY" >"$tmp/key.pem"
tr -d ' \n\r' <"$tmp/manifest.json.sig" | base64 -d >"$tmp/sig.bin" 2>/dev/null || fail "the manifest's signature is not base64"
[ "$(wc -c <"$tmp/sig.bin" | tr -d ' ')" = 64 ] || fail "the manifest's signature has the wrong length"
openssl pkeyutl -verify -pubin -inkey "$tmp/key.pem" -rawin -in "$tmp/manifest.json" -sigfile "$tmp/sig.bin" >/dev/null 2>&1 ||
  fail "the release manifest's signature does not match the release key: nothing is installed"

# The manifest lists "<arch>": { "url": …, "sha256": "…" } (no jq on a fresh server): the
# arch's object, then its two fields, in whatever order and with whatever else it holds.
entry=$(tr -d '\n ' <"$tmp/manifest.json" | sed -n "s/.*\"$arch\":{\([^}]*\)}.*/\1/p")
url=$(printf '%s' "$entry" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
want=$(printf '%s' "$entry" | sed -n 's/.*"sha256":"\([0-9a-f]\{64\}\)".*/\1/p')
[ -n "$url" ] && [ -n "$want" ] || fail "the release has no installer for $arch"
# The binary comes from the release the manifest is of, not from whatever "latest" is now.
case "$url" in
  "https://github.com/$REPO/releases/download/v"*"/mikan-$arch") ;;
  *) fail "the manifest names an installer at an address outside this project's releases: $url" ;;
esac
get "$url" "$tmp/mikan" || fail "cannot download the installer"
got=$(sha256sum "$tmp/mikan" | cut -d' ' -f1)
[ "$want" = "$got" ] || fail "the installer does not match the signed release manifest"

# Whole or not at all: the old command stays until the new one is on the disk.
install -m 755 "$tmp/mikan" /usr/local/bin/.mikan.new
mv -f /usr/local/bin/.mikan.new /usr/local/bin/mikan
# The installer is interactive; curl | bash leaves stdin on the script, so it reads the
# terminal. Without one (a script over ssh) it runs on its flags alone.
if [ -t 0 ] || ! (exec </dev/tty) 2>/dev/null; then
  exec /usr/local/bin/mikan install "$@"
fi
exec /usr/local/bin/mikan install "$@" </dev/tty
