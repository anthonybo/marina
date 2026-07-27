#!/usr/bin/env bash
# Builds all of Marina: the dashboard bundle, the daemon binary that embeds it,
# and the menu bar app. Output lands in ./dist.
#
# Usage: bash scripts/build.sh [version]
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${1:-0.1.0}"
DIST="$HERE/dist"

echo "==> Marina $VERSION"
mkdir -p "$DIST"

# 1) Dashboard. Vite writes straight into the Go embed directory.
echo "  building dashboard…"
cd "$HERE/web"
if [ ! -d node_modules ]; then npm install --silent; fi
npm run build --silent >/dev/null
echo "  ✓ dashboard bundled"

# 2) Daemon. One static binary with the dashboard inside it.
echo "  building daemon…"
cd "$HERE/daemon"
# sourceDir lets the running daemon recognise its own repo. Without it, Marina
# lists itself in the boatyard as "not running": the installed binary lives in
# ~/.local/share and runs with cwd "/", so nothing connects it to this checkout.
go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION -X main.sourceDir=$HERE" \
  -o "$DIST/marina" .
echo "  ✓ daemon → dist/marina ($(du -h "$DIST/marina" | cut -f1))"

# 3) Menu bar agent, wrapped in a real .app so macOS treats it as an agent.
echo "  building menu bar app…"
cd "$HERE/menubar"
swift build -c release --disable-sandbox 2>/dev/null >/dev/null
APP="$DIST/Marina.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp .build/release/MarinaMenu "$APP/Contents/MacOS/Marina"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Marina</string>
  <key>CFBundleDisplayName</key><string>Marina</string>
  <key>CFBundleIdentifier</key><string>tech.bocchino.marina.menubar</string>
  <key>CFBundleExecutable</key><string>Marina</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <!-- Menu bar only: no Dock icon, no window on launch. -->
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
  <!-- Talks only to the local daemon over plain HTTP on loopback. -->
  <key>NSAppTransportSecurity</key>
  <dict>
    <key>NSAllowsLocalNetworking</key><true/>
  </dict>
</dict>
</plist>
PLIST

# An ad-hoc signature is enough for a locally built agent and keeps macOS from
# re-prompting about it on every launch.
codesign --force --sign - --timestamp=none "$APP" 2>/dev/null || \
  echo "  ! could not sign Marina.app (it will still run)"
echo "  ✓ menu bar app → dist/Marina.app"

echo
echo "Built. Install with: bash scripts/install.sh"
