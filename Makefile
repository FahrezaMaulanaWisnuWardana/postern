# Postern — build and release helpers.
#
# The desktop client and the sync server are separate commands from one Go
# module. Both are static enough to copy onto a machine and run; only the
# desktop client needs a graphics stack (Metal, X11 or Wayland) at runtime.

VERSION      ?= 0.1.0
BRAND_NAME   ?= Postern
BRAND_SLUG   ?= postern
BRAND_TAGLINE?= Native, private API client
BRAND_SERVER ?=
BRAND_ACCENT ?= \#D9541E
BRAND_SITE   ?=

# A brand file supplies every identity field at once. Individual BRAND_*
# variables still work and are useful for one-off builds.
ifdef BRAND_FILE
BRAND_NAME := $(shell go run ./cmd/brandgen -in "$(BRAND_FILE)" -get name)
BRAND_SLUG := $(shell go run ./cmd/brandgen -in "$(BRAND_FILE)" -get slug)
# Rebuild when the brand file or its optional logo changes.
BRAND_DEPS := $(BRAND_FILE)
BRAND_LOGO := $(shell go run ./cmd/brandgen -in "$(BRAND_FILE)" -get logo 2>/dev/null)
ifneq ($(BRAND_LOGO),)
BRAND_DEPS += $(BRAND_LOGO)
endif
endif

BIN          := bin
APP          := $(BIN)/$(BRAND_SLUG)
SERVER       := $(BIN)/$(BRAND_SLUG)-server

# Windows packaging. The desktop executable uses the GUI subsystem so it does
# not open a console window; a console-subsystem companion carries the CLI.
WIN_ARCH   ?= amd64
WIN_SUFFIX := $(if $(filter amd64,$(WIN_ARCH)),,-$(WIN_ARCH))
WIN_APP    := $(BIN)/$(BRAND_SLUG)$(WIN_SUFFIX).exe
WIN_CLI    := $(BIN)/$(BRAND_SLUG)-cli$(WIN_SUFFIX).exe
WIN_SERVER := $(BIN)/$(BRAND_SLUG)-server$(WIN_SUFFIX).exe

# -s -w strips the symbol table and DWARF data. -trimpath keeps absolute paths
# out of the binary so release builds are reproducible across machines.
ifdef BRAND_FILE
LDFLAGS := -s -w $(shell go run ./cmd/brandgen -in "$(BRAND_FILE)" -format make | tr '\n' ' ')
else
LDFLAGS := -s -w \
	-X 'yourpm/core/brand.Version=$(VERSION)' \
	-X 'yourpm/core/brand.Name=$(BRAND_NAME)' \
	-X 'yourpm/core/brand.Slug=$(BRAND_SLUG)' \
	-X 'yourpm/core/brand.Tagline=$(BRAND_TAGLINE)' \
	-X 'yourpm/core/brand.DefaultServer=$(BRAND_SERVER)' \
	-X 'yourpm/core/brand.Accent=$(BRAND_ACCENT)' \
	-X 'yourpm/core/brand.Website=$(BRAND_SITE)'
endif

.PHONY: all build app server windows test vet fmt tidy run run-server clean bundle brand help package

all: build

help:
	@echo "targets:"
	@echo "  build       build $(APP) and $(SERVER)"
	@echo "  test        run the test suite"
	@echo "  vet         run go vet"
	@echo "  fmt         format all Go files"
	@echo "  run         open the desktop app"
	@echo "  run-server  run the sync server against ./data"
	@echo "  windows     cross-compile the Windows client, CLI and server"
	@echo "  bundle      wrap the macOS binary in $(BRAND_NAME).app"
	@echo "  package     create native installers (Python 3.10+, see docs/RELEASING.md)"
	@echo "  brand       print ldflags for BRAND=brand/acme.yaml"
	@echo "  clean       remove build output"
	@echo ""
	@echo "white-label example:"
	@echo "  make build BRAND_NAME=AcmeAPI BRAND_SLUG=acme \\"
	@echo "    BRAND_SERVER=https://sync.acme.example BRAND_ACCENT=#5BC8FF"

build: app server
	@echo "binaries ready: $(APP) and $(SERVER)"

# Release packaging always rebuilds into temporary staging; cached bin/ output
# must not accidentally retain an old version or brand.
package:
	python3 scripts/release.py --version "$(VERSION)" --name "$(BRAND_NAME)" --slug "$(BRAND_SLUG)"

app: $(APP)

server: $(SERVER)

$(APP): $(shell find app ui core cli -name '*.go') $(BRAND_DEPS) go.mod go.sum
	@mkdir -p $(BIN)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./app

$(SERVER): $(shell find cmd server core -name '*.go') $(BRAND_DEPS) go.mod go.sum
	@mkdir -p $(BIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/yourpm-server

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

tidy:
	go mod tidy

run:
	go run -ldflags "$(LDFLAGS)" ./app

run-server:
	go run ./cmd/yourpm-server

# Cross-compiles for Windows from any host. WIN_ARCH=arm64 selects Windows on
# ARM. The GUI binary is linked with -H windowsgui; use the -cli binary for
# shell commands so output reaches the terminal.
windows:
	@mkdir -p $(BIN)
	GOOS=windows GOARCH=$(WIN_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(WIN_APP) ./app
	GOOS=windows GOARCH=$(WIN_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(WIN_CLI) ./app
	GOOS=windows GOARCH=$(WIN_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(WIN_SERVER) ./cmd/yourpm-server
	@echo "built $(WIN_APP), $(WIN_CLI), $(WIN_SERVER)"

# A minimal macOS application bundle. Building this on macOS keeps the native
# menu bar, dock icon and window behaviour users expect from a desktop tool.
bundle: $(APP)
	@mkdir -p "$(BIN)/$(BRAND_NAME).app/Contents/MacOS"
	@mkdir -p "$(BIN)/$(BRAND_NAME).app/Contents/Resources"
	@cp $(APP) "$(BIN)/$(BRAND_NAME).app/Contents/MacOS/$(BRAND_SLUG)"
	@printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0"><dict>' \
		'  <key>CFBundleName</key><string>$(BRAND_NAME)</string>' \
		'  <key>CFBundleDisplayName</key><string>$(BRAND_NAME)</string>' \
		'  <key>CFBundleIdentifier</key><string>dev.$(BRAND_SLUG).desktop</string>' \
		'  <key>CFBundleExecutable</key><string>$(BRAND_SLUG)</string>' \
		'  <key>CFBundlePackageType</key><string>APPL</string>' \
		'  <key>CFBundleShortVersionString</key><string>$(VERSION)</string>' \
		'  <key>CFBundleVersion</key><string>$(VERSION)</string>' \
		'  <key>NSHighResolutionCapable</key><true/>' \
		'  <key>LSMinimumSystemVersion</key><string>13.0</string>' \
		'</dict></plist>' > "$(BIN)/$(BRAND_NAME).app/Contents/Info.plist"
	@echo "built $(BIN)/$(BRAND_NAME).app"

# Convert a simple brand file into the -X flags understood by the linker.
# Usage: make brand BRAND=brand/acme.yaml
brand:
	@test -n "$(BRAND)" || (echo "usage: make brand BRAND=brand/acme.yaml" && exit 2)
	@go run ./cmd/brandgen -in "$(BRAND)" -format make

clean:
	rm -rf $(BIN)
