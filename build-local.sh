#!/bin/bash

# build-local.sh - Build and install BlueSnake app locally
set -e

# Get the repo root (where this script is located)
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$REPO_ROOT/cmd/desktop"

echo "🔨 Building BlueSnake..."
cd "$DESKTOP_DIR"
wails build --clean

echo "📦 Installing to ~/Applications..."
# Create ~/Applications if it doesn't exist
mkdir -p ~/Applications

# Remove old version if it exists
if [ -d ~/Applications/BlueSnake.app ]; then
    echo "🗑️  Removing old version..."
    rm -rf ~/Applications/BlueSnake.app
fi

# Copy the new build
echo "📋 Copying BlueSnake.app to ~/Applications..."
cp -R "$DESKTOP_DIR/build/bin/BlueSnake.app" ~/Applications/

echo "✅ Done! BlueSnake.app is now in ~/Applications"
echo ""
echo "You can now:"
echo "  • Search for 'BlueSnake' using Spotlight (Cmd+Space)"
echo "  • Open it from Finder → Applications"
echo "  • Drag it to your Dock to pin it"
echo ""
echo "To launch now: open ~/Applications/BlueSnake.app"
