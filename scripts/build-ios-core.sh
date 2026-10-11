#!/bin/bash
# Build liboflux.a from ./OpenFlux-core (local tree, not necessarily a git submodule).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
core="${1:-$root/OpenFlux-core}"

if [ ! -d "$core/mobile" ]; then
  echo "OpenFlux-core missing — syncing from monorepo root..."
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
echo "    module: $(grep -E '^module ' "$core/go.mod" 2>/dev/null | awk '{print $2}')"

if [ -f "$core/build_ios.sh" ]; then
  chmod +x "$core/build_ios.sh"
  (cd "$core" && bash ./build_ios.sh)
  src_a="$core/output/ios/liboflux.a"
else
  SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path)"
  CLANG="$(xcrun --sdk iphoneos --find clang)"
  export GOOS=ios GOARCH=arm64 CGO_ENABLED=1
  export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  mkdir -p "$core/output/ios"
  (cd "$core/mobile" && go build -a -buildmode=c-archive -tags ios \
    -ldflags="-w" -trimpath -o "$core/output/ios/liboflux.a" ./ios)
  src_a="$core/output/ios/liboflux.a"
fi

cp -f "$src_a" "$out/liboflux.a"
[ -f "${src_a%.a}.h" ] && cp -f "${src_a%.a}.h" "$out/liboflux.h"
branch=$(git -C "$root" rev-parse --abbrev-ref HEAD 2>/dev/null || echo local)
rev=$(git -C "$root" describe --always --dirty 2>/dev/null || echo unknown)
printf '%s@%s\n' "$branch" "$rev" > "$out/openflux-core.version"
echo "core $(cat "$out/openflux-core.version") -> $out/liboflux.a"
