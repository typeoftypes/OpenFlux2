#!/bin/bash
# Populate ./OpenFlux-core from this monorepo (no git submodule).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/OpenFlux-core"
mkdir -p "$DEST"

for item in mobile transport utils netbind share socks5 streamproxy tunnel provision \
            go.mod go.sum main.go; do
  if [ -e "$ROOT/$item" ]; then
    if [ -d "$ROOT/$item" ]; then
      mkdir -p "$DEST/$(basename "$item")"
      rsync -a --delete --exclude 'output' --exclude '.git' \
        "$ROOT/$item/" "$DEST/$(basename "$item")/" 2>/dev/null \
        || cp -a "$ROOT/$item/." "$DEST/$(basename "$item")/"
    else
      cp -f "$ROOT/$item" "$DEST/"
    fi
  fi
done

# Minimal build_ios.sh INSIDE core — only compiles mobile/, never calls sync
cat > "$DEST/build_ios.sh" << 'COREBUILD'
#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$ROOT/output/ios"
OUT="$OUT_DIR/liboflux.a"
mkdir -p "$OUT_DIR"
[ -d "$ROOT/mobile/ios" ] || { echo "error: mobile/ios missing in $ROOT"; exit 1; }
if [ "$(uname -s)" != "Darwin" ]; then echo "error: need macOS"; exit 1; fi
SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path)"
CLANG="$(xcrun --sdk iphoneos --find clang)"
export GOOS=ios GOARCH=arm64 CGO_ENABLED=1
export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
echo "==> building liboflux.a from $ROOT/mobile"
(cd "$ROOT/mobile" && go build -a -buildmode=c-archive -tags ios \
  -ldflags="-w" -trimpath -o "$OUT" ./ios)
ls -lh "$OUT"
COREBUILD
chmod +x "$DEST/build_ios.sh"

echo "OpenFlux-core synced -> $DEST"
ls "$DEST/mobile" 2>/dev/null | head -5
