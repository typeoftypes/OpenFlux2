#!/bin/bash
# Repo-root: sync OpenFlux-core, build into ios-app/Lib AND output/ios/ (for CI checks).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
bash "$ROOT/scripts/sync-openflux-core.sh"
bash "$ROOT/scripts/build-ios-core.sh"

# Mirror into repo-root output/ios for workflows that check this path
mkdir -p "$ROOT/output/ios"
if [ -f "$ROOT/ios-app/Lib/liboflux.a" ]; then
  cp -f "$ROOT/ios-app/Lib/liboflux.a" "$ROOT/output/ios/liboflux.a"
  [ -f "$ROOT/ios-app/Lib/liboflux.h" ] && cp -f "$ROOT/ios-app/Lib/liboflux.h" "$ROOT/output/ios/liboflux.h"
fi
if [ ! -f "$ROOT/output/ios/liboflux.a" ]; then
  echo "error: liboflux.a not in output/ios or ios-app/Lib" >&2
  exit 1
fi
echo "==> also at $ROOT/output/ios/liboflux.a"
ls -lh "$ROOT/output/ios/liboflux.a" "$ROOT/ios-app/Lib/liboflux.a"
