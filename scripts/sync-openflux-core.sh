#!/bin/bash
# Populate ./OpenFlux-core from this monorepo (no git submodule).
# Copies mobile/, transport/, go.mod, build_ios.sh, etc. for scripts/build-ios-core.sh.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/OpenFlux-core"
mkdir -p "$DEST"

# Core pieces only — not ios-app (avoid recursion)
for item in mobile transport utils netbind share socks5 streamproxy tunnel provision \
            go.mod go.sum build_ios.sh main.go; do
  if [ -e "$ROOT/$item" ]; then
    rsync -a --delete \
      --exclude 'output' --exclude '.git' \
      "$ROOT/$item" "$DEST/" 2>/dev/null || cp -a "$ROOT/$item" "$DEST/"
  fi
done
# Prefer keeping go.mod at DEST root
[ -f "$ROOT/go.mod" ] && cp -f "$ROOT/go.mod" "$DEST/go.mod"
[ -f "$ROOT/go.sum" ] && cp -f "$ROOT/go.sum" "$DEST/go.sum"
[ -f "$ROOT/build_ios.sh" ] && cp -f "$ROOT/build_ios.sh" "$DEST/build_ios.sh" && chmod +x "$DEST/build_ios.sh"

echo "OpenFlux-core synced from monorepo -> $DEST"
ls "$DEST/mobile" "$DEST/transport" 2>/dev/null | head
