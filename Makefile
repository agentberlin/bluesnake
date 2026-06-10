GO ?= go
PKGS := ./...
COVER_PKGS := ./internal/...
COVER_MIN := 85

.PHONY: build test unit acceptance cover lint clean desktop desktop-dev

build:
	$(GO) build -o bin/acrawler ./cmd/acrawler

# desktop app (Wails) — requires the wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest
desktop:
	cd desktop && wails build

desktop-dev:
	cd desktop && wails dev

test: unit acceptance

unit:
	$(GO) test $(COVER_PKGS) ./cmd/...

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
