# Cross-build environment for LinGecu: Debian 12 with amd64, arm64 and armhf
# development libraries installed side by side (Debian multiarch) plus the
# matching cross compilers. Building on bookworm keeps the glibc baseline low
# so the binaries run on most current distributions.
FROM golang:1.25.3-bookworm

RUN dpkg --add-architecture arm64 && dpkg --add-architecture armhf \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential pkg-config file wayland-protocols \
      crossbuild-essential-arm64 crossbuild-essential-armhf \
 && for a in amd64 arm64 armhf; do pkgs="$pkgs \
      libusb-1.0-0-dev:$a zlib1g-dev:$a libgl1-mesa-dev:$a libx11-dev:$a \
      libxcursor-dev:$a libxrandr-dev:$a libxinerama-dev:$a libxi-dev:$a \
      libxxf86vm-dev:$a libxkbcommon-dev:$a libwayland-dev:$a"; done \
 && apt-get install -y --no-install-recommends $pkgs \
 && rm -rf /var/lib/apt/lists/*
