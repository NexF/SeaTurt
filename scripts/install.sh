#!/usr/bin/env bash
# ============================================================================
# SeaTurt — 一键安装脚本
# 支持平台：macOS (arm64/amd64)、Linux (Ubuntu/Debian/CentOS/Fedora)、WSL2
# 用法：./install.sh [OPTIONS]
# ============================================================================

# --source-only 模式：仅加载函数定义，不执行 main（用于单元测试）
_SEATURT_SOURCE_ONLY=false
for _arg in "$@"; do
    [[ "$_arg" == "--source-only" ]] && _SEATURT_SOURCE_ONLY=true
done

# 严格模式（source-only 时不设置，避免影响测试 shell）
if [[ "$_SEATURT_SOURCE_ONLY" != true ]]; then
    set -euo pipefail
fi

# ============================================================================
# 全局变量
# ============================================================================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEATURT_VERSION="0.3.2"

# 可被测试覆盖的状态变量
OS_TYPE=""          # darwin / linux
ARCH_TYPE=""        # arm64 / amd64
DISTRO=""           # ubuntu / debian / centos / fedora / rhel / unknown
IS_WSL2=false

# 命令行参数（默认值）
AUTO_YES=false
SKIP_CONFIG=false
SKIP_DOCKER=false
FORCE_BUILD=false
AUTO_START=false
INSTALL_SERVICE=false

# 环境变量配置
LLM_API_KEY="${LLM_API_KEY:-}"
LLM_BASE_URL="${LLM_BASE_URL:-https://api.openai.com/v1}"
SEATURT_PORT="${SEATURT_PORT:-8080}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ============================================================================
# 工具函数
# ============================================================================

log_info() {
    echo -e "${CYAN}[INFO]${NC} $*"
}

log_ok() {
    echo -e "${GREEN}[✓]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[⚠]${NC} $*"
}

log_error() {
    echo -e "${RED}[✗]${NC} $*"
}

log_step() {
    echo ""
    echo -e "${CYAN}==> $*${NC}"
}

# ============================================================================
# 平台检测
# ============================================================================

detect_os() {
    case "$(uname -s)" in
        Darwin*)  OS_TYPE="darwin" ;;
        Linux*)   OS_TYPE="linux" ;;
        MINGW*|MSYS*|CYGWIN*) OS_TYPE="windows" ;;
        *)        OS_TYPE="unknown" ;;
    esac

    # 检测 WSL2
    if [[ "$OS_TYPE" == "linux" ]] && grep -qi microsoft /proc/version 2>/dev/null; then
        IS_WSL2=true
    fi
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  ARCH_TYPE="amd64" ;;
        arm64|aarch64) ARCH_TYPE="arm64" ;;
        *)             ARCH_TYPE="unknown" ;;
    esac
}

detect_distro() {
    if [[ "$OS_TYPE" != "linux" ]]; then
        DISTRO="n/a"
        return
    fi

    if [[ -f /etc/os-release ]]; then
        # shellcheck source=/dev/null
        . /etc/os-release
        case "${ID:-}" in
            ubuntu)          DISTRO="ubuntu" ;;
            debian)          DISTRO="debian" ;;
            centos)          DISTRO="centos" ;;
            fedora)          DISTRO="fedora" ;;
            rhel|redhat)     DISTRO="rhel" ;;
            *)               DISTRO="unknown" ;;
        esac
    else
        DISTRO="unknown"
    fi
}

# ============================================================================
# Docker 检测与安装
# ============================================================================

check_docker() {
    # 返回 0 = Docker 就绪，1 = Docker 不可用
    if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
        return 0
    fi
    return 1
}

wait_for_docker() {
    local timeout="${1:-60}"
    local elapsed=0
    local interval=3

    log_info "等待 Docker daemon 就绪（最长 ${timeout}s）..."

    while ! docker info &>/dev/null 2>&1; do
        if (( elapsed >= timeout )); then
            log_error "Docker daemon 未在 ${timeout}s 内就绪"
            log_error "排查建议："
            echo "  1. 检查 Docker 是否正在启动中"
            echo "  2. macOS: 打开 Docker Desktop / OrbStack / 运行 colima start"
            echo "  3. Linux: sudo systemctl start docker"
            echo "  4. 查看日志: docker info 2>&1"
            return 1
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
        echo -n "."
    done
    echo ""
    log_ok "Docker daemon 已就绪"
    return 0
}

