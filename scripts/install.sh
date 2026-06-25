#!/bin/sh
# PromptVCR CLI installer.
#
#   curl -fsSL https://raw.githubusercontent.com/promptvcr/cli/main/scripts/install.sh | sh
#
# Detects OS/arch, downloads the matching release archive from promptvcr/cli,
# verifies it against checksums.txt, and installs the `promptvcr` binary.
#
# Env overrides:
#   PROMPTVCR_VERSION       pin a version (e.g. v0.2.0); default: latest release
#   PROMPTVCR_INSTALL_DIR   install directory; default: $HOME/.local/bin
set -eu

REPO="promptvcr/cli"
BINARY="promptvcr"
INSTALL_DIR="${PROMPTVCR_INSTALL_DIR:-$HOME/.local/bin}"

err() { printf 'error: %s\n' "$1" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- detect platform ---------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) err "unsupported OS '$os' (use install.ps1 on Windows)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) err "unsupported architecture '$arch'" ;;
esac

# --- pick a downloader -------------------------------------------------------
if have curl; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif have wget; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO - "$1"; }
else
  err "need curl or wget"
fi

# --- resolve version ---------------------------------------------------------
tag="${PROMPTVCR_VERSION:-}"
if [ -z "$tag" ]; then
  tag="$(fetch "https://api.github.com/repos/$REPO/releases/latest" \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
fi
[ -n "$tag" ] || err "could not resolve a release tag (set PROMPTVCR_VERSION)"
version="${tag#v}"

archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

# --- download + verify + install ---------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf 'Downloading %s %s (%s/%s)...\n' "$BINARY" "$tag" "$os" "$arch"
dl "$base/$archive" "$tmp/$archive" || err "download failed: $base/$archive"

if dl "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  expected="$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')"
  if [ -n "$expected" ]; then
    if have sha256sum; then
      actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
    elif have shasum; then
      actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
    else
      actual=""
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      err "checksum mismatch for $archive"
    fi
    [ -n "$actual" ] && printf 'Checksum verified.\n'
  fi
else
  printf 'warning: checksums.txt not found; skipping verification\n' >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp" || err "failed to extract $archive"
[ -f "$tmp/$BINARY" ] || err "archive did not contain '$BINARY'"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/$BINARY" "$INSTALL_DIR/$BINARY" 2>/dev/null \
  || { cp "$tmp/$BINARY" "$INSTALL_DIR/$BINARY" && chmod 0755 "$INSTALL_DIR/$BINARY"; }

printf '\nInstalled %s to %s\n' "$BINARY" "$INSTALL_DIR/$BINARY"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Add it to your PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac
printf 'Next: %s init && %s doctor\n' "$BINARY" "$BINARY"
