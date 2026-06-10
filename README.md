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
