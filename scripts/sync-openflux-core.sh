#!/bin/bash
# Populate ./OpenFlux-core from monorepo root — full Go tree except app/CI noise.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/OpenFlux-core"
mkdir -p "$DEST"

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

# Parent module path (e.g. github.com/typeoftypes/OpenFlux2)
PARENT_MOD="$(grep -E '^module ' "$DEST/go.mod" | awk '{print $2}')"
echo "parent module: $PARENT_MOD"

# mobile/ is module openflux-mobile and must replace parent -> ..
if [ -f "$DEST/mobile/go.mod" ]; then
  python3 - "$DEST/mobile/go.mod" "$PARENT_MOD" <<'PY'
import sys, re
path, parent = sys.argv[1], sys.argv[2]
t = open(path).read()
# Ensure require + replace for parent module
if parent not in t:
    # add require if missing
    if f"require (\n" in t and parent not in t:
        t = t.replace(
            "require (\n",
            f"require (\n\t{parent} v0.0.0\n",
            1,
        )
# Always set replaces at end
# Remove old replace lines for known roots
lines = t.splitlines()
out = []
for line in lines:
    if line.strip().startswith("replace ") and ("OpenFlux" in line or "OpenFlux2" in line or "universal-bypass" in line):
        continue
    out.append(line)
t = "\n".join(out).rstrip() + "\n"
replaces = f"""
replace {parent} => ..
replace github.com/p1neappleXpress/OpenFlux => ..
replace github.com/typeoftypes/OpenFlux2 => ..
"""
# dedupe if parent is already one of these
t = t + "\n" + replaces
open(path, "w").write(t)
print("patched mobile/go.mod replaces -> ..")
PY
fi

# Core-local build: from mobile/ package ios (nested module openflux-mobile)
cat > "$DEST/build_ios.sh" << 'COREBUILD'
#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$ROOT/output/ios"
OUT="$OUT_DIR/liboflux.a"
mkdir -p "$OUT_DIR"
[ -d "$ROOT/mobile/ios" ] || { echo "error: mobile/ios missing"; exit 1; }
if [ "$(uname -s)" != "Darwin" ]; then echo "error: need macOS"; exit 1; fi
SDK_PATH="$(xcrun --sdk iphoneos --show-sdk-path)"
CLANG="$(xcrun --sdk iphoneos --find clang)"
export GOOS=ios GOARCH=arm64 CGO_ENABLED=1
export CC="$CLANG -isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_CFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"
export CGO_LDFLAGS="-isysroot $SDK_PATH -arch arm64 -miphoneos-version-min=15.0"

echo "==> parent: $(grep -E '^module ' "$ROOT/go.mod" | awk '{print $2}')"
echo "==> mobile/go.mod replaces:"
grep -E '^replace ' "$ROOT/mobile/go.mod" || true

# Nested module: build from mobile directory
(cd "$ROOT/mobile" && go build -a -buildmode=c-archive -tags ios \
  -ldflags="-w" -trimpath \
  -o "$OUT" \
  ./ios)
ls -lh "$OUT"
COREBUILD
chmod +x "$DEST/build_ios.sh"

echo "OpenFlux-core synced -> $DEST"
test -d "$DEST/mobile/ios"
test -f "$DEST/mobile/go.mod"
