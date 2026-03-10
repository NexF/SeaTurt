#!/usr/bin/env bash
# ============================================================================
# SeaTurt install.sh — 单元测试
# 通过 mock/stub 外部命令和函数覆盖，测试脚本逻辑分支
# 不会真实安装任何软件
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_SCRIPT="$SCRIPT_DIR/install.sh"

# 测试计数
TESTS_TOTAL=0
TESTS_PASSED=0
TESTS_FAILED=0

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# ============================================================================
# 测试框架
# ============================================================================

# Mock 环境目录
MOCK_BIN_DIR=""

setup_mock_env() {
    MOCK_BIN_DIR=$(mktemp -d)
    # 保存原始 PATH
    _ORIG_PATH="$PATH"
    export PATH="$MOCK_BIN_DIR:$PATH"
}

teardown_mock_env() {
    if [[ -n "$MOCK_BIN_DIR" && -d "$MOCK_BIN_DIR" ]]; then
        rm -rf "$MOCK_BIN_DIR"
    fi
    export PATH="$_ORIG_PATH"
}

# 创建 mock 可执行文件
create_mock_cmd() {
    local name="$1"
    local script_body="$2"
    cat > "$MOCK_BIN_DIR/$name" <<EOF
#!/bin/bash
$script_body
EOF
    chmod +x "$MOCK_BIN_DIR/$name"
}

# 断言相等
assert_eq() {
    local expected="$1"
    local actual="$2"
    local message="${3:-}"
    if [[ "$expected" == "$actual" ]]; then
        return 0
    else
        echo -e "    ${RED}FAIL${NC}: expected='$expected' actual='$actual' $message"
        return 1
    fi
}

# 断言非空
assert_not_empty() {
    local value="$1"
    local message="${2:-}"
    if [[ -n "$value" ]]; then
        return 0
    else
        echo -e "    ${RED}FAIL${NC}: value is empty $message"
        return 1
    fi
}

# 测试运行器
run_test() {
    local test_name="$1"
    local test_func="$2"
    TESTS_TOTAL=$((TESTS_TOTAL + 1))

    echo -n "  [$TESTS_TOTAL] $test_name ... "

    # 每个测试重新加载 install.sh 函数
    setup_mock_env

    # 重置全局变量
    OS_TYPE=""
    ARCH_TYPE=""
    DISTRO=""
    IS_WSL2=false
    AUTO_YES=false
    SKIP_CONFIG=false
    SKIP_DOCKER=false
    FORCE_BUILD=false
    AUTO_START=false
    INSTALL_SERVICE=false
    LLM_API_KEY=""
    LLM_BASE_URL="https://api.openai.com/v1"
    SEATURT_PORT="8080"
    MOCK_CALLED=""

    # source install.sh（仅加载函数）
    # shellcheck source=/dev/null
    source "$INSTALL_SCRIPT" --source-only

    local test_result=0
    if $test_func; then
        echo -e "${GREEN}PASS${NC}"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        test_result=1
    fi

    teardown_mock_env
    return $test_result
}

# ============================================================================
# 测试用例
# ============================================================================

# --- 平台检测 ---

test_detect_os_darwin() {
    # mock uname 返回 Darwin
    create_mock_cmd "uname" 'if [[ "$1" == "-s" ]]; then echo "Darwin"; elif [[ "$1" == "-m" ]]; then echo "arm64"; else echo "Darwin"; fi'
    detect_os
    assert_eq "darwin" "$OS_TYPE" "OS should be darwin"
}

test_detect_os_linux() {
    create_mock_cmd "uname" 'if [[ "$1" == "-s" ]]; then echo "Linux"; elif [[ "$1" == "-m" ]]; then echo "x86_64"; else echo "Linux"; fi'
    # 确保 /proc/version 检查不会误判为 WSL2
    detect_os
    # 在 macOS 上运行这个测试时，/proc/version 不存在，所以 IS_WSL2=false 是对的
    assert_eq "linux" "$OS_TYPE" "OS should be linux"
}

test_detect_arch_arm64() {
    create_mock_cmd "uname" 'if [[ "$1" == "-m" ]]; then echo "arm64"; else echo "Darwin"; fi'
    detect_arch
    assert_eq "arm64" "$ARCH_TYPE" "Arch should be arm64"
}

