#!/usr/bin/env bash
# Runs everything that can fail: Go vet and tests, TypeScript, and a production
# build of both halves. This is what to run before committing.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
fail=0

step() { printf '\n==> %s\n' "$1"; }

# First, because it is the fastest and guards the thing with no other safety net:
# install.sh is a shell script, so nothing else in this file can see a regression
# in it, and the one it had took marina.local off the network on every device.
step "install options"
("$HERE/scripts/test-install.sh") || fail=1

step "go vet"
(cd "$HERE/daemon" && go vet ./...) || fail=1

step "go test"
(cd "$HERE/daemon" && go test ./... ) || fail=1

step "typescript"
(cd "$HERE/web" && [ -d node_modules ] || npm install --silent)
(cd "$HERE/web" && npx tsc --noEmit) || fail=1

step "vite build"
(cd "$HERE/web" && npm run build --silent >/dev/null) || fail=1

step "swift build"
(cd "$HERE/menubar" && swift build -c release 2>&1 | grep -E 'error|warning' && fail=1 || true)

step "go build"
(cd "$HERE/daemon" && go build -o /dev/null .) || fail=1

if [[ $fail -eq 0 ]]; then
  printf '\n✓ all checks passed\n'
else
  printf '\n✗ checks failed\n'
  exit 1
fi
