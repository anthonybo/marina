#!/usr/bin/env bash
# First-run setup for a Mac that has never built Marina.
#
# install.sh assumes a working toolchain and stops at the first thing missing.
# This walks a bare machine the rest of the way: checks what's absent, tells you
# exactly what it wants to install and why, asks once, then hands over to
# install.sh. Everything it does is idempotent, so re-running is harmless.
#
#   bash scripts/setup.sh                  # check, ask, install, then set Marina up
#   bash scripts/setup.sh --check          # report only; changes nothing
#   bash scripts/setup.sh --yes            # don't ask (for unattended runs)
#   bash scripts/setup.sh --no-postgres    # skip Postgres entirely
#
# Any other arguments are passed through to install.sh, so this works too:
#   bash scripts/setup.sh --roots ~/projects,~/work
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
CHECK_ONLY=0
ASSUME_YES=0
WANT_POSTGRES=1
PASSTHROUGH=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) CHECK_ONLY=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --no-postgres) WANT_POSTGRES=0; shift ;;
    -h|--help) awk 'NR>1 && /^#/ {sub(/^# ?/, ""); print; next} NR>1 {exit}' "$0"; exit 0 ;;
    *) PASSTHROUGH+=("$1"); shift ;;
  esac
done

# Postgres only ever stores pins, nicknames, and history. Skipping it costs you
# those; it costs you nothing else, so it is never a hard requirement.
POSTGRES_FORMULA="postgresql@15"
BIN_DIR="$HOME/.local/bin"

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }
miss() { printf '  \033[33m•\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$1"; }

ask() {
  # $1 = question. Yes unless the user says otherwise.
  [ "$ASSUME_YES" = 1 ] && return 0
  local reply
  printf '\n%s [Y/n] ' "$1"
  # No terminal (piped, or CI) counts as "no": better to print what to do next
  # than to install toolchains nobody confirmed.
  if ! read -r reply </dev/tty 2>/dev/null; then
    printf '\n  (no terminal to ask on — declining)\n'
    return 1
  fi
  [[ -z "$reply" || "$reply" =~ ^[Yy] ]]
}

bold "==> Marina setup"

# ── 1. The machine itself ────────────────────────────────────────────────────
# Marina reads listening sockets with lsof and ships a menu bar agent, so it is
# macOS-only by design rather than by accident.
if [ "$(uname -s)" != "Darwin" ]; then
  bad "Marina is macOS-only (it uses lsof, launchd, and a Swift menu bar agent)."
  exit 1
fi
MACOS_MAJOR="$(sw_vers -productVersion | cut -d. -f1)"
if [ "$MACOS_MAJOR" -lt 13 ]; then
  bad "macOS 13 or newer required (the menu bar agent targets 13.0). Found $(sw_vers -productVersion)."
  exit 1
fi
ok "macOS $(sw_vers -productVersion) on $(uname -m)"

# ── 2. Work out what's missing before changing anything ──────────────────────
NEED_BREW=0 NEED_GO=0 NEED_NODE=0 NEED_PG=0 NEED_CLT=0 NEED_PATH=0

# Swift comes from the Command Line Tools. `xcode-select -p` can point at a
# directory that exists while swiftc is still absent, so check the compiler.
if xcode-select -p >/dev/null 2>&1 && command -v swift >/dev/null 2>&1; then
  ok "Swift toolchain ($(swift --version 2>/dev/null | head -1 | cut -c1-48))"
else
  miss "Xcode Command Line Tools (needed for the menu bar app)"
  NEED_CLT=1
fi

# Homebrew is only needed for what's missing; a machine that already has Go and
# Node does not need it at all.
if command -v brew >/dev/null 2>&1; then
  ok "Homebrew ($(brew --version | head -1))"
else
  miss "Homebrew"
  NEED_BREW=1
fi

if command -v go >/dev/null 2>&1; then
  ok "Go ($(go version | awk '{print $3}'))"
else
  miss "Go (builds the daemon)"
  NEED_GO=1
fi

if command -v node >/dev/null 2>&1; then
  ok "Node ($(node --version))"
else
  miss "Node (builds the dashboard)"
  NEED_NODE=1
fi

if [ "$WANT_POSTGRES" = 1 ]; then
  # `command -v psql` is the wrong test: postgresql@15 is keg-only, so a working
  # server is normal while psql is absent from PATH. Ask the three questions that
  # actually matter — is anything serving 5432, is the formula installed, is a
  # client on PATH — and treat any yes as present.
  if lsof -nP -iTCP:5432 -sTCP:LISTEN >/dev/null 2>&1; then
    ok "Postgres (serving on 5432)"
  elif brew list --formula 2>/dev/null | grep -q '^postgresql@'; then
    ok "Postgres (installed, not running — start it with: brew services start $POSTGRES_FORMULA)"
  elif command -v psql >/dev/null 2>&1; then
    ok "Postgres client present"
  else
    miss "Postgres — optional: without it, pins, nicknames, and history are not saved"
    NEED_PG=1
  fi
else
  ok "Postgres skipped (--no-postgres)"
fi

# `marina status` from any shell needs ~/.local/bin on PATH. install.sh only
# warns about this; here we can actually fix it.
case ":$PATH:" in
  *":$BIN_DIR:"*) ok "$BIN_DIR is on PATH" ;;
  *) miss "$BIN_DIR is not on PATH (needed for the 'marina' command)"; NEED_PATH=1 ;;
