DOCKERHUB_USER:=weaveworks
PLUGIN_EXE:=plugin/plugin
PLUGIN_IMAGE:=$(DOCKERHUB_USER)/plugin
PLUGIN_EXPORT:=plugin.tar

.DEFAULT: all
.PHONY: all

all: $(PLUGIN_EXPORT)

PLUGIN_VERSION=git-$(shell git rev-parse --short=12 HEAD)

$(PLUGIN_EXE): plugin/main.go plugin/driver/*.go plugin/skel/*.go
	go get -tags netgo ./$(@D)
	go build -ldflags "-extldflags \"-static\" -X main.version $(PLUGIN_VERSION)" -tags netgo -o $@ ./$(@D)
	@strings $@ | grep cgo_stub\\\.go >/dev/null || { \
		rm $@; \
		echo "\nYour go standard library was built without the 'netgo' build tag."; \
		echo "To fix that, run"; \
		echo "    sudo go clean -i net"; \
		echo "    sudo go install -tags netgo std"; \
		false; \
	}

$(PLUGIN_EXPORT): plugin/Dockerfile $(PLUGIN_EXE)
	$(SUDO) docker build -t $(PLUGIN_IMAGE) plugin
	$(SUDO) docker save $(PLUGIN_IMAGE):latest > $@

build:
	$(SUDO) go clean -i net
	$(SUDO) go install -tags netgo std
	$(MAKE)

