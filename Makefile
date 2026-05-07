GOARCH ?= $(shell go env GOARCH)
GOOS ?= linux
HOST_ARCH := $(shell go env GOHOSTARCH)
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

# Toybox release-filename suffix per Go arch
TOYBOX_TARGET_amd64 := x86_64
TOYBOX_TARGET_arm64 := aarch64

# Paths
HARNESS_DIR := harness/linux-$(GOARCH)
DOWNLOAD_DIR := .cache/downloads
APPLETS_FILE := .cache/toybox-applets.txt

.DEFAULT_GOAL := tare

# Delete target files when their recipe fails — without this, a partial
# write (e.g., a redirected stdout) leaves an up-to-date but corrupt
# artifact that poisons subsequent builds.
.DELETE_ON_ERROR:

.PHONY: tare harness harness-arm64 harness-amd64 release clean

tare: harness
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o tare ./cmd/tare

harness: harness-arm64 harness-amd64

harness-arm64: $(APPLETS_FILE)
	@$(MAKE) harness-arch GOARCH=arm64

harness-amd64: $(APPLETS_FILE)
	@$(MAKE) harness-arch GOARCH=amd64

harness-arch: $(HARNESS_DIR)/bin/toybox $(HARNESS_DIR)/bin/tare-tool $(APPLETS_FILE)
	@# Symlink each toybox applet next to the toybox binary. The applet list
	@# is enumerated once at top level (see $(APPLETS_FILE)) using the host
	@# arch; toybox 0.8.13 ships an identical applet set across architectures.
	@# Cross-arch enumeration was the source of a silent failure that shipped
	@# a harness with no applet symlinks.
	@while read applet; do \
		case "$$applet" in toybox|tare-tool|"") continue ;; esac; \
		ln -sf toybox $(HARNESS_DIR)/bin/$$applet; \
	done < $(APPLETS_FILE)
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

$(DOWNLOAD_DIR)/toybox-%:
	@mkdir -p $(DOWNLOAD_DIR)
	curl -sfL -o $@ https://landley.net/toybox/bin/toybox-$(TOYBOX_TARGET_$*)
	@echo "$(TOYBOX_SHA256_$*)  $@" | shasum -a 256 -c -
	chmod +x $@

$(HARNESS_DIR)/bin/toybox: $(DOWNLOAD_DIR)/toybox-$(GOARCH)
	@mkdir -p $(HARNESS_DIR)/bin
	cp $< $@
	chmod +x $@

# Enumerate toybox applets once using the host-arch binary. Pinning
# --platform=linux/$(HOST_ARCH) avoids cross-arch exec inside the
# container; every host has at least one matching toybox build.
# Toybox prints applets whitespace-separated across multiple lines —
# we normalize to one-per-line so the count guard below is meaningful
# and the read loop in harness-arch is straightforward.
$(APPLETS_FILE): $(DOWNLOAD_DIR)/toybox-$(HOST_ARCH)
	@mkdir -p $(@D)
	docker run --rm --platform=linux/$(HOST_ARCH) \
		-v $(CURDIR)/$<:/usr/local/bin/toybox:ro \
		gcr.io/distroless/static:nonroot \
		toybox | tr -s '[:space:]' '\n' | sed '/^$$/d' > $@
	@count=$$(wc -l < $@ | tr -d ' '); \
	if [ "$$count" -lt 100 ]; then \
		echo "ERROR: toybox enumerated $$count applets (expected >=100); aborting" >&2; \
		exit 1; \
	fi; \
	echo "Enumerated $$count toybox applets -> $@"

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
