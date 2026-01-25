# BlueSnake Server

The production HTTP server for BlueSnake, providing a REST API for web crawling and SEO analysis.

## Quick Start

```bash
# Run with default settings (0.0.0.0:8080)
go run ./cmd/server

# Run on custom port
go run ./cmd/server -port 9090

# Run on custom host and port
go run ./cmd/server -host 127.0.0.1 -port 9090

# Show version
go run ./cmd/server -version
```

## Building

```bash
# Build the server binary
go build -o bluesnake-server ./cmd/server

# Run the binary
./bluesnake-server -port 8080
```

## Docker

```bash
# Build the image
docker build -t bluesnake-server .

# Run the container
docker run -p 8080:8080 bluesnake-server
```

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | `0.0.0.0` | Host to bind the server to |
| `-port` | `8080` | Port to run the server on |
| `-version` | - | Print version and exit |

## API Documentation

See [API.md](../../API.md) for complete REST API documentation.

### Quick API Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/health` | GET | Health check |
| `/api/v1/version` | GET | Server version |
| `/api/v1/projects` | GET | List all projects |
| `/api/v1/crawl` | POST | Start a new crawl |
| `/api/v1/active-crawls` | GET | List active crawls |
| `/api/v1/crawls/{id}` | GET | Get crawl results |
| `/api/v1/config` | GET/PUT | Manage crawl configuration |
| `/api/v1/search/{crawlId}` | GET | Search crawl results |

## Data Storage

The server stores data in SQLite at `~/.bluesnake/bluesnake.db`. This is shared with the Desktop app and MCP server.

## Production Deployment

For production deployments, consider:

1. **Reverse Proxy**: Use nginx or similar for TLS termination
2. **Process Manager**: Use systemd, supervisor, or Docker for process management
3. **Monitoring**: The `/api/v1/health` endpoint can be used for health checks
4. **Backups**: Periodically backup `~/.bluesnake/bluesnake.db`

### Example systemd Service

```ini
[Unit]
Description=BlueSnake Server
After=network.target

[Service]
Type=simple
User=bluesnake
ExecStart=/usr/local/bin/bluesnake-server -port 8080
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Related

- [API Documentation](../../API.md) - Complete REST API reference
- [Architecture](../../ARCHITECTURE.md) - System architecture overview
- [Test Server](../testserver/README.md) - Development server with hot-reload
