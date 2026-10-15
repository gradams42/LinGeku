# LinGecu — GUI for XGecu programmers, built together with the vendored
# minipro (third_party/minipro) into a single self-contained binary.

PREFIX  ?= /usr/local
BINDIR  := $(DESTDIR)$(PREFIX)/bin
APPDIR  := $(DESTDIR)$(PREFIX)/share/applications
BUILD   := build
GO      ?= go
MINIPRO := third_party/minipro
ASSETS  := internal/bundle/assets
MINIPRO_CFLAGS ?= -O2 -Wall -Wno-sign-compare -Wno-unused-parameter
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || sed -n 's/^var Version = "\([^"]*\)".*/\1/p' internal/ui/app.go)
LDFLAGS := -s -w -X lingecu/internal/ui.Version=$(VERSION)

.PHONY: all lingecu lingecu-system minipro bundle release test run install uninstall deps update-minipro clean

all: lingecu

# Single binary: LinGecu with minipro and its databases embedded.
lingecu: bundle
	@mkdir -p $(BUILD)
	$(GO) build -tags bundled -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/lingecu ./cmd/lingecu

# LinGecu without the embedded minipro (uses minipro from $PATH).
lingecu-system:
	@mkdir -p $(BUILD)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/lingecu ./cmd/lingecu

minipro:
	$(MAKE) -C $(MINIPRO) CC="$(CC)" CFLAGS="$(MINIPRO_CFLAGS)" minipro

bundle: minipro
	@mkdir -p $(ASSETS)
	gzip -9 -n -c $(MINIPRO)/minipro     > $(ASSETS)/minipro.gz
	gzip -9 -n -c $(MINIPRO)/infoic.xml  > $(ASSETS)/infoic.xml.gz
	gzip -9 -n -c $(MINIPRO)/logicic.xml > $(ASSETS)/logicic.xml.gz
	cp $(MINIPRO)/udev/60-minipro.rules $(MINIPRO)/UPSTREAM $(ASSETS)/

# Release tarballs for linux amd64/arm64/armhf, built in Docker:
#   make release            or   make release ARCHES="arm64"
release:
	scripts/build-release.sh $(ARCHES)

test:
	MINIPRO_SHARE=$(CURDIR)/$(MINIPRO) $(GO) test -tags ci ./...

run: lingecu
	./$(BUILD)/lingecu

install: lingecu
	install -Dm755 $(BUILD)/lingecu $(BINDIR)/lingecu
	install -Dm644 packaging/lingecu.desktop $(APPDIR)/lingecu.desktop

uninstall:
	rm -f $(BINDIR)/lingecu $(APPDIR)/lingecu.desktop

# Debian/Ubuntu build dependencies: minipro (libusb, zlib) and Fyne (GL/X11/Wayland).
deps:
	sudo apt install -y build-essential pkg-config libusb-1.0-0-dev zlib1g-dev \
		libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev wayland-protocols

# Replace the vendored minipro with an upstream ref: make update-minipro REF=master
update-minipro:
	scripts/update-minipro.sh $(or $(REF),master)

clean:
	rm -rf $(BUILD) $(ASSETS)
	$(MAKE) -C $(MINIPRO) clean
