#!/usr/bin/env bash
# Installs Marina as a login-time daemon plus a menu bar agent.
# Idempotent: safe to re-run after a `git pull` to upgrade in place.
#
#   bash scripts/install.sh                        # build, install, start
#   bash scripts/install.sh --port 7788            # use a different port
#   bash scripts/install.sh --roots ~/projects,~/work
#   bash scripts/install.sh --no-probe 3001-3013   # never send these ports HTTP
#   bash scripts/install.sh --lan                  # let other devices view it
#   bash scripts/install.sh --port80               # ...at http://marina.local, no port
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
LAN=""
PORT80=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --roots) ROOTS="$2"; shift 2 ;;
    --no-probe) NO_PROBE="$2"; shift 2 ;;
    # Let other devices load the dashboard. Changes stay refused from anything
    # but this machine, so this grants a view and links, not control.
    --lan) LAN="1"; shift ;;
    # Serve bare http://marina.local as well, on port 80. launchd binds the
    # privileged port and hands the descriptor to the daemon, which keeps running
    # as you — nothing here needs root. Off by default because port 80 is a
    # popular port and taking it should be a decision.
    --port80) PORT80="1"; LAN="1"; shift ;;
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

# Directories added in the dashboard live in roots.json and take precedence over
# --roots, so that a UI edit is not silently undone by the next upgrade. Passing
# --roots explicitly is the deliberate override, and has to win — otherwise the
# flag would appear to do nothing at all.
if [ -n "$ROOTS" ] && [ -f "$DEST/roots.json" ]; then
  rm -f "$DEST/roots.json"
  echo "  ! --roots given: cleared the directory list saved from the dashboard"
fi

# A certificate the browser already trusts, so the dashboard is not permanently
# labelled "Not Secure". mkcert signs with a root it installed into the system
# keychain, which is what makes this trusted rather than merely encrypted — a
# self-signed certificate would trade one warning for a louder one.
#
# Names, not addresses: the address changes with the DHCP lease and a certificate
# baked to one would silently go stale, which is the same reason marina.local
# exists at all.
if [ -n "$PORT80" ]; then
  if command -v mkcert >/dev/null 2>&1; then
    mkdir -p "$DEST/tls"
    MDNS_HOST="$(scutil --get LocalHostName 2>/dev/null || echo "$(hostname -s)").local"
    if mkcert -cert-file "$DEST/tls/cert.pem" -key-file "$DEST/tls/key.pem" \
         "${MDNS_NAME:-marina}.local" "$MDNS_HOST" localhost 127.0.0.1 ::1 >/dev/null 2>&1; then
      chmod 600 "$DEST/tls/key.pem"
      echo "  ✓ certificate for ${MDNS_NAME:-marina}.local, $MDNS_HOST (trusted by this Mac)"
      # Other devices reject that certificate until they trust the CA behind it, so
      # copy the CA's *public* half where the daemon can hand it out. The private
      # key stays where mkcert put it — with it, anyone could mint a trusted
      # certificate for any site on earth for whoever installed the CA.
      if CAROOT="$(mkcert -CAROOT 2>/dev/null)" && [ -f "$CAROOT/rootCA.pem" ]; then
        cp "$CAROOT/rootCA.pem" "$DEST/tls/ca.pem"
        chmod 644 "$DEST/tls/ca.pem"
        echo "    other devices: http://${MDNS_NAME:-marina}.local/trust to stop the warning"
      fi
    else
      echo "  ! mkcert failed; the dashboard will be served over plain HTTP"
    fi
  else
    echo "  ! mkcert not installed, so https is unavailable (brew install mkcert && mkcert -install)"
    echo "    without it the browser will call the dashboard \"Not Secure\""
  fi
fi

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
$( [ -n "$LAN" ] && printf '    <key>MARINA_LAN</key><string>1</string>\n' )
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
$( [ -n "$PORT80" ] && cat <<'SOCK'
  <!-- launchd binds these before starting the job, so the daemon gets listeners on
       privileged ports without ever being privileged itself. The key names must
       match what the daemon asks for: launchsock.Listeners("Listeners") and
       ("TLS"). Port 80 only redirects to 443; the app is served over https. -->
  <key>Sockets</key>
  <dict>
    <key>Listeners</key>
    <dict>
      <key>SockServiceName</key><string>80</string>
      <key>SockType</key><string>stream</string>
    </dict>
    <key>TLS</key>
    <dict>
      <key>SockServiceName</key><string>443</string>
      <key>SockType</key><string>stream</string>
    </dict>
  </dict>
SOCK
)
  <key>StandardOutPath</key><string>$DEST/marina.log</string>
  <key>StandardErrorPath</key><string>$DEST/marina.log</string>
</dict>
</plist>
PLIST
}

reload() {
  local label="$1" plist="$2" domain="gui/$(id -u)" i
  launchctl bootout "$domain/$label" 2>/dev/null || true

  # bootout returns before launchd has finished tearing the job down, so a
  # bootstrap issued immediately after can fail with "operation already in
  # progress" — and under `set -e` that aborts the install with the daemon
  # unloaded, which is the worst possible outcome for an in-place upgrade.
  # Wait for the job to actually go, then retry the bootstrap.
  for i in $(seq 1 40); do
    launchctl print "$domain/$label" >/dev/null 2>&1 || break
    sleep 0.25
  done

  for i in $(seq 1 10); do
    if launchctl bootstrap "$domain" "$plist" 2>/dev/null; then
      launchctl kickstart -k "$domain/$label" 2>/dev/null || true
      return 0
    fi
    # Already loaded is a success for our purposes: kickstart will restart it
    # with the new binary.
    if launchctl print "$domain/$label" >/dev/null 2>&1; then
      launchctl kickstart -k "$domain/$label" 2>/dev/null || true
      return 0
    fi
    sleep 0.5
  done

  echo "ERROR: could not register $label with launchd." >&2
  echo "       Try: launchctl bootout $domain/$label && bash scripts/install.sh" >&2
  return 1
}

write_plist "$DAEMON_LABEL" "$AGENTS/$DAEMON_LABEL.plist" "$DEST/marina" "serve"
reload "$DAEMON_LABEL" "$AGENTS/$DAEMON_LABEL.plist"
echo "  ✓ daemon registered with launchd (starts at login)"
[ -n "$ROOTS" ] && echo "  ✓ scanning for projects in: $ROOTS"
[ -n "$NO_PROBE" ] && echo "  ✓ HTTP probing disabled for ports: $NO_PROBE"
[ -n "$LAN" ] && echo "  ✓ listening on the network too — other devices can view, not change"
[ -n "$PORT80" ] && echo "  ✓ https://${MDNS_NAME:-marina}.local — no port, and a real padlock"

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
