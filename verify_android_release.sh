#!/bin/bash
set -euo pipefail
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
APK="${1:-$ROOT/dist/OpenFlux-android-universal-release.apk}"
if [ ! -f "$APK" ]; then
  echo "APK not found: $APK" >&2
  echo "Build with: BUILD_TYPE=release ./build_android_app.sh" >&2
  exit 2
fi
APKSIGNER="${APKSIGNER:-$(command -v apksigner || true)}"
if [ -z "$APKSIGNER" ]; then
  echo "apksigner not found; APK cannot be verified here." >&2
  exit 3
fi
"$APKSIGNER" verify --verbose --print-certs "$APK"
printf '\nSHA-256: '
sha256sum "$APK" | awk '{print $1}'
printf 'Package/manifest check: '
unzip -t "$APK" >/dev/null && echo OK
