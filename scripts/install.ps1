#Requires -Version 5.1
<#
.SYNOPSIS
    SeaTurt Windows 安装脚本 — WSL2 引桥
.DESCRIPTION
    此脚本在 Windows 上运行，自动完成：
    1. 检测/安装 WSL2
    2. 检测/安装 Ubuntu 发行版
    3. 将 release 包复制到 WSL2 内部
    4. 在 WSL2 内执行 install.sh 完成实际安装
.NOTES
    需要以管理员身份运行（安装 WSL2 需要管理员权限）
    首次安装 WSL2 后需要重启系统
.EXAMPLE
    # 右键 → 使用 PowerShell 运行
    # 或在管理员 PowerShell 中：
    powershell -ExecutionPolicy Bypass -File install.ps1
.EXAMPLE
    # 非交互模式
    powershell -ExecutionPolicy Bypass -File install.ps1 -Yes
#>

param(
    [switch]$SkipConfig,
    [switch]$SkipDocker,
    [switch]$ForceBuild,
    [switch]$Start,
    [switch]$Yes,
    [string]$LlmApiKey = $env:LLM_API_KEY,
    [string]$LlmBaseUrl = $env:LLM_BASE_URL,
    [switch]$Help
)

$ErrorActionPreference = "Stop"
$ScriptVersion = "0.3.2"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# ============================================================================
# 帮助
# ============================================================================

if ($Help) {
    Write-Host @"
SeaTurt Windows Installer v$ScriptVersion

此脚本自动将 SeaTurt 安装到 WSL2 (Ubuntu) 环境中。

Usage:
  powershell -ExecutionPolicy Bypass -File install.ps1 [OPTIONS]

Options:
  -SkipConfig     跳过配置引导
  -SkipDocker     跳过 Docker 安装
  -ForceBuild     强制重新构建沙箱镜像
  -Start          安装完成后自动启动
  -Yes            非交互模式（自动确认所有提示）
  -LlmApiKey      LLM API Key
  -LlmBaseUrl     LLM Base URL
  -Help           显示帮助信息

Examples:
  .\install.ps1                           # 交互式安装
  .\install.ps1 -Yes                      # 非交互式安装
  .\install.ps1 -Yes -LlmApiKey "sk-xxx"  # 全自动安装
"@
    exit 0
}

# ============================================================================
# 工具函数
# ============================================================================

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host "[OK] $Message" -ForegroundColor Green
}

function Write-Warn {
    param([string]$Message)
    Write-Host "[!!] $Message" -ForegroundColor Yellow
}

function Write-Err {
    param([string]$Message)
    Write-Host "[XX] $Message" -ForegroundColor Red
}

# ============================================================================
# 管理员权限检测
# ============================================================================

Write-Host ""
Write-Host "================================================================" -ForegroundColor Cyan
Write-Host "   SeaTurt Windows Installer v$ScriptVersion" -ForegroundColor Cyan
Write-Host "================================================================" -ForegroundColor Cyan
Write-Host ""

Write-Step "管理员权限检测"

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)

