#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
# Ensure local OpenFlux-core tree, then build
bash "$ROOT/scripts/sync-openflux-core.sh"
exec "$ROOT/scripts/build-ios-core.sh"
