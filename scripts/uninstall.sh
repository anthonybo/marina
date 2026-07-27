#!/usr/bin/env bash
# Removes Marina completely: launchd jobs, binaries, and the PATH symlink.
# Leaves the Postgres database alone unless you pass --drop-db.
set -euo pipefail

DEST="$HOME/.local/share/marina"
BIN_DIR="$HOME/.local/bin"
AGENTS="$HOME/Library/LaunchAgents"
DROP_DB=0

[[ "${1:-}" == "--drop-db" ]] && DROP_DB=1

echo "==> Removing Marina"

for label in tech.bocchino.marina tech.bocchino.marina.menubar; do
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null && echo "  ✓ stopped $label" || true
  rm -f "$AGENTS/$label.plist"
done

pkill -f "$DEST/marina" 2>/dev/null || true
pkill -f "Marina.app/Contents/MacOS/Marina" 2>/dev/null || true

rm -f "$BIN_DIR/marina"
rm -rf "$DEST"
echo "  ✓ removed $DEST"

if [[ $DROP_DB -eq 1 ]]; then
  for psql in psql /usr/local/opt/postgresql@15/bin/psql /usr/local/opt/postgresql@16/bin/psql \
              /opt/homebrew/opt/postgresql@15/bin/psql /opt/homebrew/opt/postgresql@16/bin/psql; do
    if command -v "$psql" >/dev/null 2>&1 || [ -x "$psql" ]; then
      "$psql" -h localhost -d postgres -c 'DROP DATABASE IF EXISTS marina' >/dev/null 2>&1 \
        && echo "  ✓ dropped the marina database" && break
    fi
  done
else
  echo "  · left the marina database in place (use --drop-db to remove it)"
fi

echo "==> Done."
