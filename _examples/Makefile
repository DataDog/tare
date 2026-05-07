EXAMPLES := go-static scratch c-dynamic python java java-jlinked runtime oci-layout

DOCKER_BUILD := docker build
ifdef PLATFORM
DOCKER_BUILD += --platform $(PLATFORM)
endif

.PHONY: all clean $(EXAMPLES) $(addsuffix -good,$(EXAMPLES)) $(addsuffix -bad,$(EXAMPLES))

all: $(EXAMPLES)

# Each example builds good and bad, then runs tare check against both.
# The good image should pass; the bad image should fail.

go-static: go-static-good go-static-bad
go-static-good:
	$(DOCKER_BUILD) -t example-go-static $(CURDIR)/go-static
	tare check -i example-go-static go-static/check.yaml
go-static-bad:
	$(DOCKER_BUILD) -t example-go-static-bad --target bad $(CURDIR)/go-static
	-tare check -i example-go-static-bad go-static/check.yaml

scratch: scratch-good scratch-bad
scratch-good:
	$(DOCKER_BUILD) -t example-scratch $(CURDIR)/scratch
	tare check -i example-scratch scratch/check.yaml
scratch-bad:
	$(DOCKER_BUILD) -t example-scratch-bad --target bad $(CURDIR)/scratch
	-tare check -i example-scratch-bad scratch/check.yaml

c-dynamic: c-dynamic-good c-dynamic-bad
c-dynamic-good:
	$(DOCKER_BUILD) -t example-c-dynamic $(CURDIR)/c-dynamic
	tare check -i example-c-dynamic c-dynamic/check.yaml
c-dynamic-bad:
	$(DOCKER_BUILD) -t example-c-dynamic-bad --target bad $(CURDIR)/c-dynamic
	-tare check -i example-c-dynamic-bad c-dynamic/check.yaml

python: python-good python-bad
python-good:
	$(DOCKER_BUILD) -t example-python $(CURDIR)/python
	tare check -i example-python python/check.yaml
python-bad:
	$(DOCKER_BUILD) -t example-python-bad --target bad $(CURDIR)/python
	-tare check -i example-python-bad python/check.yaml

java: java-good java-bad
java-good:
	$(DOCKER_BUILD) -t example-java $(CURDIR)/java
	tare check -i example-java java/check.yaml
java-bad:
	$(DOCKER_BUILD) -t example-java-bad --target bad $(CURDIR)/java
	-tare check -i example-java-bad java/check.yaml

# java-jlinked has no good/bad split — it's the same app as `java`, but
# shipped with a custom jlinked JRE on distroless/base. The point is to
# show the tare-weight contrast (~235 MB → ~70 MB).
java-jlinked:
	$(DOCKER_BUILD) -t example-java-jlinked $(CURDIR)/java-jlinked
	tare check -i example-java-jlinked java-jlinked/check.yaml

# runtime has no good/bad split — it's the same image either way; the
# point is that the command test only passes when tare.runtime is
# applied (binds + env mirror the deployment environment).
runtime:
	$(DOCKER_BUILD) -t example-runtime $(CURDIR)/runtime
	tare check -i example-runtime runtime/check.yaml

# OCI layout examples use gcr.io/distroless/static:nonroot pinned by digest.
OCI_IMAGE := gcr.io/distroless/static@sha256:e3f945647ffb95b5839c07038d64f9811adf17308b9121d8a2b87b6a22a80a39

oci-layout: oci-layout-single oci-layout-multiarch

$(CURDIR)/oci-layout/single/oci-layout:
	crane pull --format=oci --platform=linux/$(shell go env GOARCH) $(OCI_IMAGE) $(CURDIR)/oci-layout/single

$(CURDIR)/oci-layout/multiarch/oci-layout:
	crane pull --format=oci $(OCI_IMAGE) $(CURDIR)/oci-layout/multiarch

oci-layout-single: $(CURDIR)/oci-layout/single/oci-layout
	tare check -i $(CURDIR)/oci-layout/single oci-layout/check.yaml

oci-layout-multiarch: $(CURDIR)/oci-layout/multiarch/oci-layout
	tare check -i $(CURDIR)/oci-layout/multiarch oci-layout/check.yaml

clean:
	-docker rmi $(foreach e,$(EXAMPLES),example-$(e) example-$(e)-bad) 2>/dev/null
	-rm -rf $(CURDIR)/oci-layout/single $(CURDIR)/oci-layout/multiarch
