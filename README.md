# Fix TestInstallOnVDS / GitHub 404

## Cause
`node-install.sh` asked GitHub for releases under `p1neappleXpress/OpenFlux`.
Fake server in `integration_test.go` only served that path (or mismatched after fork).
curl → **404** → `GitHub не ответил на список релизов`.

## Changes
- `deploy/node-install.sh` → `RELEASE_REPO="typeoftypes/OpenFlux2"`
- `provision/integration_test.go` → fake repo `/typeoftypes/OpenFlux2/`, force RELEASE_REPO in test script
- `provision/pin.go` → PinnedRepo + PinnedSHA256=`12575191f453326e6a2f558b29eb5055d1b83990b173cd10eec320db5879de39`

## Apply
```bash
cd ~/github/OpenFlux2
unzip -o OpenFlux-provision-repo-fix.zip
git add deploy/node-install.sh provision/
# set PinnedCommit after commit:
#   git log -1 --format=%H -- deploy/node-install.sh
git commit -m "provision: point node-install releases at typeoftypes/OpenFlux2"
```

Note: auto-update will look for **node-v*** releases on **your** GitHub repo.
Publish releases there or updater will report no newer release.