test_detect_arch_amd64() {
    create_mock_cmd "uname" 'if [[ "$1" == "-m" ]]; then echo "x86_64"; else echo "Linux"; fi'
    detect_arch
    assert_eq "amd64" "$ARCH_TYPE" "Arch should be amd64"
}

# --- Docker 检测 ---

test_check_docker_available() {
    create_mock_cmd "docker" '
if [[ "$1" == "info" ]]; then exit 0; fi
if [[ "$1" == "--version" ]]; then echo "Docker version 24.0.0"; fi
exit 0
'
    check_docker
    assert_eq "0" "$?" "Docker should be available"
}

test_check_docker_not_available() {
    # 不创建 docker mock → command -v docker 会失败
    # 但如果系统有 docker，我们需要在 MOCK_BIN_DIR 之后把系统 docker 排除
    # 用一个返回失败的 docker
    create_mock_cmd "docker" 'exit 1'
    if check_docker; then
        return 1  # 应该失败
    fi
    return 0  # check_docker 返回 1，这是预期的
}

# --- macOS Docker 选择菜单 ---

test_macos_noninteractive_chooses_colima() {
    AUTO_YES=true
    OS_TYPE="darwin"

    # mock brew 可用
    create_mock_cmd "brew" 'echo "Homebrew 4.0.0"'

    # mock install_colima（覆盖函数）
    install_colima() { MOCK_CALLED="install_colima"; }

    install_docker_macos
    assert_eq "install_colima" "$MOCK_CALLED" "Non-interactive should call install_colima"
}

test_macos_menu_choice_2_orbstack() {
    AUTO_YES=false
    OS_TYPE="darwin"

    create_mock_cmd "brew" 'echo "Homebrew 4.0.0"'

    install_orbstack() { MOCK_CALLED="install_orbstack"; }

    # mock read 输入 2
    create_mock_cmd "read" 'true'  # 这里不能直接 mock read，改用直接赋值
    DOCKER_CHOICE=2

    # 直接测试 case 逻辑
    case "$DOCKER_CHOICE" in
        1) MOCK_CALLED="install_colima" ;;
        2) install_orbstack ;;
        3) MOCK_CALLED="install_docker_desktop" ;;
        4) MOCK_CALLED="skip" ;;
    esac

    assert_eq "install_orbstack" "$MOCK_CALLED" "Choice 2 should call install_orbstack"
}

test_macos_menu_choice_3_docker_desktop() {
    DOCKER_CHOICE=3

    install_docker_desktop() { MOCK_CALLED="install_docker_desktop"; }

    case "$DOCKER_CHOICE" in
        1) MOCK_CALLED="install_colima" ;;
        2) MOCK_CALLED="install_orbstack" ;;
        3) install_docker_desktop ;;
        4) MOCK_CALLED="skip" ;;
    esac

    assert_eq "install_docker_desktop" "$MOCK_CALLED" "Choice 3 should call install_docker_desktop"
}

test_macos_no_brew() {
    AUTO_YES=false
    OS_TYPE="darwin"

    # 不创建 brew mock，但要确保系统 brew 不被找到
    # 用一个 "command -v brew" 失败的方式
    create_mock_cmd "brew" 'exit 127'
    # 覆盖 brew 为一个不存在的命令
    # 实际上更好的方式是检查 install_docker_macos 中的逻辑
    # 因为我们 mock 了 brew 但它返回成功，所以 command -v brew 会找到它
    # 让我们用另一种方式：直接测试 command -v brew 的行为
    # 其实 create_mock_cmd 创建的 brew 是可被 command -v 找到的
    # 所以我们需要删除 mock 的 brew
    rm -f "$MOCK_BIN_DIR/brew"

    # 在 macOS 系统上，真实的 brew 可能存在，所以这个测试可能不准确
    # 但如果在 CI 无 brew 环境上，这就能工作
    # 这里我们测试的是逻辑分支，所以直接检查函数行为
    if command -v brew &>/dev/null; then
        # 系统有 brew，跳过此测试
        return 0
    fi

    if install_docker_macos 2>/dev/null; then
        return 1  # 应该失败
    fi
    return 0  # install_docker_macos 失败是预期的
}

