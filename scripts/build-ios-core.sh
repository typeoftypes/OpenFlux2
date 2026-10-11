#!/bin/bash
# Build liboflux.a from the OpenFlux core checkout into ios-app/Lib/,
# plus openflux-core.version (branch@commit), same role as
# scripts/build-android-core.sh in OpenFluxAndroid.
#
#   scripts/build-ios-core.sh              # uses the OpenFlux-core/ submodule
#   scripts/build-ios-core.sh ../OpenFlux  # or any other checkout
#
# Needs Go, Xcode (iPhoneOS SDK). Run on macOS.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
core=$(cd "${1:-$root/OpenFlux-core}" && pwd)
[ -d "$core/mobile" ] || {
  echo "$core has no mobile/ package. Run 'git submodule update --init OpenFlux-core' or pass the path to an OpenFlux-core checkout." >&2
  exit 1
}
[ -f "$core/go.mod" ] || {
  echo "$core has no go.mod — not an OpenFlux core tree." >&2
  exit 1
}

out="$root/ios-app/Lib"
mkdir -p "$out"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: iOS c-archive requires macOS + Xcode (this host is $(uname -s))" >&2
  exit 1
fi

echo "==> core: $core"
echo "    module: $(grep -E '^module ' "$core/go.mod" | awk '{print $2}')"
if [ -f "$core/transport/mailru/mailru.go" ]; then
  echo "    UA: $(grep -oE 'Mozilla/5.0[^"]+' "$core/transport/mailru/mailru.go" | head -1 || true)"
fi

# Prefer core's own build_ios.sh (builds mobile/ -> output/ios/liboflux.a)
if [ -x "$core/build_ios.sh" ] || [ -f "$core/build_ios.sh" ]; then
  chmod +x "$core/build_ios.sh"
  (cd "$core" && bash ./build_ios.sh)
  src_a="$core/output/ios/liboflux.a"
  src_h="$core/output/ios/liboflux.h"
else
  # Inline build if submodule has no build_ios.sh
  echo "==> building mobile/ios c-archive inline"
  SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path)"
  CLANG="$(xcrun --sdk iphoneos --find clang)"
  export GOOS=ios GOARCH=arm64 CGO_ENABLED=1
  export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
  mkdir -p "$core/output/ios"
  (cd "$core/mobile" && go build -a -buildmode=c-archive -tags ios \
    -ldflags="-w" -trimpath \
    -o "$core/output/ios/liboflux.a" ./ios)
  src_a="$core/output/ios/liboflux.a"
  src_h="$core/output/ios/liboflux.h"
fi

[ -f "$src_a" ] || { echo "error: liboflux.a not produced under $core/output/ios" >&2; exit 1; }

cp -f "$src_a" "$out/liboflux.a"
if [ -f "$src_h" ]; then
  cp -f "$src_h" "$out/liboflux.h"
elif ls "$core/output/ios/"*.h >/dev/null 2>&1; then
  cp -f "$(ls "$core/output/ios/"*.h | head -1)" "$out/liboflux.h"
fi

branch=$(git -C "$core" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
rev=$(git -C "$core" describe --always --dirty 2>/dev/null || echo unknown)
printf '%s@%s\n' "$branch" "$rev" > "$out/openflux-core.version"
echo "core $(cat "$out/openflux-core.version") -> $out/liboflux.a"
ls -lh "$out/liboflux.a"
