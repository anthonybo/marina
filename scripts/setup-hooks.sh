#!/usr/bin/env bash
# Points this clone's git hooks at scripts/hooks, so the pre-commit check is
# version-controlled instead of living only inside one machine's .git directory.
#
# Run once per clone:
#   bash scripts/setup-hooks.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"

chmod +x "$REPO/scripts/hooks/"* 2>/dev/null || true
git -C "$REPO" config core.hooksPath scripts/hooks

echo "✓ git hooks now come from scripts/hooks"
echo "  pre-commit runs scripts/check.sh; bypass a single commit with --no-verify"
