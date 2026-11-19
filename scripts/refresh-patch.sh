#!/usr/bin/env bash
# Regenerate patches/0001-rd-fork.patch: everything under client/ that differs
# from upstream RustDesk (the vendor commit plus the upstream files its
# .gitignore hid, see update-client.sh).
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
VENDOR="${VENDOR:-$(git log --format=%H --grep='^Vendor RustDesk' -1)}"
ASSETS="$(git log --format=%H --grep='track the upstream assets that .gitignore hid' -1 || true)"
TMP="$(mktemp -d)"
trap 'git worktree remove --force "$TMP" >/dev/null 2>&1 || true; git branch -D rd-patch-base >/dev/null 2>&1 || true' EXIT
git worktree add -q -b rd-patch-base "$TMP" "$VENDOR"
if [ -n "$ASSETS" ]; then
  ( cd "$TMP" && git checkout "$ASSETS" -- $(git -C "$ROOT" diff --name-only "$ASSETS^" "$ASSETS") \
    && git add -f -A client && git commit -q -m base )
fi
BASE="$(git -C "$TMP" rev-parse HEAD)"
git diff --binary "$BASE" HEAD -- client > patches/0001-rd-fork.patch
( cd "$TMP" && git apply --check "$ROOT/patches/0001-rd-fork.patch" )
echo "patches/0001-rd-fork.patch: $(grep -c '^diff --git' patches/0001-rd-fork.patch) files, applies cleanly"