# --- macOS Docker 安装 ---

install_colima() {
    log_info "通过 Homebrew 安装 Colima + Docker CLI..."
    brew install colima docker docker-buildx
    log_info "启动 Colima VM..."
    colima start
    wait_for_docker 120
}

install_orbstack() {
    log_info "通过 Homebrew 安装 OrbStack..."
    brew install --cask orbstack
    log_info "启动 OrbStack..."
    open -a OrbStack
    wait_for_docker 120
}

install_docker_desktop() {
    log_info "通过 Homebrew 安装 Docker Desktop..."
    brew install --cask docker
    log_info "启动 Docker Desktop..."
    open -a Docker
    log_warn "Docker Desktop 首次启动可能需要您手动同意服务协议"
    wait_for_docker 120
}

install_docker_macos() {
    if ! command -v brew &>/dev/null; then
        log_error "未检测到 Homebrew，请先安装 Homebrew: https://brew.sh"
        echo "或手动安装以下任一 Docker 运行时："
        echo "  - Colima（推荐）: https://github.com/abiosoft/colima"
        echo "  - OrbStack: https://orbstack.dev"
        echo "  - Docker Desktop: https://www.docker.com/products/docker-desktop/"
        return 1
    fi

    if [[ "$AUTO_YES" == true ]]; then
        # 非交互模式：默认安装 Colima（开源免费，无许可证风险）
        install_colima
        return $?
    fi

    echo ""
    echo "请选择要安装的 Docker 运行时："
    echo ""
    echo "  1) Colima     [推荐] 开源免费、轻量，适合所有用户（MIT 协议）"
    echo "  2) OrbStack   性能最佳，个人免费，商业使用需付费（\$8/用户/月）"
    echo "  3) Docker Desktop  官方标准，个人免费，250人以上企业需付费"
    echo "  4) 跳过       我已有其他 Docker 运行时 / 稍后自行安装"
    echo ""
    read -rp "请选择 [1]: " DOCKER_CHOICE
    DOCKER_CHOICE=${DOCKER_CHOICE:-1}

    case "$DOCKER_CHOICE" in
        1) install_colima ;;
        2) install_orbstack ;;
        3) install_docker_desktop ;;
        4)
            log_warn "跳过 Docker 安装。请确保 docker 可用后重新运行此脚本。"
            return 1
            ;;
        *)
            log_error "无效选择"
            return 1
            ;;
    esac
}

# --- Linux (Debian/Ubuntu) Docker 安装 ---

install_docker_debian() {
    log_info "安装 Docker Engine (apt)..."

    # 卸载旧版本
    sudo apt-get remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true

    # 安装依赖
    sudo apt-get update -qq
    sudo apt-get install -y -qq ca-certificates curl gnupg

    # 添加 Docker 官方 GPG key
    sudo install -m 0755 -d /etc/apt/keyrings
    local id
    id=$(. /etc/os-release && echo "$ID")
    curl -fsSL "https://download.docker.com/linux/${id}/gpg" | \
        sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg 2>/dev/null
    sudo chmod a+r /etc/apt/keyrings/docker.gpg

    # 添加仓库
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
        https://download.docker.com/linux/${id} $(lsb_release -cs) stable" | \
        sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

    # 安装
    sudo apt-get update -qq
    sudo apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin

    # 将当前用户加入 docker 组（免 sudo）
    sudo usermod -aG docker "$USER" 2>/dev/null || true
    log_warn "请重新登录以使 docker 组生效，或执行: newgrp docker"

    # 启动服务
    sudo systemctl enable docker 2>/dev/null || true
    sudo systemctl start docker 2>/dev/null || true
}

# --- Linux (RHEL/CentOS/Fedora) Docker 安装 ---

