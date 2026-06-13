#!/usr/bin/env bash
#
# bluesnake CLI installer (Linux).
#
#   curl -fsSL https://snake.blue/install.sh | bash
#
# Options (pass as environment variables):
#   VERSION=v0.1.0            install a specific release (default: latest)
#   BLUESNAKE_INSTALL_DIR=…   install location (default: /usr/local/bin)
#   BLUESNAKE_BIN_NAME=…      command name to install as (default: bluesnake)
#   BLUESNAKE_REPO=owner/repo source repo (default: agentberlin/bluesnake)
#
#   curl -fsSL https://snake.blue/install.sh | VERSION=v0.1.0 bash
#   curl -fsSL https://snake.blue/install.sh | BLUESNAKE_INSTALL_DIR="$HOME/.local/bin" bash
#
# Re-running this script upgrades an existing install in place. The whole script
# is wrapped in main() and invoked on the last line, so a truncated download
# (e.g. a dropped connection mid-pipe) executes nothing rather than half a script.
set -euo pipefail

err()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }

# dl <url> <output-path> — download with curl or wget, whichever exists.
dl() {
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --proto '=https' --tlsv1.2 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    err "need 'curl' or 'wget' to download"
  fi
}

main() {
  local REPO="${BLUESNAKE_REPO:-agentberlin/bluesnake}"
  local BIN_NAME="${BLUESNAKE_BIN_NAME:-bluesnake}"
  local INSTALL_DIR="${BLUESNAKE_INSTALL_DIR:-/usr/local/bin}"
  local VERSION="${VERSION:-latest}"
  local os arch asset base url expected actual dest
  # NOTE: tmp is intentionally NOT local — the EXIT trap below runs in global
  # scope after main() returns, and under `set -u` a local would be unbound there.

  # --- OS check: Linux only (macOS ships a .dmg; Windows ships a desktop app) ---
  os="$(uname -s)"
  case "$os" in
    Linux) ;;
    Darwin) err "macOS isn't supported by this installer — download the bluesnake .dmg from https://github.com/${REPO}/releases/latest" ;;
    *) err "unsupported OS: ${os} (this installer supports Linux)" ;;
  esac

  # --- architecture detection ---
  case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) err "unsupported architecture: $(uname -m) (supported: x86_64/amd64, aarch64/arm64)" ;;
  esac

  asset="bluesnake-linux-${arch}"
  base="https://github.com/${REPO}/releases"
  if [ "$VERSION" = "latest" ]; then
    url="${base}/latest/download/${asset}"
  else
    url="${base}/download/${VERSION}/${asset}"
  fi

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  info "Downloading ${asset} (${VERSION}) …"
  dl "$url" "$tmp/${asset}" || err "download failed: ${url}"

  # --- checksum verification -------------------------------------------------
  # If the release publishes a .sha256 sidecar (ours always do), enforce it
  # strictly: a malformed or mismatched checksum is a hard failure. We compute
  # the hash and compare directly rather than trusting `sha256sum -c`, whose exit
  # code is 0 for an "improperly formatted" line — so an empty/corrupt sidecar
  # would otherwise pass silently. A genuinely absent sidecar is a (loud) skip.
  if dl "${url}.sha256" "$tmp/${asset}.sha256" 2>/dev/null && [ -s "$tmp/${asset}.sha256" ]; then
    expected="$(awk '{print $1; exit}' "$tmp/${asset}.sha256" | tr -d '[:space:]')"
    if ! printf '%s' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$'; then
      err "published checksum is malformed — aborting"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$tmp/${asset}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      actual="$(shasum -a 256 "$tmp/${asset}" | awk '{print $1}')"
    else
      actual=""
      info "no sha256 tool (sha256sum/shasum) found — skipping checksum verification"
    fi
    if [ -n "$actual" ]; then
      if [ "$(printf '%s' "$actual" | tr 'A-F' 'a-f')" = "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" ]; then
        info "Checksum verified."
      else
        err "checksum verification FAILED — aborting (expected ${expected}, got ${actual})"
      fi
    fi
  else
    info "No published checksum for this release — skipping verification"
  fi

  chmod +x "$tmp/${asset}"

  # --- install (use sudo only if the target dir isn't writable) ---
  dest="${INSTALL_DIR%/}/${BIN_NAME}"
  info "Installing to ${dest} …"
  if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; }; then
    install -m 0755 "$tmp/${asset}" "$dest"
  elif command -v sudo >/dev/null 2>&1; then
    info "Elevated permissions needed to write ${INSTALL_DIR}; using sudo."
    sudo install -m 0755 "$tmp/${asset}" "$dest"
  else
    err "cannot write to ${INSTALL_DIR} and 'sudo' is unavailable — set BLUESNAKE_INSTALL_DIR to a writable directory"
  fi

  info "Installed ${BIN_NAME} → ${dest}"
  case ":$PATH:" in
    *":${INSTALL_DIR%/}:"*) ;;
    *) info "Note: ${INSTALL_DIR} is not on your PATH — add it to use '${BIN_NAME}' directly." ;;
  esac
  "${dest}" version 2>/dev/null || info "Run '${BIN_NAME} version' to confirm the install."
}

main "$@"
