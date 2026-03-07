#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "==> Installing Python dependencies..."
pip install -r requirements.txt

echo "==> Building mcp-server-search with PyInstaller..."
pyinstaller \
    --onefile \
    --name mcp-server-search \
    --clean \
    --noconfirm \
    main.py

echo "==> Built: $SCRIPT_DIR/dist/mcp-server-search"
ls -lh "$SCRIPT_DIR/dist/mcp-server-search"
