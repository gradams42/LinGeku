#!/bin/sh
# Runs inside the cross-build container: builds one architecture from a
# read-only copy of the repo at /src and writes a tarball to /out.
set -eu
: "${ARCH:?}" "${VERSION:?}"

case "$ARCH" in
  amd64) TRIPLE=x86_64-linux-gnu;    export GOARCH=amd64 ;;
  arm64) TRIPLE=aarch64-linux-gnu;   export GOARCH=arm64 ;;
  armhf) TRIPLE=arm-linux-gnueabihf; export GOARCH=arm GOARM=7 ;;
  *) echo "unsupported ARCH $ARCH" >&2; exit 1 ;;
esac
export GOOS=linux CGO_ENABLED=1 CC="$TRIPLE-gcc"
export PKG_CONFIG_LIBDIR="/usr/lib/$TRIPLE/pkgconfig:/usr/share/pkgconfig"

# Work on a clean copy so host build outputs never leak into the target build.
W=/tmp/work
rm -rf "$W" && mkdir -p "$W"
(cd /src && tar -cf - --exclude=./.git --exclude=./build --exclude=./internal/bundle/assets \
   --exclude='*.o' --exclude='*.a' --exclude=./third_party/minipro/minipro \
   --exclude=./third_party/minipro/src/version.c --exclude=./third_party/minipro/src/version.h .) \
  | tar -xf - -C "$W"
cd "$W"

make lingecu VERSION="$VERSION"

echo "== $ARCH =="
file third_party/minipro/minipro build/lingecu

NAME="lingecu-$VERSION-linux-$ARCH"
PKG="/tmp/$NAME"
rm -rf "$PKG" && mkdir -p "$PKG"
cp build/lingecu README.md LICENSE packaging/lingecu.desktop third_party/minipro/udev/60-minipro.rules "$PKG/"
tar -C /tmp -czf "/out/$NAME.tar.gz" "$NAME"
(cd /out && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
[ -n "${HOST_UID:-}" ] && chown "$HOST_UID:${HOST_GID:-$HOST_UID}" "/out/$NAME.tar.gz" "/out/$NAME.tar.gz.sha256"
echo "wrote /out/$NAME.tar.gz"
