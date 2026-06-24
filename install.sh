#!/usr/bin/env bash
set -euo pipefail

REPO="pottom/hopscotch"
BINARY="hopscotch"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="${HOME}/.config/hopscotch"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

# ── Platform detection ────────────────────────────────────────────────────────

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
    darwin|linux) ;;
    *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

ASSET="${BINARY}-${OS}-${ARCH}"

# ── Latest release ────────────────────────────────────────────────────────────

echo "Fetching latest release..."
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | head -1 \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')

if [[ -z "$TAG" ]]; then
    echo "Failed to fetch latest release tag." >&2
    exit 1
fi

echo "Latest version: $TAG"

URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

# ── Download and install binary ───────────────────────────────────────────────

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

echo "Downloading ${ASSET}..."
curl -fsSL "$URL" -o "$TMP"
chmod +x "$TMP"

if [[ -w "$INSTALL_DIR" ]]; then
    mv "$TMP" "${INSTALL_DIR}/${BINARY}"
else
    echo "Installing to ${INSTALL_DIR} (sudo required)..."
    sudo mv "$TMP" "${INSTALL_DIR}/${BINARY}"
fi

echo "Installed: $(${INSTALL_DIR}/${BINARY} version)"

# ── Example config ────────────────────────────────────────────────────────────

mkdir -p "$CONFIG_DIR"

if [[ -f "$CONFIG_FILE" ]]; then
    echo "Config already exists, skipping: ${CONFIG_FILE}"
else
    echo "Creating example config: ${CONFIG_FILE}"
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/hopscotch.example.yaml" \
        -o "$CONFIG_FILE"
    echo ""
    echo "Edit ${CONFIG_FILE} to configure your tunnels, then run:"
    echo "  hopscotch start"
fi
