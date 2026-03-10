#!/usr/bin/env bash
# ============================================================================
# SeaTurt — 卸载脚本
# 用法：./uninstall.sh
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[⚠]${NC} $*"; }

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║   🐢  SeaTurt 卸载                                      ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# ─── 1. 停止运行中的 seaturt 进程 ───
log_info "检查 seaturt 进程..."
if pgrep -x seaturt >/dev/null 2>&1; then
    log_warn "发现运行中的 seaturt 进程，正在停止..."
    pkill -x seaturt 2>/dev/null || true
    sleep 2
    # 如果还没停，强制杀
    if pgrep -x seaturt >/dev/null 2>&1; then
        pkill -9 -x seaturt 2>/dev/null || true
    fi
    log_ok "seaturt 进程已停止"
else
    log_ok "没有运行中的 seaturt 进程"
fi

# ─── 2. 停止 systemd 服务（如果存在） ───
if [[ -f /etc/systemd/system/seaturt.service ]]; then
    log_info "移除 systemd 服务..."
    sudo systemctl stop seaturt 2>/dev/null || true
    sudo systemctl disable seaturt 2>/dev/null || true
    sudo rm -f /etc/systemd/system/seaturt.service
    sudo systemctl daemon-reload 2>/dev/null || true
    log_ok "systemd 服务已移除"
fi

# ─── 3. 停止并删除所有 seaturt 容器 ───
if command -v docker &>/dev/null; then
    log_info "清理 seaturt 容器..."
    local_containers=$(docker ps -a --filter "label=seaturt.managed" -q 2>/dev/null || true)
    if [[ -n "$local_containers" ]]; then
        echo "$local_containers" | xargs docker rm -f 2>/dev/null || true
        log_ok "seaturt 容器已清理"
    else
        log_ok "没有 seaturt 容器需要清理"
    fi

    # ─── 4. 删除沙箱镜像 ───
    log_info "检查沙箱镜像..."
    if docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -q '^seaturt/sandbox:latest$'; then
        docker rmi seaturt/sandbox:latest 2>/dev/null || true
        log_ok "沙箱镜像 seaturt/sandbox:latest 已删除"
    else
        log_ok "沙箱镜像不存在（无需删除）"
    fi
else
    log_warn "Docker 未安装，跳过容器/镜像清理"
fi

# ─── 5. 可选：删除数据目录 ───
DATA_DIR="$HOME/.seaturt"
if [[ -d "$DATA_DIR" ]]; then
    echo ""
    # 检查是否在非交互模式（通过检查 stdin）
    if [[ -t 0 ]]; then
        read -rp "是否删除数据目录 $DATA_DIR？(y/N) " -n 1 REPLY
        echo
        if [[ "$REPLY" =~ ^[Yy]$ ]]; then
            rm -rf "$DATA_DIR"
            log_ok "数据目录已删除: $DATA_DIR"
        else
            log_info "数据目录保留: $DATA_DIR"
        fi
    else
        log_info "非交互模式，数据目录保留: $DATA_DIR"
        log_info "如需删除，请手动执行: rm -rf $DATA_DIR"
    fi
else
    log_ok "数据目录不存在（$DATA_DIR）"
fi

# ─── 6. 可选：删除配置备份 ───
if [[ -f "$SCRIPT_DIR/config.yaml.bak" ]]; then
    rm -f "$SCRIPT_DIR/config.yaml.bak"
    log_ok "配置备份已清理"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║   🐢  SeaTurt 卸载完成                                  ║"
echo "║                                                          ║"
echo "║   注意：Docker 本身未被卸载，如需卸载请手动操作          ║"
echo "║   macOS:  brew uninstall colima docker                   ║"
echo "║   Linux:  sudo apt-get remove docker-ce                  ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
