GO ?= go
PKGS := ./...
# The coverage gate spans internal/ AND cmd/ — cmd/bluesnake being outside the
# gate (plus having no unit tests at all) is the structural reason a CLI-only
# resume-corruption bug shipped invisibly (#74 R1/R4). NOTE: acceptance
# scenarios exec the built binary as a subprocess, which in-process coverage
# instrumentation cannot see — the cmd/ unit tests are what count here.
COVER_PKGS := ./internal/... ./cmd/...
# -coverpkg wants a comma-separated pattern list (space-separated would parse as
# package arguments).
COVERPKG := ./internal/...,./cmd/...
# Aggregate statement-coverage gate across COVER_PKGS (see docs/DESIGN.md §6).
COVER_MIN := 90

# Where the built .app bundle gets installed so Spotlight can index it.
# ~/Applications is user-writable (no sudo) and indexed by Spotlight; override
# with `make desktop APP_INSTALL_DIR=/Applications` for a system-wide install.
APP_NAME := bluesnake.app
APP_BUNDLE := desktop/build/bin/$(APP_NAME)
APP_INSTALL_DIR ?= $(HOME)/Applications

.PHONY: build tunnel-server test unit acceptance race cover lint clean desktop desktop-build desktop-dev dist-cli package-deb

# Concurrency-critical packages exercised under the race detector (T9). Scoped to
# the packages with real goroutine interplay — worker pool, dispatcher, the store
# dedup/content authority, runner sink — so the gate stays fast while covering the
# shared-state paths the bounded-RAM / parallel rework introduced.
RACE_PKGS := ./internal/crawler/... ./internal/runner/... ./internal/frontier/... ./internal/store/... ./internal/queue/...

build:
	$(GO) build -o bin/bluesnake ./cmd/bluesnake

# Reverse-tunnel server (deployed separately; self-contained under tunnelserver/).
tunnel-server:
	$(GO) build -o bin/bluesnake-tunnelserver ./tunnelserver

# desktop app (Wails) — requires the wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest
# `make desktop` builds the .app bundle and installs it into APP_INSTALL_DIR so
# it shows up in Spotlight.
desktop: desktop-build
	@mkdir -p "$(APP_INSTALL_DIR)"
	@rm -rf "$(APP_INSTALL_DIR)/$(APP_NAME)"
	@cp -R "$(APP_BUNDLE)" "$(APP_INSTALL_DIR)/"
	@echo "Installed $(APP_NAME) to $(APP_INSTALL_DIR) — search for \"bluesnake\" in Spotlight."

desktop-build:
	cd desktop && wails build

desktop-dev:
	cd desktop && wails dev

test: unit acceptance

unit:
	$(GO) test $(COVER_PKGS) ./tunnelserver/...

acceptance: build
	$(GO) test ./test/...

race:
	$(GO) test -race $(RACE_PKGS)

# Coverage of internal/... measured by BOTH the unit tests AND the acceptance/BDD
# suite (./test/...), attributed via -coverpkg so a package exercised only through
# the acceptance flow still counts toward the gate (P19). Caveat: scenarios that
# exec the bluesnake binary as a subprocess are NOT captured by in-process
# instrumentation (that would need GOCOVERDIR integration-test coverage), so the
# acceptance contribution is partial; the @chrome scenarios (excluded by default)
# are what fill the render package's coverage on a Chrome-equipped CI toolchain.
cover: build
	$(GO) test -coverpkg=$(COVERPKG) -coverprofile=coverage.out $(COVER_PKGS) ./test/...
	@$(GO) tool cover -func=coverage.out | tail -1
	@total=$$($(GO) tool cover -func=coverage.out | tail -1 | awk '{gsub("%","",$$3); print int($$3)}'); \
	if [ $$total -lt $(COVER_MIN) ]; then echo "coverage $$total% is below $(COVER_MIN)%"; exit 1; fi

lint:
	@test -z "$$(gofmt -l . | grep -v '^vendor/')" || (gofmt -l . && exit 1)
	$(GO) vet $(PKGS)

clean:
	rm -rf bin coverage.out

# ---- distribution artifacts -------------------------------------------------
# These mirror .github/workflows/release.yml for local builds. The CLI is pure
# Go (modernc sqlite), so it cross-compiles with CGO disabled; the Wails desktop
# app must be built natively per-OS (macOS .app/.dmg here, Windows on CI).
DIST ?= dist
VERSION := $(shell cat internal/version/VERSION)
DIST_LDFLAGS := -s -w

# Cross-compile the CLI for every shipped target into $(DIST).
dist-cli:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 $(GO) build -trimpath -ldflags "$(DIST_LDFLAGS)" -o $(DIST)/bluesnake-linux-amd64  ./cmd/bluesnake
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 $(GO) build -trimpath -ldflags "$(DIST_LDFLAGS)" -o $(DIST)/bluesnake-linux-arm64  ./cmd/bluesnake
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "$(DIST_LDFLAGS)" -o $(DIST)/bluesnake-darwin-amd64 ./cmd/bluesnake
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "$(DIST_LDFLAGS)" -o $(DIST)/bluesnake-darwin-arm64 ./cmd/bluesnake
	@echo "CLI binaries in $(DIST)/"

# Build a Debian .deb for the host arch (override with `make package-deb ARCH=arm64`).
# Requires nfpm: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
package-deb:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(or $(ARCH),amd64) $(GO) build -trimpath -ldflags "$(DIST_LDFLAGS)" -o $(DIST)/bluesnake ./cmd/bluesnake
	VERSION=$(VERSION) ARCH=$(or $(ARCH),amd64) nfpm package --config packaging/nfpm.yaml --packager deb --target $(DIST)/
	@echo "Debian package in $(DIST)/"