install_docker_rhel() {
    log_info "安装 Docker Engine (dnf/yum)..."

    # 卸载旧版本
    sudo dnf remove -y docker docker-client docker-latest docker-engine podman buildah 2>/dev/null || true

    # 添加 Docker 仓库
    sudo dnf install -y -q dnf-plugins-core 2>/dev/null || \
        sudo yum install -y -q yum-utils 2>/dev/null || true

    local repo_url="https://download.docker.com/linux/fedora/docker-ce.repo"
    if [[ "$DISTRO" == "centos" || "$DISTRO" == "rhel" ]]; then
        repo_url="https://download.docker.com/linux/centos/docker-ce.repo"
    fi
    sudo dnf config-manager --add-repo "$repo_url" 2>/dev/null || \
        sudo yum-config-manager --add-repo "$repo_url" 2>/dev/null || true

    # 安装
    sudo dnf install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin 2>/dev/null || \
        sudo yum install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin 2>/dev/null || true

    # 用户组 + 启动
    sudo usermod -aG docker "$USER" 2>/dev/null || true
    sudo systemctl enable docker 2>/dev/null || true
    sudo systemctl start docker 2>/dev/null || true
}

# --- Docker 安装主入口 ---

ensure_docker() {
    log_step "Docker 检测 & 安装"

    if [[ "$SKIP_DOCKER" == true ]]; then
        log_info "跳过 Docker 检测（--skip-docker）"
        return 0
    fi

    if check_docker; then
        log_ok "Docker 已就绪 ($(docker --version 2>/dev/null || echo 'unknown version'))"
        return 0
    fi

    log_warn "Docker 未就绪，准备安装..."

    case "$OS_TYPE" in
        darwin)
            install_docker_macos
            ;;
        linux)
            # 检查 sudo 可用性
            if ! command -v sudo &>/dev/null; then
                log_error "需要 sudo 权限来安装 Docker，但 sudo 不可用"
                log_error "请联系系统管理员安装 Docker，或以 root 身份运行"
                return 1
            fi

            # WSL2 特殊处理：检查 systemd
            if [[ "$IS_WSL2" == true ]]; then
                if ! systemctl is-system-running &>/dev/null 2>&1; then
                    log_warn "WSL2 中 systemd 未启用"
                    log_info "正在通过 /etc/wsl.conf 启用 systemd..."
                    sudo bash -c 'echo -e "[boot]\nsystemd=true" > /etc/wsl.conf'
                    log_error "请在 Windows 侧执行 'wsl --shutdown' 后重新运行此脚本"
                    return 1
                fi
            fi

            case "$DISTRO" in
                ubuntu|debian)
                    install_docker_debian
                    ;;
                centos|rhel|fedora)
                    install_docker_rhel
                    ;;
                *)
                    log_error "不支持的发行版: $DISTRO"
                    echo "支持的发行版：Ubuntu、Debian、CentOS、Fedora、RHEL"
                    echo "请手动安装 Docker Engine: https://docs.docker.com/engine/install/"
                    return 1
                    ;;
            esac
            ;;
        *)
            log_error "不支持的操作系统: $OS_TYPE"
            return 1
            ;;
    esac

    # 安装后验证
    wait_for_docker 120
}

# ============================================================================
# Docker 镜像加速（国内网络）
# ============================================================================

# 国内 Docker 镜像加速源列表
# 用户可通过 DOCKER_MIRROR 环境变量覆盖
DOCKER_MIRROR="${DOCKER_MIRROR:-}"

# 已知可用的国内 Docker registry mirror（按优先级排列）
_MIRROR_CANDIDATES=(
    "https://docker.1ms.run"
    "https://docker.xuanyuan.me"
    "https://dockerpull.org"
)

# 检测当前网络是否需要镜像加速
# 如果能直连 ghcr.io 则不需要
_needs_mirror() {
    # 如果用户显式设置了 DOCKER_MIRROR=none，则跳过
    if [[ "$DOCKER_MIRROR" == "none" ]]; then
        return 1
    fi
    # 如果已经设置了自定义 mirror，直接用
    if [[ -n "$DOCKER_MIRROR" ]]; then
        return 0
    fi
    # 尝试连接 ghcr.io，超时 5s
    if curl -sSf --connect-timeout 5 -o /dev/null "https://ghcr.io/v2/" 2>/dev/null; then
        return 1  # 能直连，不需要加速
    fi
    return 0  # 连不上，需要加速
}

