#!/bin/bash
# Repo-root entry: sync OpenFlux-core tree, then build into ios-app/Lib.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
bash "$ROOT/scripts/sync-openflux-core.sh"
bash "$ROOT/scripts/build-ios-core.sh"
