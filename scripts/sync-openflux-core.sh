#!/bin/bash
# Populate ./OpenFlux-core from monorepo root — full Go tree except app/CI noise.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/OpenFlux-core"
mkdir -p "$DEST"

# Full copy of module sources so `go build ./mobile/ios` resolves all imports.
rsync -a --delete \
  --exclude '.git/' \
  --exclude 'OpenFlux-core/' \
  --exclude 'ios-app/' \
  --exclude 'output/' \
  --exclude 'DerivedData/' \
  --exclude 'build/' \
  --exclude '.github/' \
  --exclude 'androidApp/' \
  --exclude 'shared/' \
  --exclude 'node_modules/' \
  --exclude '*.ipa' \
  --exclude '*.xcarchive' \
  "$ROOT/" "$DEST/"

# Core-local build_ios.sh: compile only (never call repo-root scripts/)
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
echo "==> building liboflux.a from $ROOT (module $(grep -E '^module ' "$ROOT/go.mod" | awk '{print $2}'))"
# Build from module root so all packages resolve; package path is mobile/ios
(cd "$ROOT" && go build -a -buildmode=c-archive -tags ios \
  -ldflags="-w" -trimpath \
  -o "$OUT" \
  ./mobile/ios)
ls -lh "$OUT"
COREBUILD
chmod +x "$DEST/build_ios.sh"

echo "OpenFlux-core synced -> $DEST"
ls "$DEST/mobile" "$DEST/network" "$DEST/deploy" 2>/dev/null | head -20 || true
test -d "$DEST/mobile/ios"
test -f "$DEST/go.mod"