# 从候选列表中找到可用的镜像源
_find_working_mirror() {
    if [[ -n "$DOCKER_MIRROR" && "$DOCKER_MIRROR" != "none" ]]; then
        echo "$DOCKER_MIRROR"
        return 0
    fi
    for mirror in "${_MIRROR_CANDIDATES[@]}"; do
        if curl -sSf --connect-timeout 5 -o /dev/null "${mirror}/v2/" 2>/dev/null; then
            echo "$mirror"
            return 0
        fi
    done
    return 1
}

# 配置 Docker daemon 的 registry-mirrors
configure_docker_mirrors() {
    if ! _needs_mirror; then
        log_ok "网络连通性良好，无需配置镜像加速"
        return 0
    fi

    log_info "检测到国内网络环境，正在查找可用的镜像加速源..."

    local mirror
    if ! mirror=$(_find_working_mirror); then
        log_warn "未找到可用的镜像加速源，将使用默认源（可能较慢）"
        log_info "你可以通过 DOCKER_MIRROR=https://your-mirror ./install.sh 手动指定"
        return 0
    fi

    log_ok "找到可用镜像加速源: $mirror"
    DOCKER_MIRROR="$mirror"

    # 配置 Docker daemon.json
    local daemon_json=""
    if [[ "$OS_TYPE" == "darwin" ]]; then
        daemon_json="$HOME/.docker/daemon.json"
    else
        daemon_json="/etc/docker/daemon.json"
    fi

    # 检查是否已配置了 registry-mirrors
    if [[ -f "$daemon_json" ]] && grep -q "registry-mirrors" "$daemon_json" 2>/dev/null; then
        log_info "Docker daemon.json 已有 registry-mirrors 配置，跳过"
        return 0
    fi

    log_info "配置 Docker 镜像加速: $daemon_json"

    if [[ -f "$daemon_json" ]]; then
        # daemon.json 已存在，需要合并
        # 简单策略：如果是合法 JSON 且没有 registry-mirrors，用 python/jq 加入
        if command -v jq &>/dev/null; then
            local tmp_json
            tmp_json=$(mktemp)
            jq --arg m "$mirror" '. + {"registry-mirrors": [$m]}' "$daemon_json" > "$tmp_json" 2>/dev/null
            if [[ $? -eq 0 ]]; then
                if [[ "$OS_TYPE" == "darwin" ]]; then
                    cp "$tmp_json" "$daemon_json"
                else
                    sudo cp "$tmp_json" "$daemon_json"
                fi
                rm -f "$tmp_json"
            else
                rm -f "$tmp_json"
                log_warn "daemon.json 合并失败，跳过镜像加速配置"
                return 0
            fi
        else
            log_warn "未安装 jq，跳过 daemon.json 合并（已有配置文件）"
            log_info "请手动添加 registry-mirrors 到 $daemon_json"
            return 0
        fi
    else
        # daemon.json 不存在，直接创建
        local content="{
  \"registry-mirrors\": [\"$mirror\"]
}"
        if [[ "$OS_TYPE" == "darwin" ]]; then
            mkdir -p "$(dirname "$daemon_json")"
            echo "$content" > "$daemon_json"
        else
            sudo mkdir -p "$(dirname "$daemon_json")"
            echo "$content" | sudo tee "$daemon_json" > /dev/null
        fi
    fi

    log_ok "镜像加速已配置"

    # 重启 Docker daemon 使配置生效
    log_info "重启 Docker daemon 以应用镜像加速配置..."
    if [[ "$OS_TYPE" == "linux" ]]; then
        sudo systemctl restart docker 2>/dev/null || true
    elif [[ "$OS_TYPE" == "darwin" ]]; then
        # macOS: Colima / Docker Desktop / OrbStack 各不相同
        if command -v colima &>/dev/null && colima status 2>/dev/null | grep -qi running; then
            colima restart 2>/dev/null || true
        else
            # Docker Desktop / OrbStack 通常不需要手动重启，读取 ~/.docker/daemon.json 自动生效
            log_info "请手动重启 Docker Desktop / OrbStack 使镜像加速生效"
        fi
    fi

    wait_for_docker 60 || true
}

