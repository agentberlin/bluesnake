.PHONY: dev build install clean reset-db help

# Default target
help:
	@echo "BlueSnake Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make dev       - Run Wails development server"
	@echo "  make build     - Build BlueSnake for local machine"
	@echo "  make install   - Install BlueSnake.app to ~/Applications (builds first)"
	@echo "  make clean     - Clean build artifacts"
	@echo "  make reset-db  - Delete the database file from ~/.bluesnake/"

# Run Wails development server
dev:
	@echo "🚀 Starting Wails dev server..."
	cd cmd/desktop && wails dev

# Build for local machine
build:
	@echo "🔨 Building BlueSnake..."
	cd cmd/desktop && wails build --clean -tags desktop
	@echo "✅ Build complete! Binary is in cmd/desktop/build/bin/"

# Install to ~/Applications (builds first if needed)
install: build
	@echo "📦 Installing to ~/Applications..."
	@mkdir -p ~/Applications
	@if [ -d ~/Applications/BlueSnake.app ]; then \
		echo "🗑️  Removing old version..."; \
		rm -rf ~/Applications/BlueSnake.app; \
	fi
	@echo "📋 Copying BlueSnake.app to ~/Applications..."
	@cp -R cmd/desktop/build/bin/BlueSnake.app ~/Applications/
	@echo "✅ Done! BlueSnake.app is now in ~/Applications"
	@echo ""
	@echo "You can now:"
	@echo "  • Search for 'BlueSnake' using Spotlight (Cmd+Space)"
	@echo "  • Open it from Finder → Applications"
	@echo "  • Drag it to your Dock to pin it"
	@echo ""
	@echo "To launch now: open ~/Applications/BlueSnake.app"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -rf cmd/desktop/build
	@echo "✅ Clean complete!"

# Delete database file
reset-db:
	@echo "🗑️  Deleting database file..."
	@if [ -f ~/.bluesnake/bluesnake.db ]; then \
		rm ~/.bluesnake/bluesnake.db; \
		echo "✅ Database deleted: ~/.bluesnake/bluesnake.db"; \
	else \
		echo "ℹ️  Database file not found: ~/.bluesnake/bluesnake.db"; \
	fi
