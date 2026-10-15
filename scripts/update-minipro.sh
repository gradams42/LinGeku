#!/bin/sh
# Replace third_party/minipro with a fresh export of upstream minipro.
# Usage: scripts/update-minipro.sh [ref]   (default: master)
set -eu
REPO=https://gitlab.com/DavidGriffith/minipro.git
REF=${1:-master}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEST=$ROOT/third_party/minipro
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

git clone --quiet "$REPO" "$TMP/src"
git -C "$TMP/src" checkout --quiet "$REF"
COMMIT=$(git -C "$TMP/src" rev-parse HEAD)
DATE=$(git -C "$TMP/src" show -s --format=%ci HEAD)

rm -rf "$DEST"
mkdir -p "$DEST"
# git archive applies minipro's export-subst so its Makefile carries the version.
git -C "$TMP/src" archive HEAD | tar -x -C "$DEST"
printf 'repository: %s\ncommit: %s\ndate: %s\n' "$REPO" "$COMMIT" "$DATE" > "$DEST/UPSTREAM"
echo "third_party/minipro updated to $COMMIT ($DATE)"
