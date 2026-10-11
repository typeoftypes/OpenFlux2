#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
core="${1:-$root/OpenFlux-core}"

if [ ! -d "$core/mobile" ]; then
  echo "OpenFlux-core missing — syncing..."
  bash "$root/scripts/sync-openflux-core.sh"
  core="$root/OpenFlux-core"
fi
[ -d "$core/mobile" ] || { echo "error: no mobile/ under $core"; exit 1; }

out="$root/ios-app/Lib"
mkdir -p "$out"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: macOS + Xcode required"; exit 1
fi

echo "==> core: $core"
echo "    module: $(grep -E '^module ' "$core/go.mod" 2>/dev/null | awk '{print $2}' || true)"

# Always use the core-local build_ios.sh written by sync (no scripts/ dependency)
if [ ! -f "$core/build_ios.sh" ]; then
  bash "$root/scripts/sync-openflux-core.sh"
fi
chmod +x "$core/build_ios.sh"
(cd "$core" && bash ./build_ios.sh)

src_a="$core/output/ios/liboflux.a"
[ -f "$src_a" ] || { echo "error: $src_a missing"; exit 1; }
cp -f "$src_a" "$out/liboflux.a"
[ -f "${src_a%.a}.h" ] && cp -f "${src_a%.a}.h" "$out/liboflux.h"
h=$(ls "$core/output/ios/"*.h 2>/dev/null | head -1 || true)
[ -n "$h" ] && [ ! -f "$out/liboflux.h" ] && cp -f "$h" "$out/liboflux.h"

branch=$(git -C "$root" rev-parse --abbrev-ref HEAD 2>/dev/null || echo local)
rev=$(git -C "$root" describe --always --dirty 2>/dev/null || echo unknown)
printf '%s@%s\n' "$branch" "$rev" > "$out/openflux-core.version"
echo "core $(cat "$out/openflux-core.version") -> $out/liboflux.a"
ls -lh "$out/liboflux.a"
