# sonarc - build and deployment
#
# The editor is developed on macOS and runs on Linux servers. CGO is disabled
# throughout, which produces a genuinely static binary: no libc dependency, so
# one file runs on Alpine/musl, Debian, and ancient-glibc CentOS alike.

BIN     := sonarc
PKG     := ./cmd/sonarc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD   := build

GO      ?= go
export CGO_ENABLED = 0

.PHONY: all build linux release site install test race cover vet fmt lint bench clean deploy doctor help

all: build

## build: compile for the host platform
build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

## linux: static binaries for linux/amd64 and linux/arm64
linux:
	@mkdir -p $(BUILD)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags '$(LDFLAGS)' -o $(BUILD)/$(BIN)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags '$(LDFLAGS)' -o $(BUILD)/$(BIN)-linux-arm64 $(PKG)
	@echo
	@ls -lh $(BUILD)/$(BIN)-linux-* | awk '{print $$9, $$5}'
	@file $(BUILD)/$(BIN)-linux-* 2>/dev/null | sed 's/, BuildID.*//' || true

## release: static binaries for linux and macOS, amd64 and arm64, with
##   checksums.txt - the files a GitHub release carries and install.sh fetches
RELEASE_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo 'shasum -a 256')
release:
	@rm -rf $(BUILD) && mkdir -p $(BUILD)
	@for t in $(RELEASE_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "building $(BIN)-$$os-$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD)/$(BIN)-$$os-$$arch $(PKG) || exit 1; \
	done
	cd $(BUILD) && $(SHA256) $(BIN)-* > checksums.txt
	@cat $(BUILD)/checksums.txt

## site: build the website into build/site as the Pages workflow does
##   (docs from README.md and CONTRIBUTING.md, releases from GitHub)
site:
	@rm -rf $(BUILD)/site && mkdir -p $(BUILD)/site
	cp -R site/. $(BUILD)/site/ && rm -rf $(BUILD)/site/gen
	cp install.sh $(BUILD)/site/
	cd site/gen && $(GO) run . -root ../.. -out ../../$(BUILD)/site
	@echo "open $(BUILD)/site/index.html"

## install: install to GOPATH/bin for local use
install:
	$(GO) install -ldflags '$(LDFLAGS)' $(PKG)

## test: run the full test suite
test:
	$(GO) test ./...

## race: run tests under the race detector (needs CGO)
race:
	CGO_ENABLED=1 $(GO) test -race ./...

## cover: report test coverage per package
cover:
	$(GO) test -cover ./...

## bench: run benchmarks
bench:
	$(GO) test -bench=. -benchmem -run='^$$' ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format all sources
fmt:
	$(GO) fmt ./...

## lint: formatting and vet gate used before deploying
lint: vet
	@test -z "$$(gofmt -l cmd internal)" || { echo "unformatted files:"; gofmt -l cmd internal; exit 1; }

## deploy: build and copy to a server, matching its architecture
##   usage: make deploy HOST=myserver [DEST=~/bin]
deploy: linux
	@test -n "$(HOST)" || { echo "usage: make deploy HOST=<ssh-host> [DEST=~/bin]"; exit 1; }
	$(eval DEST ?= ~/bin)
	@echo "detecting architecture on $(HOST)..."
	$(eval ARCH := $(shell ssh $(HOST) 'uname -m' 2>/dev/null))
	@test -n "$(ARCH)" || { echo "could not reach $(HOST) over ssh"; exit 1; }
	$(eval GOARCH_REMOTE := $(if $(filter aarch64 arm64,$(ARCH)),arm64,amd64))
	@echo "$(HOST) is $(ARCH) -> shipping $(BIN)-linux-$(GOARCH_REMOTE)"
	ssh $(HOST) 'mkdir -p $(DEST)'
	scp $(BUILD)/$(BIN)-linux-$(GOARCH_REMOTE) $(HOST):$(DEST)/$(BIN)
	@echo
	@echo "installed. on $(HOST), run:  $(DEST)/$(BIN) -doctor"

## doctor: report this terminal's capabilities
doctor: build
	./$(BIN) -doctor

## clean: remove build artifacts
clean:
	rm -rf $(BUILD) $(BIN)

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
