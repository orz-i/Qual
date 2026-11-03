#!/usr/bin/env bash
# Reproducible Qual Go patch-level gate without modifying system Go or gvm.
# All downloaded toolchain/build/module bytes stay under ignored tmp/ in Qual.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [[ ! -f "$ROOT/apps/server/go.mod" || ! -f "$ROOT/Makefile" ]]; then
  echo "Must be run inside a Qual repository checkout" >&2
  exit 2
fi
CACHE="$ROOT/tmp/phase10"
export GOPATH="$CACHE/go-toolchain-gopath"
export GOMODCACHE="$CACHE/go-toolchain-modcache"
export GOCACHE="$CACHE/go-toolchain-buildcache"
export GOTOOLCHAIN="go1.27.3"
mkdir -p "$GOPATH" "$GOMODCACHE" "$GOCACHE"
VERSION="$(go version)"
case "$VERSION" in
  "go version go1.27.3 "*) ;;
  *) echo "Required Go 1.27.3 toolchain unavailable, observed: $VERSION" >&2; exit 3 ;;
esac
echo "Qual Go gate using $VERSION (project-local toolchain/cache; no system installation)"
cd "$ROOT"
make check-go
