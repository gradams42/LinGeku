#!/bin/sh
# Build release tarballs for several Linux architectures in Docker.
# Usage: scripts/build-release.sh [arch ...]   (default: amd64 arm64 armhf)
# Set VERSION to override the version string.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
ARCHES=${*:-amd64 arm64 armhf}
VERSION=${VERSION:-$(git -C "$ROOT" describe --tags --dirty 2>/dev/null ||
  sed -n 's/^var Version = "\([^"]*\)".*/\1/p' "$ROOT/internal/ui/app.go")}
IMAGE=lingecu-cross:bookworm
OUT=$ROOT/build/release

docker build -t "$IMAGE" "$ROOT/packaging/cross"
mkdir -p "$OUT"
for arch in $ARCHES; do
  echo "### building linux/$arch ($VERSION)"
  docker run --rm \
    -v "$ROOT:/src:ro" -v "$OUT:/out" \
    -v lingecu-gomod:/go/pkg/mod -v lingecu-gocache:/root/.cache/go-build \
    -e ARCH="$arch" -e VERSION="$VERSION" -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
    "$IMAGE" sh /src/packaging/cross/build-in-container.sh
done
ls -l "$OUT"
