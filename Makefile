VERSION ?= $(shell git describe --tags --exact-match --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"
BINARY := bin/xzap
RELEASE_DIR := dist
RELEASE_ARCHIVE := xzap_$(VERSION)_macos_arm64.tar.gz

.PHONY: build install release

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/xzap

install: build
	install -d "$(HOME)/.local/bin"
	install -m 755 $(BINARY) "$(HOME)/.local/bin/xzap"

release:
	@test "$(VERSION)" != dev || { echo "Release needs a version tag or VERSION=vX.Y.Z." >&2; exit 1; }
	mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath $(LDFLAGS) -o $(RELEASE_DIR)/xzap ./cmd/xzap
	cp LICENSE README.md install.sh $(RELEASE_DIR)/
	tar -czf $(RELEASE_DIR)/$(RELEASE_ARCHIVE) -C $(RELEASE_DIR) xzap LICENSE README.md
	cd $(RELEASE_DIR) && shasum -a 256 $(RELEASE_ARCHIVE) > checksums.txt