if (-not $isAdmin) {
    Write-Err "此脚本需要管理员权限（安装 WSL2 需要）"
    Write-Host ""
    Write-Host "请以管理员身份运行 PowerShell，然后执行：" -ForegroundColor Yellow
    Write-Host "  powershell -ExecutionPolicy Bypass -File `"$($MyInvocation.MyCommand.Path)`"" -ForegroundColor White
    Write-Host ""
    Write-Host "或者右键点击 install.ps1 → '使用 PowerShell 运行'" -ForegroundColor Yellow
    exit 1
}

Write-Ok "管理员权限已确认"

# ============================================================================
# WSL2 检测 & 安装
# ============================================================================

Write-Step "WSL2 检测"

$wslInstalled = $false
try {
    $wslOutput = & wsl --status 2>&1
    if ($LASTEXITCODE -eq 0) {
        $wslInstalled = $true
    }
} catch {
    # wsl 命令不存在
}

if (-not $wslInstalled) {
    Write-Warn "WSL2 未安装，正在安装..."
    Write-Host "  执行: wsl --install --no-distribution" -ForegroundColor DarkGray

    try {
        & wsl --install --no-distribution
    } catch {
        Write-Err "WSL2 安装失败: $_"
        exit 1
    }

    Write-Host ""
    Write-Host "================================================================" -ForegroundColor Red
    Write-Host "  WSL2 安装完成，需要重启系统！" -ForegroundColor Red
    Write-Host "" -ForegroundColor Red
    Write-Host "  请重启后重新运行此脚本：" -ForegroundColor Red
    Write-Host "  powershell -ExecutionPolicy Bypass -File install.ps1" -ForegroundColor White
    Write-Host "================================================================" -ForegroundColor Red
    Write-Host ""

    if (-not $Yes) {
        Read-Host "按 Enter 键确认..."
    }
    exit 1
}

Write-Ok "WSL2 已安装"

# ============================================================================
# Ubuntu 发行版检测 & 安装
# ============================================================================

Write-Step "Ubuntu 发行版检测"

$distros = & wsl --list --quiet 2>&1 | Where-Object { $_ -match "Ubuntu" }

if (-not $distros) {
    Write-Warn "未检测到 Ubuntu 发行版，正在安装..."
    Write-Host "  执行: wsl --install -d Ubuntu" -ForegroundColor DarkGray

    & wsl --install -d Ubuntu

    Write-Host "  等待 Ubuntu 初始化完成..." -ForegroundColor DarkGray
    Start-Sleep -Seconds 15

    # 再次检测
    $distros = & wsl --list --quiet 2>&1 | Where-Object { $_ -match "Ubuntu" }
    if (-not $distros) {
        Write-Err "Ubuntu 安装失败，请手动运行: wsl --install -d Ubuntu"
        exit 1
    }
}

Write-Ok "Ubuntu 发行版已就绪"

# ============================================================================
# 传入 Release 包
# ============================================================================

Write-Step "复制文件到 WSL2"

# 获取 WSL2 内用户名
$wslUser = (& wsl -d Ubuntu -- whoami 2>&1).Trim()
if (-not $wslUser -or $wslUser -eq "root") {
    # 如果返回 root，尝试获取第一个普通用户
    $wslUser = (& wsl -d Ubuntu -- bash -c "getent passwd 1000 | cut -d: -f1" 2>&1).Trim()
    if (-not $wslUser) {
        $wslUser = "root"
    }
}

$wslTarget = "/home/$wslUser/seaturt"
if ($wslUser -eq "root") {
    $wslTarget = "/root/seaturt"
}

Write-Host "  WSL2 用户: $wslUser" -ForegroundColor DarkGray
Write-Host "  目标路径: $wslTarget" -ForegroundColor DarkGray

# 清理旧目录
& wsl -d Ubuntu -- rm -rf $wslTarget 2>$null
& wsl -d Ubuntu -- mkdir -p $wslTarget

# 通过 wslpath 转换 Windows 路径 → WSL 路径
$wslWindowsPath = (& wsl -d Ubuntu -- wslpath "$ScriptDir" 2>&1).Trim()
Write-Host "  Windows 路径: $ScriptDir" -ForegroundColor DarkGray
Write-Host "  WSL 映射路径: $wslWindowsPath" -ForegroundColor DarkGray

# 复制文件
& wsl -d Ubuntu -- cp -r "$wslWindowsPath/." "$wslTarget/"

# 确保脚本有执行权限
& wsl -d Ubuntu -- chmod +x "$wslTarget/install.sh"
& wsl -d Ubuntu -- chmod +x "$wslTarget/uninstall.sh" 2>$null
& wsl -d Ubuntu -- chmod +x "$wslTarget/seaturt" 2>$null

Write-Ok "文件已复制到 WSL2: $wslTarget"

# ============================================================================
# 在 WSL2 内执行 install.sh
# ============================================================================

Write-Step "在 WSL2 内执行安装"

# 构建参数
$installArgs = @()
if ($Yes)         { $installArgs += "-y" }
if ($SkipConfig)  { $installArgs += "--skip-config" }
if ($SkipDocker)  { $installArgs += "--skip-docker" }
if ($ForceBuild)  { $installArgs += "--force-build" }
if ($Start)       { $installArgs += "--start" }

# 构建环境变量前缀
$envPrefix = ""
if ($LlmApiKey)  { $envPrefix += "LLM_API_KEY='$LlmApiKey' " }
if ($LlmBaseUrl) { $envPrefix += "LLM_BASE_URL='$LlmBaseUrl' " }

$argsStr = $installArgs -join ' '
$bashCmd = "${envPrefix}bash $wslTarget/install.sh $argsStr"

Write-Host "  执行: wsl -d Ubuntu -- bash -c `"$bashCmd`"" -ForegroundColor DarkGray
Write-Host ""

& wsl -d Ubuntu -- bash -c $bashCmd

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Err "安装失败！请查看上方错误信息。"
    exit 1
}

# ============================================================================
# 完成提示
# ============================================================================

Write-Host ""
Write-Host "================================================================" -ForegroundColor Green
Write-Host "  SeaTurt 安装完成！" -ForegroundColor Green
Write-Host "" -ForegroundColor Green
Write-Host "  启动命令:" -ForegroundColor Cyan
Write-Host "    wsl -d Ubuntu -- $wslTarget/seaturt" -ForegroundColor White
Write-Host "" -ForegroundColor Green
Write-Host "  访问地址:" -ForegroundColor Cyan
Write-Host "    http://localhost:8080" -ForegroundColor White
Write-Host "" -ForegroundColor Green
Write-Host "  配置文件:" -ForegroundColor Cyan
Write-Host "    wsl -d Ubuntu -- nano $wslTarget/config.yaml" -ForegroundColor White
Write-Host "" -ForegroundColor Green
Write-Host "  提示: 后续操作（启动/停止/配置）均在 WSL2 内执行" -ForegroundColor DarkGray
Write-Host "================================================================" -ForegroundColor Green
Write-Host ""
