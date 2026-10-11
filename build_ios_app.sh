#!/bin/bash
# Submodule core -> Lib -> XcodeGen (full app prep)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$ROOT/ios-app"

"$ROOT/build_ios.sh"

echo "==> XcodeGen"
cd "$APP_DIR"
if command -v xcodegen >/dev/null 2>&1; then
  xcodegen generate
else
  echo "install xcodegen: brew install xcodegen" >&2
  [ -d OpenFlux.xcodeproj ] || exit 1
fi
echo "Open $APP_DIR/OpenFlux.xcodeproj"
