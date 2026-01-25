# BlueSnake

A production-grade web crawler with multiple interfaces for SEO analysis and web crawling.

## Download Desktop App

- [Windows](https://storage.agentberlin.ai/bluesnake/BlueSnake-Windows-x64.exe)
- [macOS](https://storage.agentberlin.ai/bluesnake/BlueSnake-macOS-Universal.dmg)

## Command-Line Interface (CLI)

```bash
# Build the CLI
go build -o bluesnake ./cmd/cli

# Crawl a website
./bluesnake crawl https://example.com

# Crawl with options
./bluesnake crawl https://example.com -p 10 --js-rendering -o ./results

# Export results
./bluesnake export --crawl-id 1 --format csv -o ./export

# List projects
./bluesnake list projects
```

See [cmd/cli/README.md](cmd/cli/README.md) for complete CLI documentation.

## HTTP Server

```bash
# Run with default settings (0.0.0.0:8080)
go run ./cmd/server

# Run on custom port
go run ./cmd/server -port 9090

# Build and run
go build -o bluesnake-server ./cmd/server
./bluesnake-server -port 8080
```

See [API.md](API.md) for complete REST API documentation.

## Development

For development with hot-reload:

```bash
# Install air (Go live reload)
go install github.com/air-verse/air@latest

# Run with hot-reload
air
```

Air automatically rebuilds and restarts the server on code changes. Configuration is in `.air.toml`.

## Documentation

- [API.md](API.md) - REST API reference
- [ARCHITECTURE.md](ARCHITECTURE.md) - System architecture
- [cmd/cli/README.md](cmd/cli/README.md) - CLI documentation
- [cmd/server/README.md](cmd/server/README.md) - Server deployment guide
