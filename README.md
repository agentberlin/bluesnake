# acrawler

A modern, headless, CLI-first website crawler and SEO auditor in Go — feature parity target is Screaming Frog SEO Spider's crawling/auditing core, minus the UI, minus third-party API integrations, and minus binary config files. Everything is plain-text YAML config + flags, stored in crash-safe SQLite crawl databases.

**Start here:** [docs/DESIGN.md](docs/DESIGN.md) — the living design document this project is built from (§9 = implementation status and deltas).

**Designing a UI on top?** [docs/UI-DESIGN-BRIEF.md](docs/UI-DESIGN-BRIEF.md) — the complete, code-free product spec for designers: every feature, every setting, every dataset and state.

Feature research (what Screaming Frog does, exhaustively inventoried from official docs):
- [docs/research/01-crawl-configuration.md](docs/research/01-crawl-configuration.md)
- [docs/research/02-data-model-and-checks.md](docs/research/02-data-model-and-checks.md)
- [docs/research/03-operations-cli-storage.md](docs/research/03-operations-cli-storage.md)

## Development

BDD-first: Gherkin acceptance specs in `features/` (run by godog), exhaustive table-driven unit tests per module, written **before** implementation. See DESIGN.md §6–7 for the testing strategy and milestone order.

```sh
make test        # unit + acceptance tests
make cover       # coverage report (85% gate on internal/...)
make build       # build ./bin/acrawler
make lint        # gofmt + go vet
```

Scenarios tagged `@pending` describe modules not yet implemented; they are skipped and reported. Removing the tag is part of each milestone's definition of done.

## Desktop app (Wails)

`desktop/` is a native desktop GUI (Go + [Wails v2](https://wails.io) + React) over the same internal crawl engine the CLI uses — same `~/.acrawler` store, so crawls started from either are visible in both. The UI implements the Claude Design handoff: crawl manager, New Crawl flow, live progress, results workspace (dataset rail + tables + issues browser + per-URL drawer), settings/profiles, compare, and a robots.txt tester.

Realtime: the crawler streams pages through its `Sink` interface; the desktop app tees that stream into the SQLite store *and* an aggregator that emits throttled `crawl:progress` Wails events (~4/s) plus a final `crawl:done` — the React progress view subscribes via the Wails event runtime. Pause interrupts the crawl resumably (the store already persists the frontier); Stop finalises early and runs analysis on what was crawled.

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest   # one-time
make desktop-dev   # live-reload development app
make desktop       # production build → desktop/build/bin/acrawler.app
```
