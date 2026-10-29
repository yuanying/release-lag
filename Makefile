# release-lag
#
# `make test` passes with nothing but the Go toolchain and is the loop to stay
# in while developing. Tests that need more than that go behind a build tag
# of their own, so that `go test ./...` never picks them up.
#
# `make live` is the one target that talks to the real GitHub, through the
# GitHub App the config file names. Nothing else, CI included, leaves the
# machine.
#
# golangci-lint and govulncheck are declared in go.mod's tool directive and run
# through `go tool`, so every checkout runs the same version and nothing is
# installed globally.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO ?= go
APP ?= release-lag
BIN ?= bin/$(APP)

# The version written into the binary. The release workflow passes the tag to
# the image build instead (Dockerfile's VERSION).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X main.version=$(VERSION)

##@ General

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build the binary into bin/.
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/$(APP)

##@ Check

.PHONY: fmt
fmt: ## Format the source tree.
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt'd.
	@unformatted="$$(gofmt -l . | grep -vE '^(bin|dist)/' || true)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Not gofmt'd. Run \`make fmt\`:"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet over every package.
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint.
	$(GO) tool golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint and apply the fixes it can make itself.
	$(GO) tool golangci-lint run --fix ./...

.PHONY: vuln
vuln: ## Report known vulnerabilities in the code paths the module calls.
	$(GO) tool govulncheck ./...

##@ Test

.PHONY: test
test: ## Unit tests. Need nothing but the Go toolchain.
	$(GO) test ./...

.PHONY: live
live: ## Collect once from the real GitHub with LIVE_CONFIG and print the JSON. Not part of `make test`.
	@if [ -z "$(LIVE_CONFIG)" ]; then \
		echo "Set LIVE_CONFIG to a config file whose github section names your App, e.g."; \
		echo "  make live LIVE_CONFIG=hack/live.yaml"; \
		exit 2; \
	fi
	$(GO) run ./cmd/$(APP) --config="$(LIVE_CONFIG)" --once

##@ Housekeeping

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum.
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin dist