# 通过代理前缀拉取 ghcr.io / lscr.io 镜像
# 用法: pull_with_mirror <image> [<local_tag>]
# 例如: pull_with_mirror "lscr.io/linuxserver/webtop:ubuntu-kde"
pull_with_mirror() {
    local image="$1"
    local local_tag="${2:-$image}"

    # 如果本地已有该镜像，跳过
    if docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -qF "$local_tag"; then
        log_ok "镜像 $local_tag 已存在，跳过拉取"
        return 0
    fi

    # 先尝试直接拉取
    log_info "拉取镜像: $image ..."
    if docker pull "$image" 2>/dev/null; then
        if [[ "$image" != "$local_tag" ]]; then
            docker tag "$image" "$local_tag"
        fi
        return 0
    fi

    # 直接拉取失败，尝试通过加速代理
    if [[ -n "$DOCKER_MIRROR" && "$DOCKER_MIRROR" != "none" ]]; then
        # 构造代理镜像地址：把 lscr.io/xxx 变成 mirror/lscr.io/xxx
        local mirror_host
        mirror_host=$(echo "$DOCKER_MIRROR" | sed 's|https\?://||')
        local proxied_image="${mirror_host}/${image}"

        log_info "直接拉取失败，尝试通过加速源: $proxied_image ..."
        if docker pull "$proxied_image" 2>/dev/null; then
            docker tag "$proxied_image" "$local_tag"
            docker rmi "$proxied_image" 2>/dev/null || true
            log_ok "通过加速源拉取成功: $local_tag"
            return 0
        fi
    fi

    log_error "镜像拉取失败: $image"
    log_error "排查建议："
    echo "  1. 检查网络连接"
    echo "  2. 手动指定加速源: DOCKER_MIRROR=https://your-mirror ./install.sh"
    echo "  3. 手动拉取: docker pull $image"
    return 1
}

# ============================================================================
# 沙箱镜像构建
# ============================================================================

check_disk_space() {
    local required_gb="${1:-10}"
    local target_dir="${2:-$SCRIPT_DIR}"

    local available_kb
    if [[ "$OS_TYPE" == "darwin" ]]; then
        available_kb=$(df -k "$target_dir" | awk 'NR==2 {print $4}')
    else
        available_kb=$(df -k "$target_dir" | awk 'NR==2 {print $4}')
    fi

    local available_gb=$((available_kb / 1024 / 1024))

    if (( available_gb < required_gb )); then
        log_error "磁盘空间不足！需要至少 ${required_gb}GB，当前可用 ${available_gb}GB"
        echo "请清理磁盘空间后重试"
        return 1
    fi

    log_ok "磁盘空间充足（可用 ${available_gb}GB，需要 ${required_gb}GB）"
    return 0
}

build_sandbox_image() {
    log_step "沙箱镜像构建"

    local dockerfile_dir="$SCRIPT_DIR/docker"

    if [[ ! -f "$dockerfile_dir/Dockerfile" ]]; then
        log_error "未找到 Dockerfile: $dockerfile_dir/Dockerfile"
        echo "请确保 release 包完整，docker/ 目录下应包含 Dockerfile"
        return 1
    fi

    # 检查镜像是否已存在
    if [[ "$FORCE_BUILD" != true ]]; then
        if docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -q '^seaturt/sandbox:latest$'; then
            log_ok "镜像 seaturt/sandbox:latest 已存在（使用 --force-build 强制重建）"
            return 0
        fi
    fi

    # 磁盘空间检查
    check_disk_space 10 "$SCRIPT_DIR" || return 1

    # 预拉取基础镜像（通过镜像加速）
    # 从 Dockerfile 提取 FROM 镜像名
    local base_image
    base_image=$(grep -m1 '^FROM ' "$dockerfile_dir/Dockerfile" | awk '{print $2}')
    if [[ -n "$base_image" ]]; then
        log_info "预拉取基础镜像: $base_image"
        pull_with_mirror "$base_image" || {
            log_warn "基础镜像预拉取失败，docker build 将自行拉取（可能较慢）"
        }
    fi

    log_info "构建镜像 seaturt/sandbox:latest ..."
    echo "    Dockerfile: $dockerfile_dir/Dockerfile"

    if ! docker build -t seaturt/sandbox:latest "$dockerfile_dir/"; then
        log_error "镜像构建失败！"
        echo "排查建议："
        echo "  1. 检查网络连接（Docker 需要从网络拉取基础镜像）"
        echo "  2. 检查磁盘空间: df -h"
        echo "  3. 重试: ./install.sh --force-build"
        echo "  4. 手动构建: docker build -t seaturt/sandbox:latest $dockerfile_dir/"
        return 1
    fi

    log_ok "镜像构建成功: seaturt/sandbox:latest"
}

