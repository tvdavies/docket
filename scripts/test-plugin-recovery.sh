#!/usr/bin/env bash
# Reproducible validation without inherited workspace/registry/Dispatch state.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$(mktemp -d "${TMPDIR:-/tmp}/docket-recovery-tests.XXXXXX")
trap 'rm -rf "$root"' EXIT
# Reuse only compiler caches; no runtime config or user PATH reaches fixtures.
go_bin=$(command -v go)
gocache=$(go env GOCACHE)
gomodcache=$(go env GOMODCACHE)
mkdir -p "$root"/{tmp,config,state,data,cache,runtime}
env -i \
  PATH="$(dirname "$go_bin"):/usr/bin:/bin" \
  HOME="$root" TMPDIR="$root/tmp" \
  DOCKET_CONFIG="$root/registry.yaml" DOCKET_PLUGIN_DIR="$root/plugins" \
  XDG_CONFIG_HOME="$root/config" XDG_STATE_HOME="$root/state" \
  XDG_DATA_HOME="$root/data" XDG_CACHE_HOME="$root/cache" XDG_RUNTIME_DIR="$root/runtime" \
  GOCACHE="$gocache" GOMODCACHE="$gomodcache" \
  "$go_bin" "$@"
