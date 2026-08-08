#!/usr/bin/env bash
# Tests how install.sh resolves its options.
#
# This exists because of a specific failure. install.sh advertised itself as
# "idempotent: safe to re-run after a `git pull` to upgrade in place", and it was
# safe in the sense that nothing crashed — but a bare re-run rebuilt the launchd
# job from the defaults, so a setup installed with --lan --port80 came back
# loopback-only. The mDNS proxy kept advertising marina.local, so the name
# resolved and nothing answered, on every device except the one running Marina.
# Nothing in the test suite could see it: the bug lived entirely in a shell script.
#
# Driven through --print-config, which resolves every option and exits without
# building or installing anything, so this runs in well under a second.
set -uo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL="$HERE/scripts/install.sh"

# A throwaway HOME, so the remembered-options file under test is never the real
# one. Everything install.sh reads or writes for configuration lives under it.
SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
CONF_DIR="$SANDBOX/.local/share/marina"
mkdir -p "$CONF_DIR"

pass=0
fail=0

# config <args...> — the resolved settings for a run with these arguments.
config() { HOME="$SANDBOX" bash "$INSTALL" --print-config "$@" 2>&1; }

# want <label> <expected line> <args...>
want() {
  local label="$1" expected="$2"; shift 2
  local out
  out="$(config "$@")"
  if grep -qxF "$expected" <<<"$out"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf '  ✗ %s\n    wanted %s\n    got:\n%s\n' \
      "$label" "$expected" "$(sed 's/^/      /' <<<"$out")"
  fi
}

forget() { rm -f "$CONF_DIR/install.conf"; }
remember() { printf '%s\n' "$@" > "$CONF_DIR/install.conf"; }

printf '==> install.sh option resolution\n'

# ── Defaults ────────────────────────────────────────────────────────────────
forget
want "a fresh install is loopback-only"      "LAN=0"
want "a fresh install does not take port 80" "PORT80=0"
want "a fresh install does not serve TLS"    "TLS=0"
want "a fresh install uses 7777"             "PORT=7777"
want "a fresh install has nothing remembered" "REMEMBERED=0"

# ── Flags apply ─────────────────────────────────────────────────────────────
want "--lan turns on the network listener" "LAN=1" --lan
want "--port80 implies --lan"              "LAN=1" --port80
want "--port80 takes port 80"              "PORT80=1" --port80
want "--tls implies --port80"              "PORT80=1" --tls
want "--port is honoured"                  "PORT=7788" --port 7788

# ── The regression: a bare re-run must not revert the setup ─────────────────
remember "PORT=7777" "LAN=1" "PORT80=1"
want "a bare re-run keeps --lan"    "LAN=1"
want "a bare re-run keeps --port80" "PORT80=1"
want "a bare re-run says so"        "REMEMBERED=1"

remember "PORT=7788" "LAN=1"
want "a bare re-run keeps a custom port" "PORT=7788"

remember "PORT=7777" "NO_PROBE=3001-3013"
want "a bare re-run keeps --no-probe" "NO_PROBE=3001-3013"

# ── Explicit flags still win over what was remembered ──────────────────────
remember "PORT=7777" "LAN=1" "PORT80=1"
want "--no-lan turns the network listener off" "LAN=0" --no-lan
want "--no-lan also gives up port 80"          "PORT80=0" --no-lan
want "--no-port80 keeps --lan"                 "LAN=1" --no-port80
want "--no-port80 gives up port 80"            "PORT80=0" --no-port80

remember "PORT=7777" "LAN=1" "PORT80=1" "TLS=1"
want "--no-tls keeps port 80" "PORT80=1" --no-tls
want "--no-tls drops TLS"     "TLS=0" --no-tls

remember "PORT=7788"
want "an explicit --port beats the remembered one" "PORT=7777" --port 7777

# ── --roots must never be remembered ───────────────────────────────────────
# A remembered --roots would delete roots.json on every upgrade, wiping the
# directories added through the dashboard. That is the same class of silent
# undoing this whole file exists to prevent, so it is asserted, not assumed.
remember "PORT=7777" "LAN=1" "ROOTS=/tmp/should-not-be-remembered"
want "a remembered ROOTS is ignored" "ROOTS="

# ── The summary must not take credit for a flag ────────────────────────────
# Built from the resolved values, it announced "keeping --lan --port80" on a run
# where both had just been typed on the command line, which makes the one line
# telling you your setup survived untrustworthy.
remember "PORT=7777"
want "flags are not reported as remembered" "KEPT=" --port80
remember "PORT=7777" "LAN=1" "PORT80=1"
want "genuinely remembered settings are reported" "KEPT= --lan --port80"
want "a flag that overrides is not reported as kept" "KEPT= --lan --port80" --no-port80

# ── https must be additive, never a replacement for http ───────────────────
# --tls used to turn port 80 into a redirect to https. On a .local name the
# certificate is signed by a CA that exists on one machine, so that moved every
# other device from "loads over http" to a full-page warning — the exact outcome
# that made TLS opt-in in the first place. Redirecting is now its own flag.
forget
want "--tls serves https"                    "TLS=1" --tls
want "--tls does not redirect port 80"       "TLS_REDIRECT=0" --tls
want "--tls-redirect redirects"              "TLS_REDIRECT=1" --tls-redirect
want "--tls-redirect implies --tls"          "TLS=1" --tls-redirect
want "--no-tls drops the redirect too"       "TLS_REDIRECT=0" --tls-redirect --no-tls
want "--no-tls-redirect keeps https"         "TLS=1" --tls-redirect --no-tls-redirect
want "--no-tls-redirect stops redirecting"   "TLS_REDIRECT=0" --tls-redirect --no-tls-redirect

remember "PORT=7777" "LAN=1" "PORT80=1" "TLS=1" "TLS_REDIRECT=1"
want "a bare re-run keeps --tls-redirect" "TLS_REDIRECT=1"
want "--no-lan gives up the redirect"     "TLS_REDIRECT=0" --no-lan

# ── A corrupt file must not take the port with it ──────────────────────────
remember "PORT=not-a-number" "LAN=1"
want "a non-numeric remembered port falls back to the default" "PORT=7777"
want "a corrupt file still keeps the rest" "LAN=1"

printf '\n'
if [ "$fail" -eq 0 ]; then
  printf '✓ %d install option checks passed\n' "$pass"
else
  printf '✗ %d of %d install option checks failed\n' "$fail" "$((pass + fail))"
  exit 1
fi