esac

TOTAL=$((NEED_BREW + NEED_GO + NEED_NODE + NEED_PG + NEED_CLT + NEED_PATH))

if [ "$CHECK_ONLY" = 1 ]; then
  echo
  if [ "$TOTAL" = 0 ]; then
    bold "Everything Marina needs is present. Run: npm start"
  else
    bold "$TOTAL item(s) above need attention. Re-run without --check to fix them."
  fi
  exit 0
fi

if [ "$TOTAL" = 0 ]; then
  echo
  bold "Toolchain complete — going straight to install."
else
  # ── 3. Command Line Tools. A GUI installer we cannot wait on. ─────────────
  if [ "$NEED_CLT" = 1 ]; then
    echo
    bold "Xcode Command Line Tools"
    echo "  macOS installs these through its own window, which this script cannot"
    echo "  drive or wait for. Accept the dialog, let it finish, then run setup again."
    if ask "  Open the Command Line Tools installer now?"; then
      xcode-select --install 2>/dev/null || echo "  (already requested — finish the dialog, then re-run)"
    fi
    echo
    echo "Re-run when it has finished:  bash scripts/setup.sh"
    exit 1
  fi

  # ── 4. Homebrew ───────────────────────────────────────────────────────────
  if [ "$NEED_BREW" = 1 ]; then
    echo
    bold "Homebrew"
    echo "  Needed to install: $( [ "$NEED_GO" = 1 ] && printf 'go '; [ "$NEED_NODE" = 1 ] && printf 'node '; [ "$NEED_PG" = 1 ] && printf '%s' "$POSTGRES_FORMULA" )"
    echo "  Installs from https://brew.sh — this will ask for your password."
    if ask "  Install Homebrew?"; then
      /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
      # A fresh install is not on PATH in this shell yet.
      for candidate in /opt/homebrew/bin/brew /usr/local/bin/brew; do
        [ -x "$candidate" ] && eval "$("$candidate" shellenv)" && break
      done
    else
      echo
      echo "Without Homebrew, install these yourself and re-run:"
      [ "$NEED_GO" = 1 ] && echo "  Go    https://go.dev/dl/"
      [ "$NEED_NODE" = 1 ] && echo "  Node  https://nodejs.org/"
      exit 1
    fi
  fi

  # ── 5. Toolchain packages ─────────────────────────────────────────────────
  FORMULAE=()
  [ "$NEED_GO" = 1 ] && FORMULAE+=("go")
  [ "$NEED_NODE" = 1 ] && FORMULAE+=("node")
  [ "$NEED_PG" = 1 ] && FORMULAE+=("$POSTGRES_FORMULA")

  if [ "${#FORMULAE[@]}" -gt 0 ]; then
    echo
    bold "Installing: ${FORMULAE[*]}"
    if ask "  Run brew install ${FORMULAE[*]}?"; then
      brew install "${FORMULAE[@]}"
      if [ "$NEED_PG" = 1 ]; then
        # Keg-only formula: its psql is not linked into PATH.
        PG_BIN="$(brew --prefix "$POSTGRES_FORMULA")/bin"
        brew services start "$POSTGRES_FORMULA" || true
        echo "  ✓ $POSTGRES_FORMULA started at login via brew services"
        case ":$PATH:" in
          *":$PG_BIN:"*) ;;
          *) export PATH="$PG_BIN:$PATH"
             echo "  ! $POSTGRES_FORMULA is keg-only. To use psql directly, add to your shell profile:"
             echo "      export PATH=\"$PG_BIN:\$PATH\"" ;;
        esac
      fi
    else
      echo "Nothing installed. Marina cannot build without ${FORMULAE[*]}."
      exit 1
    fi
  fi

  # ── 6. PATH for the `marina` command ──────────────────────────────────────
  if [ "$NEED_PATH" = 1 ]; then
    # Append to the profile the user's shell actually reads, and only once.
    case "$(basename "${SHELL:-/bin/zsh}")" in
      zsh) PROFILE="$HOME/.zshrc" ;;
      bash) PROFILE="$HOME/.bash_profile" ;;
      *) PROFILE="" ;;
    esac
    LINE='export PATH="$HOME/.local/bin:$PATH"'
    if [ -n "$PROFILE" ] && ! grep -qsF '.local/bin' "$PROFILE"; then
      if ask "  Add ~/.local/bin to your PATH in $(basename "$PROFILE")?"; then
        printf '\n# Added by Marina setup so `marina status` works from any shell.\n%s\n' "$LINE" >> "$PROFILE"
        ok "updated $PROFILE (open a new shell to pick it up)"
      fi
    elif [ -z "$PROFILE" ]; then
      echo "  ! add this to your shell profile yourself: $LINE"
    fi
    export PATH="$BIN_DIR:$PATH"
  fi
fi

# ── 7. Hand over to the installer ────────────────────────────────────────────
echo
bold "==> Building and installing Marina"
bash "$HERE/scripts/install.sh" ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"}
