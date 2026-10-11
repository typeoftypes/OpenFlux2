#!/bin/bash
# Build liboflux.a from THIS repository (mobile/ios + local transport/).
# Does not use a core/ submodule — UA/transport changes in this tree must land in the IPA.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$ROOT/output/ios"
OUT="$OUT_DIR/liboflux.a"

mkdir -p "$OUT_DIR"

if [ ! -d "$ROOT/mobile/ios" ]; then
  echo "error: mobile/ios not found under $ROOT" >&2
  exit 1
fi
if [ ! -f "$ROOT/transport/mailru/mailru.go" ]; then
  echo "error: transport/mailru/mailru.go missing under $ROOT" >&2
  exit 1
fi

MODULE="$(grep -E '^module ' "$ROOT/go.mod" 2>/dev/null | awk '{print $2}' || true)"
UA="$(grep -oE 'Mozilla/5\.0[^"]+' "$ROOT/transport/mailru/mailru.go" | head -1 || true)"
echo "==> Building liboflux.a"
echo "    root:   $ROOT"
echo "    module: ${MODULE:-(unknown)}"
echo "    UA:     ${UA:-(not found in mailru.go)}"

# Require macOS + iPhoneOS SDK for GOOS=ios c-archive.
if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: iOS c-archive must be built on macOS (Xcode). This host is $(uname -s)." >&2
  exit 1
fi

XCODE_PATH="${XCODE_PATH:-/Applications/Xcode.app}"
SDK_PATH="${SDK_PATH:-$XCODE_PATH/Contents/Developer/Platforms/iPhoneOS.platform/Developer/SDKs/iPhoneOS.sdk}"
if [ ! -d "$SDK_PATH" ]; then
  # Fallback: xcrun
  SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path 2>/dev/null || true)"
fi
if [ -z "${SDK_PATH}" ] || [ ! -d "$SDK_PATH" ]; then
  echo "error: iPhoneOS SDK not found. Install Xcode." >&2
  exit 1
fi

export GOOS=ios
export GOARCH=arm64
export CGO_ENABLED=1
export SDK_PATH
CLANG="$(xcrun --sdk iphoneos --find clang)"
export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CXX="${CLANG}++ -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"

(
  cd "$ROOT/mobile"
  go build -a -buildmode=c-archive -tags ios \
    -ldflags="-w" -trimpath \
    -o "$OUT" \
    ./ios
)

if [ ! -f "$OUT" ]; then
  echo "error: build did not produce $OUT" >&2
  exit 1
fi

echo "==> Built: $OUT"
ls -lh "$OUT"

echo "==> UA strings inside archive:"
strings "$OUT" | grep -E 'Mozilla/5\.0|Firefox/|Chrome/|iPhone OS|Macintosh' | sort -u | head -20 || true

# Fail if source declares a distinctive marker that is missing from the binary.
if [ -n "$UA" ]; then
  MARKER="$(printf '%s' "$UA" | grep -oE 'Firefox/[0-9]+|Chrome/[0-9]+|iPhone OS [0-9_]+|Version/[0-9.]+' | head -1 || true)"
  if [ -n "$MARKER" ]; then
    if ! strings "$OUT" | grep -qF "$MARKER"; then
      echo "error: liboflux.a does not contain UA marker '$MARKER' from transport/mailru/mailru.go" >&2
      echo "source UA: $UA" >&2
      exit 1
    fi
    echo "==> OK: found UA marker '$MARKER' in liboflux.a"
  fi
fi

# Header is emitted next to the .a by cgo (liboflux.h or same basename).
if [ -f "${OUT%.a}.h" ]; then
  echo "==> Header: ${OUT%.a}.h"
elif [ -f "$OUT_DIR/liboflux.h" ]; then
  echo "==> Header: $OUT_DIR/liboflux.h"
else
  echo "warning: no liboflux.h next to archive (cgo usually writes it)"
fi