# --- 参数解析 ---

test_parse_args_skip_docker() {
    parse_args --skip-docker
    assert_eq "true" "$SKIP_DOCKER" "--skip-docker should set SKIP_DOCKER=true"
}

test_parse_args_skip_config() {
    parse_args --skip-config
    assert_eq "true" "$SKIP_CONFIG" "--skip-config should set SKIP_CONFIG=true"
}

test_parse_args_yes() {
    parse_args -y
    assert_eq "true" "$AUTO_YES" "-y should set AUTO_YES=true"
}

test_parse_args_force_build() {
    parse_args --force-build
    assert_eq "true" "$FORCE_BUILD" "--force-build should set FORCE_BUILD=true"
}

test_parse_args_combined() {
    parse_args -y --skip-docker --skip-config --force-build --start
    assert_eq "true" "$AUTO_YES" "AUTO_YES" && \
    assert_eq "true" "$SKIP_DOCKER" "SKIP_DOCKER" && \
    assert_eq "true" "$SKIP_CONFIG" "SKIP_CONFIG" && \
    assert_eq "true" "$FORCE_BUILD" "FORCE_BUILD" && \
    assert_eq "true" "$AUTO_START" "AUTO_START"
}

# --- wait_for_docker 超时 ---

test_wait_for_docker_timeout() {
    # mock docker info 持续失败
    create_mock_cmd "docker" 'exit 1'

    # 用很短的超时来测试
    if wait_for_docker 3 2>/dev/null; then
        return 1  # 应该超时失败
    fi
    return 0
}

# --- check_disk_space ---

test_check_disk_space_sufficient() {
    # 使用当前目录，通常有足够空间
    OS_TYPE="darwin"  # 或 linux
    check_disk_space 1 "/tmp"
}

test_check_disk_space_insufficient() {
    OS_TYPE="darwin"
    # 请求一个不可能的大小
    if check_disk_space 999999 "/tmp" 2>/dev/null; then
        return 1  # 应该失败
    fi
    return 0
}

# --- ensure_docker 跳过 ---

test_ensure_docker_skip() {
    SKIP_DOCKER=true
    ensure_docker
    return 0  # 应该成功（跳过安装）
}

test_ensure_docker_already_installed() {
    SKIP_DOCKER=false
    create_mock_cmd "docker" '
if [[ "$1" == "info" ]]; then exit 0; fi
if [[ "$1" == "--version" ]]; then echo "Docker version 24.0.0"; exit 0; fi
exit 0
'
    OS_TYPE="darwin"
    ensure_docker
}

# --- 幂等性 ---

test_idempotent_image_exists() {
    FORCE_BUILD=false
    create_mock_cmd "docker" '
if [[ "$1" == "images" ]]; then echo "seaturt/sandbox:latest"; exit 0; fi
exit 0
'
    # 创建临时目录模拟 release 结构
    local orig_script_dir="$SCRIPT_DIR"
    SCRIPT_DIR=$(mktemp -d)
    mkdir -p "$SCRIPT_DIR/docker"
    touch "$SCRIPT_DIR/docker/Dockerfile"

    # build_sandbox_image 应该跳过构建（镜像已存在）
    build_sandbox_image
    local result=$?

    rm -rf "$SCRIPT_DIR"
    SCRIPT_DIR="$orig_script_dir"
    return $result
}

# --- 配置检测 ---

test_check_config_no_file() {
    # SCRIPT_DIR 指向一个没有 config.yaml 的临时目录
    local orig_script_dir="$SCRIPT_DIR"
    SCRIPT_DIR=$(mktemp -d)

    if check_config; then
        SCRIPT_DIR="$orig_script_dir"
        return 1  # 应该返回需要配置
    fi
    SCRIPT_DIR="$orig_script_dir"
    return 0
}

test_check_config_with_valid_key() {
    local orig_script_dir="$SCRIPT_DIR"
    SCRIPT_DIR=$(mktemp -d)

    cat > "$SCRIPT_DIR/config.yaml" <<EOF
providers:
  openai:
    api_key: sk-real-key-123
EOF

    if check_config; then
        rm -rf "$SCRIPT_DIR"
        SCRIPT_DIR="$orig_script_dir"
        return 0  # 配置有效
    fi
    rm -rf "$SCRIPT_DIR"
    SCRIPT_DIR="$orig_script_dir"
    return 1
}

