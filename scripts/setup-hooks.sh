#!/usr/bin/env bash
#
# Install repository-managed git hooks.
#
# Usage: ./scripts/setup-hooks.sh
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "Setting git hooksPath to .githooks/"
git -C "$REPO_ROOT" config core.hooksPath .githooks

chmod +x "$REPO_ROOT/.githooks/"*

echo "Done. Pre-commit hook is active."
echo ""
echo "Checks that run before each commit:"
echo "  1. gofmt (staged Go files)"
echo "  2. golangci-lint run ./..."
echo "  3. go test ./..."
echo "  4. python3 scripts/validate_versions.py (changelog format)"
