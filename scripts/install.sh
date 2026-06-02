#!/usr/bin/env bash
#
# Install the Emailable CLI.
#
# Usage:
#   curl -fsSL https://emailable.com/install-cli | bash
#
# Environment overrides:
#   EMAILABLE_VERSION   Specific version to install (e.g. v0.2.0). Defaults to
#                       the latest GitHub release.
#   EMAILABLE_PREFIX    Install prefix. Defaults to /usr/local when writable,
#                       otherwise $HOME/.local. Binary goes into <prefix>/bin
#                       and man pages into <prefix>/share/man/man1.
#   EMAILABLE_NO_MAN    Set to any non-empty value to skip man-page install.
#
# The script picks the right release tarball for your OS/arch, verifies it
# against the published checksums.txt, and installs the binary + bundled man
# pages. It uses sudo only when necessary (system-wide prefix on a tree the
# current user can't write to).

set -euo pipefail

REPO="emailable/emailable-cli"
BINARY="emailable"

red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
dim()   { printf '\033[2m%s\033[0m\n'  "$*"; }

abort() { red "Error: $*"; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || abort "missing required command: $1"
}

need curl
need tar
need uname
need mktemp
need install

# --- detect OS / arch -------------------------------------------------------

os_raw=$(uname -s)
arch_raw=$(uname -m)

case "$os_raw" in
  Linux)   os=linux ;;
  Darwin)  os=darwin ;;
  *)       abort "unsupported OS: $os_raw (use the Windows PowerShell installer instead)" ;;
esac

case "$arch_raw" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)           abort "unsupported architecture: $arch_raw" ;;
esac

# --- resolve version --------------------------------------------------------

# Resolve the latest version, validating against semver so a prerelease-only
# repo (whose /releases/latest has no /tag/) fails here instead of 404ing on a
# bogus download URL. Prereleases aren't auto-selected; set EMAILABLE_VERSION.
latest_version() {
  local url version json

  # Prefer the redirect over the API to dodge unauthenticated rate limits.
  if url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
             "https://github.com/${REPO}/releases/latest" 2>/dev/null); then
    version="${url##*/}"
    version="${version#v}"
    if [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
      printf '%s\n' "$version"
      return 0
    fi
  fi

  if json=$(curl -fsSL -H 'Accept: application/vnd.github+json' \
              -H 'User-Agent: emailable-cli-installer' \
              "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null); then
    if [[ "$json" =~ \"tag_name\"[[:space:]]*:[[:space:]]*\"v?([^\"]+)\" ]]; then
      version="${BASH_REMATCH[1]}"
      if [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
        printf '%s\n' "$version"
        return 0
      fi
    fi
  fi

  return 1
}

version="${EMAILABLE_VERSION:-}"
if [ -z "$version" ]; then
  version=$(latest_version) || \
    abort "could not determine the latest version; set EMAILABLE_VERSION to install a specific release"
fi
version="${version#v}"
tag="v${version}"

# --- pick prefix ------------------------------------------------------------

prefix="${EMAILABLE_PREFIX:-}"
if [ -z "$prefix" ]; then
  if [ -w /usr/local ] || [ "$(id -u)" -eq 0 ]; then
    prefix=/usr/local
  elif [ -d /usr/local ] && command -v sudo >/dev/null 2>&1; then
    prefix=/usr/local
  else
    prefix="$HOME/.local"
  fi
fi

bindir="$prefix/bin"
mandir="$prefix/share/man/man1"

# --- download & verify ------------------------------------------------------

archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

# Bare `mktemp -d` works on GNU and modern BSD/macOS; the templated form is a
# fallback for stricter mktemp implementations that demand one.
tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t emailable)
trap 'rm -rf "$tmpdir"' EXIT

dim "Downloading $archive from $tag..."
curl -fsSL -o "$tmpdir/$archive"     "$base_url/$archive"
curl -fsSL -o "$tmpdir/checksums.txt" "$base_url/checksums.txt"

dim "Verifying checksum..."
# Field match on the exact filename — avoids the dots in $archive being
# treated as regex wildcards against unrelated checksum lines.
expected=$(awk -v f="$archive" '$2 == f {print $1}' "$tmpdir/checksums.txt")
[ -n "$expected" ] || abort "no checksum entry for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmpdir/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmpdir/$archive" | awk '{print $1}')
else
  abort "neither sha256sum nor shasum is available"
fi

[ "$expected" = "$actual" ] || abort "checksum mismatch (expected $expected, got $actual)"

# --- extract & install ------------------------------------------------------

tar -xzf "$tmpdir/$archive" -C "$tmpdir"

# sudo wrapper: only escalate when we can't write the target directory.
maybe_sudo() {
  if [ -w "$(dirname "$1")" ] || [ "$(id -u)" -eq 0 ]; then
    "${@:2}"
  else
    sudo "${@:2}"
  fi
}

dim "Installing to $bindir/$BINARY..."
maybe_sudo "$bindir" install -d -m 0755 "$bindir"
maybe_sudo "$bindir/$BINARY" install -m 0755 "$tmpdir/$BINARY" "$bindir/$BINARY"

if [ -z "${EMAILABLE_NO_MAN:-}" ] && [ -d "$tmpdir/man" ]; then
  dim "Installing man pages to $mandir..."
  maybe_sudo "$mandir" install -d -m 0755 "$mandir"
  for page in "$tmpdir"/man/*.1; do
    [ -e "$page" ] || continue
    maybe_sudo "$mandir/$(basename "$page")" install -m 0644 "$page" "$mandir/"
  done
fi

green "Installed $BINARY $version to $bindir/$BINARY"

# Warn if the install prefix isn't on PATH (common for ~/.local).
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) dim "Note: $bindir is not on your PATH. Add it to your shell profile:"
     dim "  export PATH=\"$bindir:\$PATH\""
     ;;
esac
