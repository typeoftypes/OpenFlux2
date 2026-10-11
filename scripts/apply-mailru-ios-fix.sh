#!/bin/bash
# Apply mailru + build_ios fix into the current OpenFlux / OpenFlux2 repo.
# Usage: from repo root:  bash scripts/apply-mailru-ios-fix.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ ! -f go.mod ]; then
  echo "error: run from repo root (go.mod missing)" >&2
  exit 1
fi
MOD="$(grep -E '^module ' go.mod | awk '{print $2}')"
echo "==> module: $MOD"

if [ ! -f transport/mailru/mailru.go ]; then
  echo "error: transport/mailru/mailru.go missing" >&2
  exit 1
fi

# 1) Install mailru.go and rewrite imports to THIS module
if [ -f "$(dirname "$0")/../transport/mailru/mailru.go" ] && [ "$ROOT" != "$(cd "$(dirname "$0")/.." && pwd)" ]; then
  :
fi
# Expect mailru.go already in place when unzipped into repo; rewrite imports:
python3 - "$MOD" <<'PY'
import sys, re
from pathlib import Path
mod = sys.argv[1]
p = Path("transport/mailru/mailru.go")
t = p.read_text()
# Any previous module path -> current
t = re.sub(r'"github\.com/[^"]+/transport"', f'"{mod}/transport"', t)
t = re.sub(r'"github\.com/[^"]+/utils"', f'"{mod}/utils"', t)
t = re.sub(r'"github\.com/[^"]+/netbind"', f'"{mod}/netbind"', t)
t = re.sub(r'"universal-bypass-tool/transport"', f'"{mod}/transport"', t)
t = re.sub(r'"universal-bypass-tool/utils"', f'"{mod}/utils"', t)
# Force Safari UA
t = re.sub(
    r'const mailruUserAgent = "[^"]+"',
    'const mailruUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6 Mobile/15E148 Safari/604.1"',
    t,
)
p.write_text(t)
print("mailru.go imports ->", mod)
print("UA:", [l for l in t.splitlines() if "mailruUserAgent =" in l][0][:100])
PY

# 2) Rewrite ALL mobile + transport imports from foreign modules to this module
echo "==> rewriting imports under mobile/ transport/ to $MOD"
python3 - "$MOD" <<'PY'
import sys, re
from pathlib import Path
mod = sys.argv[1]
# Map known foreign module roots to current
foreign = [
    r'github\.com/p1neappleXpress/OpenFlux',
    r'github\.com/typeoftypes/OpenFlux2',
    r'universal-bypass-tool',
]
pat = re.compile(r'"((?:' + '|'.join(foreign) + r')/[^"]*)"')
changed = 0
for path in list(Path("mobile").rglob("*.go")) + list(Path("transport").rglob("*.go")):
    t = path.read_text(encoding="utf-8", errors="replace")
    def repl(m):
        rest = m.group(1)
        for f in ("github.com/p1neappleXpress/OpenFlux", "github.com/typeoftypes/OpenFlux2", "universal-bypass-tool"):
            if rest.startswith(f + "/") or rest == f:
                return '"' + mod + rest[len(f):] + '"'
        return m.group(0)
    nt = pat.sub(repl, t)
    if nt != t:
        path.write_text(nt)
        changed += 1
print(f"updated {changed} files")
PY

# 3) Ensure build_ios.sh is the local one
if [ -f build_ios.sh ]; then
  chmod +x build_ios.sh
fi

# 4) Prove go list resolves LOCAL mailru
echo "==> go list transport/mailru"
( cd "$ROOT" && go list -f '{{.Dir}}' ./transport/mailru )
echo "==> UA in resolved package:"
UAFILE="$(go list -f '{{.Dir}}' ./transport/mailru)/mailru.go"
grep 'mailruUserAgent =' "$UAFILE"

# 5) Hint
echo ""
echo "Next (on macOS / CI macos-latest):"
echo "  ./build_ios.sh"
echo "  cp -f output/ios/liboflux.a output/ios/liboflux.h ios-app/Lib/"
echo "  # then archive / TestFlight"
echo ""
echo "In device log after connect you MUST see:"
echo "  [M-DOCS] start ... UA=Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 ..."
