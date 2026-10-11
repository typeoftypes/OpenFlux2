#!/bin/bash
# Monorepo: build liboflux.a from THIS repo (mobile/ + transport/).
# No OpenFlux-core submodule.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$ROOT/output/ios"
OUT="$OUT_DIR/liboflux.a"
mkdir -p "$OUT_DIR"

[ -d "$ROOT/mobile/ios" ] || { echo "error: $ROOT/mobile/ios missing"; exit 1; }
[ -f "$ROOT/go.mod" ] || { echo "error: go.mod missing"; exit 1; }

echo "==> monorepo core from $ROOT"
echo "    module: $(grep -E '^module ' "$ROOT/go.mod" | awk '{print $2}')"
[ -f "$ROOT/transport/mailru/mailru.go" ] && \
  echo "    UA: $(grep -oE 'Mozilla/5.0[^"]+' "$ROOT/transport/mailru/mailru.go" | head -1 || true)"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: need macOS + Xcode"; exit 1
fi
SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path)"
CLANG="$(xcrun --sdk iphoneos --find clang)"
export GOOS=ios GOARCH=arm64 CGO_ENABLED=1
export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"

(cd "$ROOT/mobile" && go build -a -buildmode=c-archive -tags ios \
  -ldflags="-w" -trimpath -o "$OUT" ./ios)

ls -lh "$OUT"
STRINGS_FILE="$OUT_DIR/liboflux.strings.txt"
/usr/bin/strings "$OUT" >"$STRINGS_FILE" 2>/dev/null || true
grep -E 'Mozilla/5\.0|iPhone OS|Firefox/|Chrome/' "$STRINGS_FILE" | sort -u | head -15 || true
