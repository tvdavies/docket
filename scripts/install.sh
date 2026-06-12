#!/bin/sh
# tadu installer — drops the `tadu` binary into a bin dir on PATH.
#
# Usage (humans or agents):
#   curl -fsSL https://raw.githubusercontent.com/tvdavies/tadu/main/scripts/install.sh | sh
#
# Env overrides:
#   TADU_VERSION   release tag to install (default: latest)
#   TADU_BIN_DIR   install dir (default: $HOME/.local/bin)
#   TADU_REPO      owner/repo (default: tvdavies/tadu)
#
# If no prebuilt release asset is found but Go is installed, it builds from
# source as a fallback.
set -eu

REPO="${TADU_REPO:-tvdavies/tadu}"
BIN_DIR="${TADU_BIN_DIR:-$HOME/.local/bin}"
VERSION="${TADU_VERSION:-latest}"

say()  { printf '%s\n' "tadu-install: $*"; }
die()  { printf '%s\n' "tadu-install: error: $*" >&2; exit 1; }

detect_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    linux) echo linux ;;
    darwin) echo darwin ;;
    *) die "unsupported OS: $os" ;;
  esac
}

detect_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) die "unsupported arch: $arch" ;;
  esac
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"; return
  fi
  # Resolve the latest tag via the GitHub API (no auth needed for public repos).
  api="https://api.github.com/repos/$REPO/releases/latest"
  tag=$(curl -fsSL "$api" 2>/dev/null | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' || true)
  echo "$tag"
}

install_from_release() {
  os=$1 arch=$2 ver=$3
  [ -n "$ver" ] || return 1
  asset="tadu_${os}_${arch}.tar.gz"
  url="https://github.com/$REPO/releases/download/$ver/$asset"
  say "downloading $url"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  if ! curl -fsSL "$url" -o "$tmp/$asset" 2>/dev/null; then
    return 1
  fi
  tar -xzf "$tmp/$asset" -C "$tmp" || return 1
  mkdir -p "$BIN_DIR"
  install -m 0755 "$tmp/tadu" "$BIN_DIR/tadu" 2>/dev/null || { cp "$tmp/tadu" "$BIN_DIR/tadu"; chmod 0755 "$BIN_DIR/tadu"; }
  return 0
}

install_from_source() {
  command -v go >/dev/null 2>&1 || return 1
  say "no release asset found; building from source with go install"
  GOBIN="$BIN_DIR" go install "github.com/$REPO@${1:-latest}" 2>/dev/null && return 0
  return 1
}

main() {
  command -v curl >/dev/null 2>&1 || die "curl is required"
  command -v tar  >/dev/null 2>&1 || die "tar is required"

  os=$(detect_os)
  arch=$(detect_arch)
  ver=$(resolve_version)

  if install_from_release "$os" "$arch" "$ver"; then
    :
  elif install_from_source "$ver"; then
    :
  else
    die "could not install from release or source. Install Go and retry, or download a binary from https://github.com/$REPO/releases"
  fi

  say "installed to $BIN_DIR/tadu"
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) say "note: $BIN_DIR is not on PATH. Add: export PATH=\"$BIN_DIR:\$PATH\"" ;;
  esac
  "$BIN_DIR/tadu" --version || true
  say "run 'tadu skill' to print the agent usage guide"
}

main "$@"
