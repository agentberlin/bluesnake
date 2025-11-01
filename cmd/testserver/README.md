# Test Server

This is a minimal HTTP server wrapper used for testing and development purposes.

## Purpose

- Used by `compare/compare_crawlers.py` for comparing BlueSnake with ScreamingFrog
- Provides HTTP API access to BlueSnake crawling functionality
- For development and testing only (not for end-user distribution)

## Usage

```bash
# Run with default settings (localhost:8080)
go run ./cmd/testserver

# Run on custom port
go run ./cmd/testserver -port 9090

# Run on custom host and port
go run ./cmd/testserver -host 127.0.0.1 -port 9090
```

## Implementation Note

The actual server implementation lives in `internal/server/` and can be imported by:
- This test server
- Future CLI tools (when implemented)
- The desktop app (if needed for MCP or local API access)

This wrapper just bootstraps the server for standalone HTTP access during testing.
