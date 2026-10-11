#!/bin/bash
# Ensure OpenFlux-core submodule exists, then build into ios-app/Lib.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
if [ ! -d "$ROOT/OpenFlux-core/mobile" ]; then
  echo "==> init submodule OpenFlux-core"
  git -C "$ROOT" submodule update --init --recursive OpenFlux-core
fi
exec "$ROOT/scripts/build-ios-core.sh"
