#!/usr/bin/env bash
# Installs Marina as a login-time daemon plus a menu bar agent.
# Idempotent: safe to re-run after a `git pull` to upgrade in place.
#
#   bash scripts/install.sh                        # build, install, start
#   bash scripts/install.sh --port 7788            # use a different port
#   bash scripts/install.sh --roots ~/projects,~/work
#   bash scripts/install.sh --no-probe 3001-3013   # never send these ports HTTP
#
# Uninstall with: bash scripts/uninstall.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="0.1.0"
PORT=7777
DEST="$HOME/.local/share/marina"
BIN_DIR="$HOME/.local/bin"
AGENTS="$HOME/Library/LaunchAgents"
DAEMON_LABEL="tech.bocchino.marina"
MENU_LABEL="tech.bocchino.marina.menubar"
ROOTS=""
NO_PROBE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --roots) ROOTS="$2"; shift 2 ;;
    --no-probe) NO_PROBE="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

ADDR="127.0.0.1:$PORT"
echo "==> Installing Marina $VERSION on $ADDR"

# 1) Prerequisites. Postgres is intentionally *not* required: the daemon runs
#    without it and connects whenever it becomes available.
command -v go >/dev/null   || { echo "ERROR: go not found (brew install go)"; exit 1; }
command -v node >/dev/null || { echo "ERROR: node not found (brew install node)"; exit 1; }
command -v swift >/dev/null || { echo "ERROR: swift not found (install Xcode)"; exit 1; }
echo "  ✓ toolchain present"

if ! /usr/sbin/lsof -nP -iTCP -sTCP:LISTEN >/dev/null 2>&1; then
  echo "  ! lsof returned an error — Marina needs it to see listening ports"
fi

# A busy port is only a problem if it isn't already Marina. Re-running this
# script to upgrade in place is the expected path, and that means the previous
# daemon is still listening when we get here.
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  if curl -sf --max-time 2 "http://$ADDR/healthz" 2>/dev/null | grep -q '"ok"'; then
    echo "  ✓ Marina already on $PORT — upgrading in place"
  else
    echo "ERROR: port $PORT is in use by something that isn't Marina."
    echo "       Pick another port with --port, or stop whatever holds it:"
    echo "       lsof -nP -iTCP:$PORT -sTCP:LISTEN"
    exit 1
  fi
fi

# 2) Build.
bash "$HERE/scripts/build.sh" "$VERSION"

# 3) Install files.
mkdir -p "$DEST" "$BIN_DIR" "$AGENTS"
cp "$HERE/dist/marina" "$DEST/marina"
chmod +x "$DEST/marina"
rm -rf "$DEST/Marina.app"
cp -R "$HERE/dist/Marina.app" "$DEST/Marina.app"

# A symlink on PATH so `marina status` works from any shell.
ln -sf "$DEST/marina" "$BIN_DIR/marina"
echo "  ✓ installed to $DEST"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "  ! add $BIN_DIR to your PATH to use the 'marina' command" ;;
esac

# 4) launchd jobs. Both RunAtLoad and KeepAlive so they survive a reboot and a
#    crash. Unlike the cmux socket, nothing here needs a terminal session.
write_plist() {
  local label="$1" plist="$2"; shift 2
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array>
$(for arg in "$@"; do printf '    <string>%s</string>\n' "$arg"; done)
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <!-- Interactive, not Background.
       ProcessType applies a QoS class that every descendant inherits, and
       "Background" is the lowest scheduling band — the one Spotlight indexing
       runs in. Apps launched from Marina inherited it and crawled: measured
       PRI 4 against PRI 31 for the same kind of dev server started from a
       terminal, with multi-second event-loop stalls that used almost no CPU.
       It also lowered their jetsam priority, making them likelier to be killed
       under memory pressure. Marina's own cost is ~0.5% of one core, so there
       was never anything to gain by throttling it. -->
  <key>ProcessType</key><string>Interactive</string>
  <!-- Apps you start from Marina are its descendants. Without this, tearing the
       job down (which is what upgrading does) takes your dev servers with it,
       even though they were setsid'd into their own session. -->
  <key>AbandonProcessGroup</key><true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MARINA_ADDR</key><string>$ADDR</string>
$( [ -n "$ROOTS" ] && printf '    <key>MARINA_ROOTS</key><string>%s</string>\n' "$ROOTS" )
$( [ -n "$NO_PROBE" ] && printf '    <key>MARINA_NO_PROBE</key><string>%s</string>\n' "$NO_PROBE" )
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
  <key>StandardOutPath</key><string>$DEST/marina.log</string>
  <key>StandardErrorPath</key><string>$DEST/marina.log</string>
</dict>
</plist>
PLIST
}

reload() {
  local label="$1" plist="$2"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist"
  launchctl kickstart -k "gui/$(id -u)/$label" 2>/dev/null || true
}

write_plist "$DAEMON_LABEL" "$AGENTS/$DAEMON_LABEL.plist" "$DEST/marina" "serve"
reload "$DAEMON_LABEL" "$AGENTS/$DAEMON_LABEL.plist"
echo "  ✓ daemon registered with launchd (starts at login)"
[ -n "$ROOTS" ] && echo "  ✓ scanning for projects in: $ROOTS"
[ -n "$NO_PROBE" ] && echo "  ✓ HTTP probing disabled for ports: $NO_PROBE"

write_plist "$MENU_LABEL" "$AGENTS/$MENU_LABEL.plist" \
  "$DEST/Marina.app/Contents/MacOS/Marina"
reload "$MENU_LABEL" "$AGENTS/$MENU_LABEL.plist"
echo "  ✓ menu bar app registered with launchd (starts at login)"

# 5) Wait for the daemon to answer, then report.
printf "  waiting for daemon"
for _ in $(seq 1 25); do
  if curl -sf "http://$ADDR/healthz" >/dev/null 2>&1; then
    echo " — up"
    break
  fi
  printf "."
  sleep 0.4
done
echo

if curl -sf "http://$ADDR/healthz" >/dev/null 2>&1; then
  "$DEST/marina" status -addr "$ADDR" || true
  cat <<EOF

==> Marina is running.
    Dashboard:  http://$ADDR
    Menu bar:   look for the waves icon at the top right
    Status:     marina status
    Logs:       tail -f $DEST/marina.log
    Uninstall:  bash scripts/uninstall.sh
EOF
else
  echo "ERROR: the daemon did not come up. Check $DEST/marina.log"
  exit 1
fi
