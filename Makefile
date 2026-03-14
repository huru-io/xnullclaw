BINARY    := xnc
BUILD_DIR := $(CURDIR)/build

GO        := go
GOFLAGS   := -trimpath
CGO_ENABLED := 0

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.version=$(VERSION)

PREFIX    := /usr/local

.DEFAULT_GOAL := build

.PHONY: all build test test-e2e lint vet install install-local clean cross docker-mux

all: lint test build

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) .

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 ./...

# E2E tests — requires Docker + LLM key. See internal/mux/e2e_test.go.
test-e2e:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -tags e2e -count=1 -timeout 300s -v ./internal/mux/...

vet:
	$(GO) vet ./...

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)

install-local: build
	@mkdir -p $(HOME)/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) $(HOME)/bin/$(BINARY)

# Cross-compile for common platforms.
cross:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 .

docker-mux: build
	cp $(BUILD_DIR)/$(BINARY) $(CURDIR)/$(BINARY)
	docker build -f Dockerfile.mux -t xnc-mux:$(VERSION) -t xnc-mux:latest .
	rm -f $(CURDIR)/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
	$(GO) clean -testcache
