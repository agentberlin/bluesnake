#!/bin/bash
# Install the bluesnake command-line tool.
#
# The `bluesnake` CLI ships inside bluesnake.app. This script symlinks it onto
# your PATH so you can run `bluesnake` from any terminal. Drag bluesnake to your
# Applications folder first, then double-click this file.
set -euo pipefail

APP="/Applications/bluesnake.app"
if [ ! -d "$APP" ]; then
  # Fall back to an app sitting next to this script (e.g. on the mounted DMG).
  HERE="$(cd "$(dirname "$0")" && pwd)"
  if [ -d "$HERE/bluesnake.app" ]; then
    APP="$HERE/bluesnake.app"
  fi
fi

CLI="$APP/Contents/Resources/bin/bluesnake"
if [ ! -x "$CLI" ]; then
  echo "Could not find bluesnake.app in /Applications."
  echo "Drag bluesnake into your Applications folder first, then run this again."
  read -n 1 -s -r -p "Press any key to close…"; echo
  exit 1
fi

# Prefer a system bin dir if it's writable, otherwise use ~/.local/bin.
TARGET=""
for d in /usr/local/bin /opt/homebrew/bin; do
  if [ -d "$d" ] && [ -w "$d" ]; then TARGET="$d/bluesnake"; break; fi
done
if [ -z "$TARGET" ]; then
  mkdir -p "$HOME/.local/bin"
  TARGET="$HOME/.local/bin/bluesnake"
fi

ln -sf "$CLI" "$TARGET"
echo "Linked bluesnake → $TARGET"

# Warn if the chosen dir isn't on PATH.
case ":$PATH:" in
  *":$(dirname "$TARGET"):"*) ;;
  *) echo "Note: $(dirname "$TARGET") is not on your PATH — add it to use 'bluesnake' directly." ;;
esac

echo "Done. Open a new terminal and run: bluesnake version"
read -n 1 -s -r -p "Press any key to close…"; echo
