BINARY    := xnc-mux
BUILD_DIR := $(CURDIR)/build
MUX_DIR   := mux

GO        := go
GOFLAGS   := -trimpath
CGO_ENABLED := 1

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.version=$(VERSION)

PREFIX    := /usr/local

.DEFAULT_GOAL := build

.PHONY: all build test lint lint-go lint-shell install clean

all: lint test build

build:
	@mkdir -p $(BUILD_DIR)
	cd $(MUX_DIR) && CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) .

test:
	cd $(MUX_DIR) && CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 -race ./...

lint: lint-go lint-shell

lint-go:
	cd $(MUX_DIR) && $(GO) vet ./...
	@command -v staticcheck >/dev/null 2>&1 && (cd $(MUX_DIR) && staticcheck ./...) || echo "staticcheck not installed, skipping"

lint-shell:
	shellcheck -s bash -S warning xnullclaw

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -m 755 xnullclaw $(DESTDIR)$(PREFIX)/bin/xnullclaw

clean:
	rm -rf $(BUILD_DIR)
	cd $(MUX_DIR) && $(GO) clean -testcache
