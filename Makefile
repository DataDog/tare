GOARCH ?= $(shell go env GOARCH)
GOOS ?= linux
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
TAR ?= tar
define require-gnu-tar
$(if $(shell $(TAR) --version 2>&1 | grep GNU),,$(error GNU tar required; install with 'brew install gnu-tar' and set TAR=gtar))
endef

# Versions
TOYBOX_VERSION := 0.8.13

# Checksums (sha256)
TOYBOX_SHA256_amd64 := 8c98795a15db31ea55c8065fed379db3669766b7a714c46b009d8bfb87b25ffd
TOYBOX_SHA256_arm64 := b3508e5f51a0d429c1bda9d500d98d97dc0b86571762eeb099495eb238a8c52a

# Derived
TOYBOX_ARCH_$(GOARCH) := $(if $(filter amd64,$(GOARCH)),x86_64,aarch64)
TOYBOX_ARCH := $(TOYBOX_ARCH_$(GOARCH))
TOYBOX_SHA256 := $(TOYBOX_SHA256_$(GOARCH))

# Paths
HARNESS_DIR := harness/linux-$(GOARCH)
DOWNLOAD_DIR := .cache/downloads

# URLs
TOYBOX_URL := https://landley.net/toybox/bin/toybox-$(TOYBOX_ARCH)

.DEFAULT_GOAL := tare

.PHONY: tare harness harness-arm64 harness-amd64 release clean

tare: harness
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o tare ./cmd/tare

harness: harness-arm64 harness-amd64

harness-arm64:
	@$(MAKE) harness-arch GOARCH=arm64

harness-amd64:
	@$(MAKE) harness-arch GOARCH=amd64

harness-arch: $(HARNESS_DIR)/bin/toybox $(HARNESS_DIR)/bin/tare-tool
	@# Ensure toybox applet symlinks exist (idempotent).
	@for applet in $$(docker run --rm -v $$(pwd)/$(HARNESS_DIR)/bin/toybox:/usr/local/bin/toybox:ro gcr.io/distroless/static:nonroot toybox 2>/dev/null || $(HARNESS_DIR)/bin/toybox 2>/dev/null); do \
		[ "$$applet" = "toybox" ] || [ "$$applet" = "tare-tool" ] || ln -sf toybox $(HARNESS_DIR)/bin/$$applet 2>/dev/null; \
	done
	$(call require-gnu-tar)
	@# Build a gzipped tarball of the harness with a tmp/.tare/ prefix.
	@# This tarball is go:embed'd into the tare binary and piped to
	@# "docker cp - container:/" at runtime, which creates /tmp/.tare
	@# even on scratch images that lack /tmp.
	@# --no-xattrs: strip macOS xattrs that break docker cp.
	@# --transform: rewrite paths so entries unpack at /tmp/.tare/.
	@# --owner/--group/--numeric-owner: force ownership to root:0 so
	@# command tests like `find -nouser`/`find -nogroup` don't flag the
	@# harness as unowned/ungrouped against images whose /etc/passwd
	@# lacks the build host's UID.
	$(TAR) --no-xattrs --owner=0 --group=0 --numeric-owner -czf internal/harness/harness-linux-$(GOARCH).tar.gz --transform 's,^\./,tmp/.tare/,' -C $(HARNESS_DIR) .
	@git update-index --assume-unchanged internal/harness/harness-linux-$(GOARCH).tar.gz
	@echo "Harness assembled at $(HARNESS_DIR)/"
	@echo "  toybox:    $(TOYBOX_VERSION)"

# --- toybox ---

$(DOWNLOAD_DIR)/toybox-$(GOARCH):
	@mkdir -p $(DOWNLOAD_DIR)
	curl -sfL -o $@ $(TOYBOX_URL)
	@echo "$(TOYBOX_SHA256)  $@" | shasum -a 256 -c -

$(HARNESS_DIR)/bin/toybox: $(DOWNLOAD_DIR)/toybox-$(GOARCH)
	@mkdir -p $(HARNESS_DIR)/bin
	cp $< $@
	chmod +x $@

# --- tare-tool ---

$(HARNESS_DIR)/bin/tare-tool: FORCE
	@mkdir -p $(HARNESS_DIR)/bin
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags '$(LDFLAGS)' -o $@ ./cmd/tare-tool/

FORCE:

# --- release ---

release: harness
	@mkdir -p dist
	@for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		echo "Building tare-$${os}-$${arch}..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags '$(LDFLAGS)' -o dist/tare-$${os}-$${arch} ./cmd/tare || exit 1; \
	done
	cd dist && shasum -a 256 tare-* > checksums.txt

# --- clean ---

clean:
	rm -rf harness/ .cache/ dist/
	git update-index --no-assume-unchanged internal/harness/harness-linux-amd64.tar.gz internal/harness/harness-linux-arm64.tar.gz
	git checkout -- internal/harness/harness-linux-amd64.tar.gz internal/harness/harness-linux-arm64.tar.gz