# ============================================================================
# 配置引导
# ============================================================================

check_config() {
    # 返回 0 = 配置完整，1 = 需要配置
    local config_file="$SCRIPT_DIR/config.yaml"

    if [[ ! -f "$config_file" ]]; then
        return 1
    fi

    # 检查是否有 provider 配置（至少有一个 api_key 不为空）
    if grep -q 'api_key:' "$config_file" 2>/dev/null; then
        # 检查是否所有 api_key 都是占位符
        local real_keys
        real_keys=$(grep 'api_key:' "$config_file" | grep -v 'YOUR_API_KEY_HERE' | grep -v '^#' | grep -v '^\s*#' || true)
        if [[ -n "$real_keys" ]]; then
            return 0
        fi
    fi

    return 1
}

setup_config() {
    log_step "配置引导"

    local config_file="$SCRIPT_DIR/config.yaml"

    if [[ "$SKIP_CONFIG" == true ]]; then
        log_info "跳过配置引导（--skip-config）"
        log_info "请手动编辑: $config_file"
        return 0
    fi

    # 如果环境变量已设置，直接应用
    if [[ -n "$LLM_API_KEY" ]]; then
        apply_env_config
        return 0
    fi

    # 检查现有配置
    if check_config; then
        log_ok "配置文件已包含有效的 LLM 配置"
        return 0
    fi

    if [[ "$AUTO_YES" == true ]]; then
        log_warn "非交互模式但未设置 LLM_API_KEY 环境变量"
        log_warn "请设置后重新运行: LLM_API_KEY=sk-xxx ./install.sh -y"
        log_warn "或手动编辑: $config_file"
        return 0
    fi

    # 交互式配置引导
    echo ""
    echo "═══════════════════════════════════════════════════"
    echo "  SeaTurt 需要配置 LLM API 来提供 AI 能力"
    echo "═══════════════════════════════════════════════════"
    echo ""

    read -rp "请输入 LLM API Key（如 sk-xxx）: " input_api_key
    if [[ -z "$input_api_key" ]]; then
        log_warn "未输入 API Key，跳过配置"
        log_info "稍后可手动编辑: $config_file"
        return 0
    fi

    read -rp "请输入 LLM Base URL [${LLM_BASE_URL}]: " input_base_url
    input_base_url="${input_base_url:-$LLM_BASE_URL}"

    LLM_API_KEY="$input_api_key"
    LLM_BASE_URL="$input_base_url"

    apply_env_config
}

apply_env_config() {
    local config_file="$SCRIPT_DIR/config.yaml"

    if [[ ! -f "$config_file" ]]; then
        log_error "配置文件不存在: $config_file"
        return 1
    fi

    log_info "写入 LLM 配置到 $config_file ..."

    # 备份原始配置
    cp "$config_file" "${config_file}.bak"

    # 检查是否已有 openai provider
    if grep -q 'openai:' "$config_file" 2>/dev/null; then
        # 更新已有的 openai provider 的 api_key
        if [[ "$OS_TYPE" == "darwin" ]]; then
            sed -i '' "s|api_key:.*# openai|api_key: ${LLM_API_KEY} # openai|" "$config_file" 2>/dev/null || true
        else
            sed -i "s|api_key:.*# openai|api_key: ${LLM_API_KEY} # openai|" "$config_file" 2>/dev/null || true
        fi
    else
        # 追加 openai provider 配置
        cat >> "$config_file" <<EOF

  openai:
    base_url: ${LLM_BASE_URL}
    api: openai-completions
    api_key: ${LLM_API_KEY} # openai
    models:
      - id: gpt-4o
        name: GPT-4o
        reasoning: false
        input: [text, image]
        context_window: 128000
        max_tokens: 16384
EOF
    fi

    log_ok "LLM 配置已写入"
}