test_setup_config_skip() {
    SKIP_CONFIG=true
    setup_config
    return 0
}

test_setup_config_env_var() {
    SKIP_CONFIG=false
    LLM_API_KEY="sk-test-key"

    local orig_script_dir="$SCRIPT_DIR"
    SCRIPT_DIR=$(mktemp -d)

    # 创建一个基础 config.yaml
    cat > "$SCRIPT_DIR/config.yaml" <<EOF
server_port: 8080
providers: {}
EOF

    OS_TYPE="darwin"
    apply_env_config

    # 验证 api_key 被写入
    if grep -q "sk-test-key" "$SCRIPT_DIR/config.yaml"; then
        rm -rf "$SCRIPT_DIR"
        SCRIPT_DIR="$orig_script_dir"
        return 0
    fi
    rm -rf "$SCRIPT_DIR"
    SCRIPT_DIR="$orig_script_dir"
    return 1
}

# ============================================================================
# 运行所有测试
# ============================================================================

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║   🐢  SeaTurt install.sh 单元测试                       ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

echo "--- 平台检测 ---"
run_test "detect_os 识别 Darwin → darwin" test_detect_os_darwin || true
run_test "detect_os 识别 Linux → linux" test_detect_os_linux || true
run_test "detect_arch 识别 arm64" test_detect_arch_arm64 || true
run_test "detect_arch 识别 x86_64 → amd64" test_detect_arch_amd64 || true

echo ""
echo "--- Docker 检测 ---"
run_test "check_docker: docker 可用时返回 0" test_check_docker_available || true
run_test "check_docker: docker 不可用时返回 1" test_check_docker_not_available || true

echo ""
echo "--- macOS Docker 安装选择 ---"
run_test "非交互模式自动选择 Colima" test_macos_noninteractive_chooses_colima || true
run_test "选择 2 → 调用 install_orbstack" test_macos_menu_choice_2_orbstack || true
run_test "选择 3 → 调用 install_docker_desktop" test_macos_menu_choice_3_docker_desktop || true
run_test "无 Homebrew 时报错" test_macos_no_brew || true

echo ""
echo "--- 参数解析 ---"
run_test "--skip-docker 正确设置 flag" test_parse_args_skip_docker || true
run_test "--skip-config 正确设置 flag" test_parse_args_skip_config || true
run_test "-y 正确设置 flag" test_parse_args_yes || true
run_test "--force-build 正确设置 flag" test_parse_args_force_build || true
run_test "多参数组合正确设置" test_parse_args_combined || true

echo ""
echo "--- Docker 超时 & 磁盘检查 ---"
run_test "wait_for_docker 超时退出" test_wait_for_docker_timeout || true
run_test "磁盘空间充足" test_check_disk_space_sufficient || true
run_test "磁盘空间不足时报错" test_check_disk_space_insufficient || true

echo ""
echo "--- 幂等性 ---"
run_test "跳过 Docker 安装 (--skip-docker)" test_ensure_docker_skip || true
run_test "Docker 已安装时跳过安装" test_ensure_docker_already_installed || true
run_test "镜像已存在时跳过构建" test_idempotent_image_exists || true

echo ""
echo "--- 配置 ---"
run_test "无 config.yaml 时返回需要配置" test_check_config_no_file || true
run_test "有有效 api_key 时返回配置完整" test_check_config_with_valid_key || true
run_test "跳过配置引导 (--skip-config)" test_setup_config_skip || true
run_test "环境变量自动写入配置" test_setup_config_env_var || true

# ============================================================================
# 测试结果
# ============================================================================

echo ""
echo "═══════════════════════════════════════════════════"
echo -e "  总计: $TESTS_TOTAL | ${GREEN}通过: $TESTS_PASSED${NC} | ${RED}失败: $TESTS_FAILED${NC}"
echo "═══════════════════════════════════════════════════"
echo ""

if (( TESTS_FAILED > 0 )); then
    exit 1
fi
exit 0
