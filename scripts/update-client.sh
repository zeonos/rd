#!/usr/bin/env bash
# Re-vendor RustDesk at a new release tag and re-apply our patches.
#
#   scripts/update-client.sh 1.5.4
#
# Steps: fresh upstream copy -> commit "Vendor RustDesk <tag>" -> apply
# patches/*.patch -> you resolve any rejects, build, test, commit.
set -euo pipefail
TAG="${1:?usage: $0 <rustdesk tag>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

git -C "$ROOT" diff --quiet || { echo "Working tree not clean." >&2; exit 1; }

git clone --depth 1 --branch "$TAG" https://github.com/rustdesk/rustdesk "$TMP/rustdesk"
git -C "$TMP/rustdesk" submodule update --init --depth 1
HBB="$(git -C "$TMP/rustdesk/libs/hbb_common" rev-parse --short HEAD)"

rm -rf "$ROOT/client"
mkdir "$ROOT/client"
tar -C "$TMP/rustdesk" --exclude=.git --exclude='libs/hbb_common/.git' -cf - . | tar -C "$ROOT/client" -xf -
rm -f "$ROOT/client/.gitmodules"

git -C "$ROOT" add -A client
git -C "$ROOT" commit -m "Vendor RustDesk $TAG (hbb_common $HBB) unmodified"

for p in "$ROOT"/patches/*.patch; do
  echo "Applying $(basename "$p")"
  git -C "$ROOT" apply --3way "$p" || { echo "Resolve conflicts, then commit and refresh the patch file." >&2; exit 1; }
done
echo "Patches applied. Build, test, then commit."