# ============================================================================
# 健康检查
# ============================================================================

health_check() {
    log_step "健康检查"

    local all_ok=true

    # 1. Docker daemon
    if check_docker; then
        log_ok "Docker daemon 运行中"
    else
        log_error "Docker daemon 未运行"
        all_ok=false
    fi

    # 2. 沙箱镜像
    if docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -q '^seaturt/sandbox:latest$'; then
        log_ok "沙箱镜像 seaturt/sandbox:latest 存在"
    else
        log_error "沙箱镜像 seaturt/sandbox:latest 不存在"
        all_ok=false
    fi

    # 3. config.yaml
    if [[ -f "$SCRIPT_DIR/config.yaml" ]]; then
        log_ok "配置文件 config.yaml 存在"
    else
        log_error "配置文件 config.yaml 不存在"
        all_ok=false
    fi

    # 4. seaturt 二进制
    if [[ -x "$SCRIPT_DIR/seaturt" ]]; then
        log_ok "seaturt 二进制可执行"
    else
        log_error "seaturt 二进制不存在或无执行权限"
        all_ok=false
    fi

    # 5. 端口检查
    local port="$SEATURT_PORT"
    if command -v lsof &>/dev/null; then
        if lsof -i ":$port" &>/dev/null 2>&1; then
            log_warn "端口 $port 已被占用（可通过 SEATURT_PORT 环境变量更改）"
        else
            log_ok "端口 $port 可用"
        fi
    elif command -v ss &>/dev/null; then
        if ss -tlnp 2>/dev/null | grep -q ":$port "; then
            log_warn "端口 $port 已被占用（可通过 SEATURT_PORT 环境变量更改）"
        else
            log_ok "端口 $port 可用"
        fi
    else
        log_info "无法检测端口状态（跳过）"
    fi

    if [[ "$all_ok" == true ]]; then
        return 0
    else
        return 1
    fi
}

# ============================================================================
# 安装完成提示
# ============================================================================

print_success() {
    local port="$SEATURT_PORT"
    echo ""
    echo "╔══════════════════════════════════════════════════════════╗"
    echo "║                                                          ║"
    echo "║   🐢  SeaTurt 安装完成！                                 ║"
    echo "║                                                          ║"
    echo "╠══════════════════════════════════════════════════════════╣"
    echo "║                                                          ║"
    echo "║   启动命令:                                              ║"
    echo "║     cd $SCRIPT_DIR && ./seaturt                          "
    echo "║                                                          ║"
    echo "║   访问地址:                                              ║"
    echo "║     http://localhost:${port}                             "
    echo "║                                                          ║"
    echo "║   配置文件:                                              ║"
    echo "║     $SCRIPT_DIR/config.yaml                              "
    echo "║                                                          ║"
    echo "║   卸载:                                                  ║"
    echo "║     cd $SCRIPT_DIR && ./uninstall.sh                     "
    echo "║                                                          ║"
    echo "╚══════════════════════════════════════════════════════════╝"
    echo ""
}

# ============================================================================
# 参数解析
# ============================================================================

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --skip-config)
                SKIP_CONFIG=true
                shift
                ;;
            --skip-docker)
                SKIP_DOCKER=true
                shift
                ;;
            --force-build)
                FORCE_BUILD=true
                shift
                ;;
            --start)
                AUTO_START=true
                shift
                ;;
            --install-service)
                INSTALL_SERVICE=true
                shift
                ;;
            --yes|-y)
                AUTO_YES=true
                shift
                ;;
            --source-only)
                # 已在脚本开头处理，这里跳过
                shift
                ;;
            --help|-h)
                show_help
                exit 0
                ;;
            *)
                log_error "未知参数: $1"
                show_help
                exit 1
                ;;
        esac
    done
}

