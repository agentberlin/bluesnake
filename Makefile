.PHONY: dev build clean help

# Default target
help:
	@echo "BlueSnake Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make dev     - Run Wails development server"
	@echo "  make build   - Build BlueSnake for local machine"
	@echo "  make clean   - Clean build artifacts"

# Run Wails development server
dev:
	@echo "🚀 Starting Wails dev server..."
	cd cmd/desktop && wails dev

# Build for local machine (similar to build-local.sh)
build:
	@echo "🔨 Building BlueSnake..."
	cd cmd/desktop && wails build --clean -tags desktop
	@echo "✅ Build complete! Binary is in cmd/desktop/build/bin/"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -rf cmd/desktop/build
	@echo "✅ Clean complete!"
