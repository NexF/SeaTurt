#!/usr/bin/env bash
# build.sh — 构建 mcp-server-wechat
#
# 纯 Python 一层架构，用 PyInstaller 打包成单文件二进制。
# 和 mcp-server-search 相同模式。
#
# ⚠️ 注意: AT-SPI2 (gi.repository.Atspi) 是系统 GObject Introspection 绑定，
# PyInstaller 需要正确收集 GI typelib 文件。必须在容器内（有 AT-SPI2 环境）构建。
#
# 产出:
#   dist/mcp-server-wechat — 单文件二进制（放到 .seaturt/tools/）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "==> Installing Python dependencies..."
pip install -r requirements.txt

echo "==> Building mcp-server-wechat with PyInstaller..."
pyinstaller \
    --onefile \
    --name mcp-server-wechat \
    --clean \
    --noconfirm \
    --collect-all gi \
    --hidden-import gi.repository.Atspi \
    main.py

echo "==> Built: $SCRIPT_DIR/dist/mcp-server-wechat"
ls -lh "$SCRIPT_DIR/dist/mcp-server-wechat"