show_help() {
    cat <<EOF
SeaTurt 安装脚本 v${SEATURT_VERSION}

Usage: ./install.sh [OPTIONS]

Options:
  --skip-config         跳过配置引导，用户自行编辑 config.yaml
  --skip-docker         跳过 Docker 安装（假设已安装）
  --force-build         强制重新构建沙箱镜像（即使已存在）
  --start               安装完成后自动启动 seaturt
  --install-service     (Linux) 注册为 systemd 服务
  --yes, -y             所有确认提示自动选 Yes（非交互模式）
  --source-only         仅加载函数定义，不执行 main（用于测试）
  --help, -h            显示帮助信息

Environment Variables:
  LLM_API_KEY           LLM API 密钥（设置后可跳过交互式配置）
  LLM_BASE_URL          LLM API 地址（默认: https://api.openai.com/v1）
  SEATURT_PORT          服务监听端口（默认: 8080）

Examples:
  ./install.sh                                    # 交互式安装
  ./install.sh -y                                 # 非交互式安装（macOS 默认安装 Colima）
  LLM_API_KEY=sk-xxx ./install.sh -y              # 全自动安装
  ./install.sh --skip-docker --skip-config        # 仅构建镜像
  ./install.sh --force-build                      # 强制重新构建镜像
EOF
}

# ============================================================================
# 主流程
# ============================================================================

main() {
    parse_args "$@"

    echo ""
    echo "╔══════════════════════════════════════════════════════════╗"
    echo "║   🐢  SeaTurt Installer v${SEATURT_VERSION}                          ║"
    echo "╚══════════════════════════════════════════════════════════╝"
    echo ""

    # 1. 平台检测
    log_step "平台检测"
    detect_os
    detect_arch
    detect_distro
    log_ok "OS: $OS_TYPE | Arch: $ARCH_TYPE | Distro: $DISTRO | WSL2: $IS_WSL2"

    if [[ "$OS_TYPE" == "unknown" ]]; then
        log_error "不支持的操作系统"
        exit 1
    fi

    if [[ "$ARCH_TYPE" == "unknown" ]]; then
        log_error "不支持的 CPU 架构: $(uname -m)"
        exit 1
    fi

    # 2. Docker 检测 & 安装
    ensure_docker || exit 1

    # 3. 配置 Docker 镜像加速（国内网络）
    configure_docker_mirrors

    # 4. 沙箱镜像构建
    build_sandbox_image || exit 1

    # 5. 配置引导
    setup_config || true  # 配置失败不阻断安装

    # 6. 健康检查
    health_check || log_warn "部分检查未通过，请检查上述提示"

    # 7. 完成
    print_success

    # 可选：注册 systemd 服务
    if [[ "$INSTALL_SERVICE" == true && "$OS_TYPE" == "linux" ]]; then
        install_systemd_service
    fi

    # 可选：自动启动
    if [[ "$AUTO_START" == true ]]; then
        log_info "启动 SeaTurt..."
        cd "$SCRIPT_DIR"
        exec ./seaturt
    fi
}

# systemd 服务注册（Linux only）
install_systemd_service() {
    log_step "注册 systemd 服务"

    local service_file="/etc/systemd/system/seaturt.service"
    local seaturt_bin="$SCRIPT_DIR/seaturt"

    sudo tee "$service_file" > /dev/null <<EOF
[Unit]
Description=SeaTurt AI Agent
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=$USER
WorkingDirectory=$SCRIPT_DIR
ExecStart=$seaturt_bin
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable seaturt
    log_ok "systemd 服务已注册：seaturt.service"
    log_info "启动: sudo systemctl start seaturt"
    log_info "状态: sudo systemctl status seaturt"
    log_info "日志: journalctl -u seaturt -f"
}

# ============================================================================
# 入口：source-only 模式不执行 main
# ============================================================================
if [[ "$_SEATURT_SOURCE_ONLY" != true ]]; then
    main "$@"
fi
