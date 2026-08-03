#!/usr/bin/env bash
# Makes bare http://marina.local work, by redirecting port 80 to Marina's port.
#
# Run this yourself with sudo — it is the only part of Marina that needs root, and
# it is deliberately separate from the installer so that installing never asks for
# a password:
#
#   sudo bash scripts/port80.sh            # install the redirect
#   sudo bash scripts/port80.sh --remove   # take it away again
#
# Nothing about the daemon changes. It keeps running as you, on port 7777; the
# kernel's packet filter rewrites the destination port of arriving connections.
# Running the daemon itself on 80 would mean running it as root, and every dev
# server it launched would inherit that — which is not a trade worth making for a
# shorter URL.
#
# # The honest caveat
#
# pf redirects packets that *arrive on an interface*. A request from this Mac to
# its own address is delivered internally and may never pass through that rule, so
# bare marina.local is reliable from your phone and other machines, and may still
# need :7777 here. That is a macOS pf behaviour, not something this script can fix.
set -euo pipefail

PORT="${MARINA_PORT:-7777}"
ANCHOR="tech.bocchino.marina"
ANCHOR_FILE="/etc/pf.anchors/$ANCHOR"
PF_CONF="/etc/pf.conf"
PLIST="/Library/LaunchDaemons/$ANCHOR.pf.plist"

if [ "$(id -u)" != "0" ]; then
  echo "This one needs root: sudo bash scripts/port80.sh" >&2
  exit 1
fi

remove() {
  echo "==> Removing the port 80 redirect"
  rm -f "$ANCHOR_FILE"
  # Leave pf.conf valid: drop only the two lines this script added.
  if grep -q "$ANCHOR" "$PF_CONF" 2>/dev/null; then
    cp "$PF_CONF" "$PF_CONF.marina.bak"
    grep -v "$ANCHOR" "$PF_CONF" > "$PF_CONF.tmp" && mv "$PF_CONF.tmp" "$PF_CONF"
    echo "  ✓ cleaned $PF_CONF (previous kept at $PF_CONF.marina.bak)"
  fi
  if [ -f "$PLIST" ]; then
    launchctl bootout system "$PLIST" 2>/dev/null || true
    rm -f "$PLIST"
    echo "  ✓ removed the boot-time loader"
  fi
  pfctl -f "$PF_CONF" 2>/dev/null || true
  echo "==> Done. http://marina.local:$PORT still works."
  exit 0
}

[ "${1:-}" = "--remove" ] && remove

echo "==> Redirecting port 80 to $PORT"

# 1) The rule itself. Anchored rather than written into pf.conf directly, so it is
#    one grep to find and one file to delete.
mkdir -p /etc/pf.anchors
cat > "$ANCHOR_FILE" <<RULE
# Added by Marina (scripts/port80.sh). Sends arriving port 80 connections to the
# Marina daemon, which runs unprivileged on $PORT.
rdr pass inet proto tcp from any to any port = 80 -> 127.0.0.1 port $PORT
RULE
echo "  ✓ wrote $ANCHOR_FILE"

# 2) Reference it from pf.conf. Order matters to pf: rdr-anchor has to sit with the
#    other translation rules, before any filter rules.
if ! grep -q "$ANCHOR" "$PF_CONF"; then
  cp "$PF_CONF" "$PF_CONF.marina.bak"
  awk -v anchor="$ANCHOR" -v file="$ANCHOR_FILE" '
    { print }
    # Append ours immediately after the last existing rdr-anchor line.
    /^rdr-anchor/ && !done { print "rdr-anchor \"" anchor "\""; done = 1 }
    END {
      if (!done) {
        print "rdr-anchor \"" anchor "\""
      }
      print "load anchor \"" anchor "\" from \"" file "\""
    }
  ' "$PF_CONF.marina.bak" > "$PF_CONF"
  echo "  ✓ referenced it from $PF_CONF (previous kept at $PF_CONF.marina.bak)"
else
  echo "  ✓ $PF_CONF already references it"
fi

# 3) Check before enabling. A syntax error here would take the firewall down with
#    it, so validate first and put the original back if it does not parse.
if ! pfctl -n -f "$PF_CONF" 2>/tmp/marina-pf.err; then
  echo "  ✗ the resulting pf.conf does not parse — restoring the original" >&2
  sed -n '1,10p' /tmp/marina-pf.err >&2
  [ -f "$PF_CONF.marina.bak" ] && cp "$PF_CONF.marina.bak" "$PF_CONF"
  exit 1
fi

pfctl -f "$PF_CONF" >/dev/null 2>&1
pfctl -e >/dev/null 2>&1 || true   # already enabled is not an error
echo "  ✓ loaded and pf enabled"

# 4) Survive a reboot. macOS does not reload custom anchors on its own.
cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$ANCHOR.pf</string>
  <key>ProgramArguments</key>
  <array>
    <string>/sbin/pfctl</string>
    <string>-e</string>
    <string>-f</string>
    <string>$PF_CONF</string>
  </array>
  <key>RunAtLoad</key><true/>
  <!-- One shot at boot: pf keeps the rules once loaded. -->
  <key>KeepAlive</key><false/>
</dict>
</plist>
PLIST
chown root:wheel "$PLIST"
chmod 644 "$PLIST"
launchctl bootout system "$PLIST" 2>/dev/null || true
launchctl bootstrap system "$PLIST" 2>/dev/null || true
echo "  ✓ will reload at boot"

echo
echo "==> Done."
echo "    From a phone or another machine:  http://marina.local"
echo "    From this Mac:                    http://marina.local:$PORT"
echo "    Remove it with:                   sudo bash scripts/port80.sh --remove"
