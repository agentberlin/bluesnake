# Test Server (Development Only)

This is a development server used for testing with hot-reload support via [air](https://github.com/air-verse/air).

**For production use, see [cmd/server/](../server/README.md).**

## Usage with Air (Recommended)

```bash
# Install air (Go live reload)
go install github.com/air-verse/air@latest

# Run from project root with hot-reload
air
```

Air watches for file changes and automatically rebuilds/restarts the server. Configuration is in `.air.toml`.

## Manual Usage

```bash
# Run directly
go run ./cmd/testserver

# Run on custom port
go run ./cmd/testserver -port 9090

# Run on custom host and port
go run ./cmd/testserver -host 127.0.0.1 -port 9090
```

## Purpose

- Development with hot-reload via `air`
- Used by `compare/compare_crawlers.py` for comparing BlueSnake with ScreamingFrog
- Quick iteration during development

## Implementation Note

Both `cmd/testserver` and `cmd/server` use the same `internal/server/` implementation. The test server is simply a lightweight wrapper for development convenience.

## Related

- [Production Server](../server/README.md) - For production deployments
- [API Documentation](../../API.md) - Complete REST API reference
