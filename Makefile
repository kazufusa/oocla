GO ?= go
BIN := bin/oocla

# VERSION is the tag being built. Release builds pass it in; local builds derive
# something descriptive from git so a binary can always be traced back.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION_PKG := github.com/kazufusa/oocla/internal/buildinfo.Version
# Artifact names follow the goreleaser convention: the tag's leading v is
# stripped, so v0.1.0 ships as oocla_0.1.0_<os>_<arch>. The binary itself
# still reports the full tag.
DIST_VERSION := $(patsubst v%,%,$(VERSION))

# PLATFORMS is what a release ships. oocla is a single binary with no
# dependencies, so every one of these is just a cross-compile.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: check fmt vet test build e2e dist formula clean run

check: fmt vet test build

fmt:
	@out=$$(gofmt -l . 2>&1); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

test:
	$(GO) test -race ./...

build:
	$(GO) build -ldflags "-X $(VERSION_PKG)=$(VERSION)" -o $(BIN) ./cmd/oocla

run: build
	$(BIN) serve

e2e: build
	OOCLA_BIN=$(PWD)/$(BIN) $(GO) test -tags=e2e -count=1 -timeout=10m ./e2e/...

# dist builds every release artifact into dist/, with a checksum file. Running
# it locally produces exactly what the release workflow uploads.
dist:
	rm -rf dist
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=''; [ "$$os" = windows ] && ext='.exe'; \
		name="oocla_$(DIST_VERSION)_$${os}_$${arch}"; \
		echo "building $$name"; \
		mkdir -p "dist/$$name"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build \
			-trimpath -ldflags "-s -w -X $(VERSION_PKG)=$(VERSION)" \
			-o "dist/$$name/oocla$$ext" ./cmd/oocla || exit 1; \
		cp LICENSE README.md README.ja.md "dist/$$name/"; \
		if [ "$$os" = windows ]; then \
			(cd dist && zip -qr "$$name.zip" "$$name"); \
		else \
			tar -czf "dist/$$name.tar.gz" -C dist "$$name"; \
		fi; \
		rm -rf "dist/$$name"; \
	done
	@cd dist && sha256sum * > "oocla_$(DIST_VERSION)_checksums.txt"
	@ls -1 dist

# formula prints the Homebrew formula for the artifacts already in dist/.
# Works on macOS Homebrew and Linuxbrew alike; the release workflow pushes it
# to the tap repository.
formula:
	@sh scripts/brew-formula.sh $(VERSION)

clean:
	rm -rf bin dist
