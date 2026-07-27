#!/usr/bin/env bash
# Development mode: the Go daemon on :7777 plus Vite with hot reload on :5199.
# Vite proxies /api to the daemon, so the dashboard behaves exactly as it will
# once embedded. Ctrl-C stops both.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
ADDR="${MARINA_ADDR:-127.0.0.1:7777}"

# Don't fight the installed daemon for the port.
if lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Port ${ADDR##*:} is busy — if that's the installed Marina daemon, stop it first:"
  echo "  launchctl bootout gui/\$(id -u)/tech.bocchino.marina"
  exit 1
fi

cleanup() {
  [[ -n "${DAEMON_PID:-}" ]] && kill "$DAEMON_PID" 2>/dev/null || true
  [[ -n "${VITE_PID:-}" ]] && kill "$VITE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "==> daemon on http://$ADDR"
cd "$HERE/daemon"
MARINA_ADDR="$ADDR" go run . -v &
DAEMON_PID=$!

cd "$HERE/web"
[ -d node_modules ] || npm install
echo "==> dashboard on http://localhost:5199 (hot reload)"
npm run dev -- --host 127.0.0.1 &
VITE_PID=$!

wait
