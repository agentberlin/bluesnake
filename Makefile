GO ?= go
PKGS := ./...
COVER_PKGS := ./internal/...
COVER_MIN := 85

# Where the built .app bundle gets installed so Spotlight can index it.
# ~/Applications is user-writable (no sudo) and indexed by Spotlight; override
# with `make desktop APP_INSTALL_DIR=/Applications` for a system-wide install.
APP_NAME := bluesnake.app
APP_BUNDLE := desktop/build/bin/$(APP_NAME)
APP_INSTALL_DIR ?= $(HOME)/Applications

.PHONY: build tunnel-server test unit acceptance cover lint clean desktop desktop-build desktop-dev

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
	$(GO) test $(COVER_PKGS) ./cmd/... ./tunnelserver/...

acceptance: build
	$(GO) test ./test/...

cover:
	$(GO) test -coverprofile=coverage.out $(COVER_PKGS)
	@$(GO) tool cover -func=coverage.out | tail -1
	@total=$$($(GO) tool cover -func=coverage.out | tail -1 | awk '{gsub("%","",$$3); print int($$3)}'); \
	if [ $$total -lt $(COVER_MIN) ]; then echo "coverage $$total% is below $(COVER_MIN)%"; exit 1; fi

lint:
	@test -z "$$(gofmt -l . | grep -v '^vendor/')" || (gofmt -l . && exit 1)
	$(GO) vet $(PKGS)

clean:
	rm -rf bin coverage.out
