SHELL := /bin/bash

# Resolve each tool from $PATH first, then fall back to its common install
# location. Override any of them explicitly, e.g. `make GO=/opt/go/bin/go build`.
# `tool <name> <fallback>` = the binary on PATH if present, else the fallback.
tool = $(shell command -v $(1) 2>/dev/null || echo $(2))

GO     ?= $(call tool,go,$(HOME)/.local/sdk/go/bin/go)
BUN    ?= $(call tool,bun,$(HOME)/.bun/bin/bun)
# air lives in GOPATH/bin after `make install`; if GOPATH can't be read, use the
# conventional ~/go.
GOPATH ?= $(shell $(GO) env GOPATH 2>/dev/null || echo $(HOME)/go)
AIR    ?= $(call tool,air,$(GOPATH)/bin/air)
PREFIX ?= $(HOME)/.local

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/enowdev/antares/internal/version.Version=$(VERSION) \
  -X github.com/enowdev/antares/internal/version.Commit=$(COMMIT) \
  -X github.com/enowdev/antares/internal/version.Date=$(DATE)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	 | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---- development ------------------------------------------------------------

.PHONY: dev
dev: ## Run backend (Air hot reload) and frontend (Vite HMR) together
	@echo "Antares dev: API :8787 · Web :5173"
	@trap 'kill 0' EXIT INT TERM; \
	 $(MAKE) dev-api & \
	 $(MAKE) dev-web & \
	 wait

.PHONY: dev-api
dev-api: ## Backend with hot reload
	@$(AIR) -c .air.toml

.PHONY: dev-web
dev-web: ## Frontend dev server with HMR
	@cd web && $(BUN) x vite

.PHONY: tui
tui: ## Hot-reload the TUI preview in the foreground (real terminal)
	@./scripts/tui-dev.sh

.PHONY: install
install: ## Install toolchain deps (no sudo)
	@$(GO) mod download
	@$(GO) install github.com/air-verse/air@latest
	@cd web && $(BUN) install

# ---- build ------------------------------------------------------------------

.PHONY: build
build: build-web ## Build the shipped `antares` binary with the dashboard embedded
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/antares ./cmd/antares
	@echo "built bin/antares ($(VERSION))"

.PHONY: build-web
build-web: ## Build the dashboard into internal/server/dist
	@cd web && $(BUN) run build
	@rm -rf internal/server/dist
	@cp -r web/dist internal/server/dist
	# `//go:embed all:dist` requires at least one tracked file; the placeholder
	# is committed as internal/server/dist/.gitkeep so a clean checkout can
	# `go build` without a prior `bun run build`. `cp -r web/dist ...` above
	# blows the placeholder away, so restore it here — narrowly, no broad
	# cleanup of anything else the dashboard produced.
	@touch internal/server/dist/.gitkeep

.PHONY: build-fixture
build-fixture: ## Build the smoke-only fixture helper (cmd/smokefixture)
	@$(GO) build -trimpath -o bin/smokefixture ./cmd/smokefixture

.PHONY: build-api
build-api: ## Build the backend only (no dashboard)
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/antares ./cmd/antares

.PHONY: install-cli
install-cli: build ## Build (with dashboard) and install `antares` to $(PREFIX)/bin
	@mkdir -p $(PREFIX)/bin
	@cp bin/antares $(PREFIX)/bin/antares.new
	@mv -f $(PREFIX)/bin/antares.new $(PREFIX)/bin/antares
	@echo "installed $(PREFIX)/bin/antares ($(VERSION))"
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) echo "run 'antares' from anywhere (open a new shell if it's not found yet)";; \
	  *) echo "note: add $(PREFIX)/bin to your PATH — e.g. echo 'export PATH=\"$(PREFIX)/bin:\$$PATH\"' >> ~/.bashrc";; esac

# ---- desktop app (separate Go module in desktop/, needs cgo + a webview) ----

.PHONY: desktop
desktop: build ## Build desktop/bin/Antares.app (macOS) with a fresh bin/antares bundled
	@cd desktop && GO="$(GO)" VERSION="$(VERSION)" ANTARES_BIN="$(CURDIR)/bin/antares" ./scripts/build-macos.sh

.PHONY: desktop-test
desktop-test: ## Vet and test the desktop shell module
	@cd desktop && $(GO) vet ./... && $(GO) test ./...

# ---- quality ----------------------------------------------------------------

.PHONY: test
test: ## Run Go tests
	@$(GO) test ./...

.PHONY: vet
vet: ## Run go vet
	@$(GO) vet ./...

.PHONY: typecheck
typecheck: ## Typecheck the frontend
	@cd web && $(BUN) x tsc -b --noEmit

.PHONY: web-test
web-test: ## Run frontend regression tests
	@cd web && $(BUN) test

.PHONY: smoke
smoke: build build-fixture ## Build antares + the smoke fixture, then load every dashboard route in a real browser
	@$(BUN) scripts/smoke-run.mjs

.PHONY: check
check: vet test typecheck web-test ## Run every check (add `make smoke` for the browser pass)

.PHONY: fmt
fmt: ## Format Go sources
	@$(GO) fmt ./...

.PHONY: sync-models
sync-models: ## Refresh internal/providers/models_generated.json from models.dev
	@$(GO) run ./scripts/sync-models-dev.go

.PHONY: release
release: ## Cross-compile release binaries for all platforms into dist/release
	@VERSION="$(VERSION)" GO="$(GO)" BUN="$(BUN)" ./scripts/release-build.sh

.PHONY: clean
clean: ## Remove build artefacts
	@rm -rf bin .air web/dist dist desktop/bin
	@echo "cleaned"

.PHONY: doctor
doctor: ## Diagnose the local setup
	@$(GO) run ./cmd/antares doctor
