"""
wechat_launcher.py — WeChat 启动器（纯 Python 实现）

职责:
  1. 等待桌面环境就绪（DISPLAY=:1 可用）
  2. 发现/创建 D-Bus session bus，地址持久化到 /tmp/wechat-dbus-addr
  3. 确保 AT-SPI2 registryd 正在运行
  4. 设置 QT_ACCESSIBILITY=1 等环境变量
  5. 启动微信客户端（前台执行或后台执行）

用途:
  - s6 longrun 服务: exec python3 wechat_launcher.py --daemon
  - main.py 中按需调用: from wechat_launcher import ensure_environment, launch_wechat

替代了之前的 svc-wechat-run (bash) 和 start_wechat.sh。
"""

import logging
import os
import signal
import subprocess
import shutil
import sys
import time
from pathlib import Path

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="[wechat-launcher] %(asctime)s %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
logger = logging.getLogger("wechat-launcher")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

DBUS_ADDR_FILE = "/tmp/wechat-dbus-addr"
DESKTOP_PID_FILE = "/defaults/pid"  # linuxserver/webtop 桌面就绪标志
DEFAULT_DISPLAY = ":1"

WECHAT_BIN_PATHS = [
    "/usr/bin/wechat",
    "/opt/wechat/wechat",
    "/usr/local/bin/wechat",
]

SESSION_DIR = os.environ.get("WECHAT_SESSION_DIR", "/workspace/.seaturt/mcp-servers/wechat/session")

# AT-SPI2 bus launcher 可能的路径
ATSPI_LAUNCHER_PATHS = [
    "/usr/libexec/at-spi-bus-launcher",
    "/usr/lib/at-spi2-core/at-spi-bus-launcher",
    "/usr/lib/x86_64-linux-gnu/at-spi2-core/at-spi-bus-launcher",
]


# ---------------------------------------------------------------------------
# D-Bus session bus 发现/创建
# ---------------------------------------------------------------------------


def _read_dbus_addr_from_pid(pid: str) -> str | None:
    """从指定 PID 的 /proc/<pid>/environ 中提取 DBUS_SESSION_BUS_ADDRESS"""
    environ_path = f"/proc/{pid}/environ"
    if not os.path.isfile(environ_path):
        return None
    try:
        with open(environ_path, "rb") as f:
            environ_data = f.read()
        for entry in environ_data.split(b"\x00"):
            decoded = entry.decode("utf-8", errors="replace")
            if decoded.startswith("DBUS_SESSION_BUS_ADDRESS="):
                addr = decoded[len("DBUS_SESSION_BUS_ADDRESS="):]
                if addr:
                    return addr
    except (PermissionError, OSError):
        pass
    return None


# 桌面进程候选列表 —— 这些进程的 /proc/<pid>/environ 中通常包含
# 桌面 D-Bus 会话地址。按优先级排列。
_DESKTOP_PROCESS_NAMES = [
    "openbox",              # linuxserver/webtop 默认 WM
    "startplasma-x11",      # KDE Plasma X11
    "plasmashell",          # KDE Plasma shell
    "kwin_x11",             # KDE 窗口管理器
    "xfce4-session",        # XFCE
    "xfwm4",                # XFCE WM
    "mate-session",         # MATE
    "gnome-session-binary", # GNOME
    "gnome-shell",          # GNOME Shell
    "dbus-daemon",          # fallback: dbus-daemon 自身
]


def _discover_dbus_address() -> str | None:
    """
    从桌面环境相关进程的 /proc/<pid>/environ 中发现
    DBUS_SESSION_BUS_ADDRESS。

    ⚠️ 关键发现：在 linuxserver/webtop 容器中，正确的 D-Bus 地址
    来自桌面进程（如 openbox/KDE/XFCE），而非 dbus-daemon 自身。
    dbus-daemon 的 /proc/<pid>/environ 中通常不包含自己的地址。
    因此需要按优先级扫描多个桌面候选进程。

    Returns:
        D-Bus 地址字符串，未找到返回 None。
    """
    # 策略 1: 按候选进程名查找
    for proc_name in _DESKTOP_PROCESS_NAMES:
        try:
            result = subprocess.run(
                ["pgrep", "-o", "-x", proc_name],
                capture_output=True, text=True, timeout=5,
            )
            if result.returncode != 0 or not result.stdout.strip():
                continue

            pid = result.stdout.strip().split("\n")[0]
            addr = _read_dbus_addr_from_pid(pid)
            if addr:
                logger.info(f"Discovered D-Bus address from {proc_name} (PID {pid}): {addr}")
                return addr
        except Exception:
            continue

    # 策略 2: 暴力扫描 /proc/*/environ（如果候选进程都未命中）
    # 只扫描 PID > 1 的用户态进程，找第一个含 DBUS_SESSION_BUS_ADDRESS 的
    try:
        proc_dir = Path("/proc")
        pids = sorted(
            (int(p.name) for p in proc_dir.iterdir()
             if p.name.isdigit() and int(p.name) > 1),
        )
        for pid in pids:
            addr = _read_dbus_addr_from_pid(str(pid))
            if addr:
                # 读取进程名用于日志
                try:
                    comm = Path(f"/proc/{pid}/comm").read_text().strip()
                except Exception:
                    comm = "?"
                logger.info(f"Discovered D-Bus address from PID {pid} ({comm}): {addr}")
                return addr
    except Exception as e:
        logger.debug(f"Brute-force D-Bus scan failed: {e}")

    return None


def _start_dbus_session() -> str | None:
    """
    启动一个新的 D-Bus session bus (dbus-launch)。

    Returns:
        D-Bus 地址字符串，失败返回 None。
    """
    try:
        result = subprocess.run(
            ["dbus-launch", "--sh-syntax"],
            capture_output=True, text=True, timeout=10,
        )
        if result.returncode != 0:
            logger.error(f"dbus-launch failed: {result.stderr}")
            return None

        # 解析 dbus-launch 输出，格式如:
        #   DBUS_SESSION_BUS_ADDRESS='unix:abstract=...,guid=...';
        #   DBUS_SESSION_BUS_PID=12345;
        for line in result.stdout.splitlines():
            line = line.strip().rstrip(";")
            if line.startswith("DBUS_SESSION_BUS_ADDRESS="):
                addr = line[len("DBUS_SESSION_BUS_ADDRESS="):]
                # 去掉引号
                addr = addr.strip("'\"")
                if addr:
                    logger.info(f"Started new D-Bus session: {addr}")
                    return addr
    except FileNotFoundError:
        logger.error("dbus-launch not found (install dbus-x11)")
    except Exception as e:
        logger.error(f"dbus-launch error: {e}")

    return None


def ensure_dbus() -> str | None:
    """
    确保 D-Bus session bus 可用。

    优先级:
      1. 当前环境变量
      2. /tmp/wechat-dbus-addr 文件
      3. 从已运行的 dbus-daemon 进程发现
      4. 启动新的 dbus session

    成功后会:
      - 设置 os.environ["DBUS_SESSION_BUS_ADDRESS"]
      - 写入 DBUS_ADDR_FILE 供其他进程读取

    Returns:
        D-Bus 地址，或 None（失败）。
    """
    # 1. 环境变量中已有
    addr = os.environ.get("DBUS_SESSION_BUS_ADDRESS", "").strip()
    if addr:
        _persist_dbus_addr(addr)
        return addr

    # 2. 文件中已有
    if os.path.isfile(DBUS_ADDR_FILE):
        try:
            with open(DBUS_ADDR_FILE) as f:
                addr = f.read().strip()
            if addr:
                os.environ["DBUS_SESSION_BUS_ADDRESS"] = addr
                logger.info(f"Loaded D-Bus address from file: {addr}")
                return addr
        except Exception:
            pass

    # 3. 从 dbus-daemon 进程发现
    addr = _discover_dbus_address()
    if addr:
        os.environ["DBUS_SESSION_BUS_ADDRESS"] = addr
        _persist_dbus_addr(addr)
        return addr

    # 4. 启动新 session
    addr = _start_dbus_session()
    if addr:
        os.environ["DBUS_SESSION_BUS_ADDRESS"] = addr
        _persist_dbus_addr(addr)
        return addr

    logger.error("Failed to obtain D-Bus session bus address")
    return None


def _persist_dbus_addr(addr: str) -> None:
    """将 D-Bus 地址写入文件供其他进程（如 docker exec → main.py）读取。"""
    try:
        with open(DBUS_ADDR_FILE, "w") as f:
            f.write(addr)
        logger.debug(f"D-Bus address persisted to {DBUS_ADDR_FILE}")
    except Exception as e:
        logger.warning(f"Failed to persist D-Bus address: {e}")


# ---------------------------------------------------------------------------
# AT-SPI2 确保
# ---------------------------------------------------------------------------


def _find_atspi_launcher() -> str | None:
    """查找 at-spi-bus-launcher 可执行文件路径。"""
    for p in ATSPI_LAUNCHER_PATHS:
        if os.path.isfile(p) and os.access(p, os.X_OK):
            return p
    # 也在 PATH 中查找
    found = shutil.which("at-spi-bus-launcher")
    return found


def _is_process_running(name: str) -> bool:
    """检查指定进程名是否正在运行。"""
    try:
        result = subprocess.run(
            ["pgrep", "-x", name],
            capture_output=True, timeout=5,
        )
        return result.returncode == 0
    except Exception:
        return False


def _is_atspi_running() -> bool:
    """
    检测 AT-SPI2 registryd 是否正在运行。

    ⚠️ 在 linuxserver/webtop 容器中，AT-SPI2 registryd 可能由桌面会话
    （而非当前进程树）启动，进程名可能是 'at-spi2-registryd' 或
    'at-spi2-registr'（被截断）。需要多种方式检测。
    """
    # 方式 1: pgrep 精确匹配
    if _is_process_running("at-spi2-registryd"):
        return True

    # 方式 2: pgrep 模糊匹配（进程名可能被截断到 15 字符）
    try:
        result = subprocess.run(
            ["pgrep", "-f", "at-spi2-registr"],
            capture_output=True, timeout=5,
        )
        if result.returncode == 0 and result.stdout.strip():
            return True
    except Exception:
        pass

    # 方式 3: 扫描 /proc/*/comm
    try:
        proc_dir = Path("/proc")
        for p in proc_dir.iterdir():
            if not p.name.isdigit():
                continue
            try:
                comm = (p / "comm").read_text().strip()
                if "at-spi2-registr" in comm:
                    return True
            except (PermissionError, OSError):
                continue
    except Exception:
        pass

    return False


def _verify_atspi_bus_reachable() -> bool:
    """
    通过 D-Bus 验证 AT-SPI2 bus 是否可达（不仅仅检查进程存在）。

    核心逻辑:
      1. 向 org.a11y.Bus 接口发送 GetAddress 请求获取 AT-SPI2 bus 地址
      2. 如果能成功获取到地址，说明当前 D-Bus session 对应的 AT-SPI2 bus 可用

    这解决了容器中存在多个 AT-SPI2 registryd 实例、但当前 D-Bus session
    对应的那个不可达的问题。

    Returns:
        True if AT-SPI2 bus is reachable via current D-Bus session.
    """
    dbus_addr = os.environ.get("DBUS_SESSION_BUS_ADDRESS", "")
    if not dbus_addr:
        logger.warning("_verify_atspi_bus_reachable: DBUS_SESSION_BUS_ADDRESS not set")
        return False

    try:
        # 通过 dbus-send 查询 org.a11y.Bus.GetAddress
        # 这是 AT-SPI2 bus-launcher 注册在 session bus 上的标准接口
        result = subprocess.run(
            [
                "dbus-send", "--session", "--print-reply",
                "--dest=org.a11y.Bus",
                "/org/a11y/bus",
                "org.a11y.Bus.GetAddress",
            ],
            capture_output=True,
            text=True,
            timeout=5,
            env={**os.environ, "DBUS_SESSION_BUS_ADDRESS": dbus_addr},
        )
        if result.returncode == 0 and "string" in result.stdout:
            # 解析 AT-SPI2 bus 地址
            for line in result.stdout.splitlines():
                line = line.strip()
                if line.startswith("string"):
                    atspi_addr = line.split('"')[1] if '"' in line else ""
                    if atspi_addr:
                        logger.info(f"AT-SPI2 bus reachable: {atspi_addr}")
                        return True
        logger.warning(f"AT-SPI2 bus not reachable: dbus-send rc={result.returncode}, "
                       f"stdout={result.stdout[:200]}")
        return False
    except FileNotFoundError:
        logger.warning("dbus-send not found, cannot verify AT-SPI2 bus")
        return False
    except subprocess.TimeoutExpired:
        logger.warning("dbus-send timeout while querying AT-SPI2 bus")
        return False
    except Exception as e:
        logger.warning(f"AT-SPI2 bus verification failed: {e}")
        return False


def _kill_all_atspi() -> None:
    """
    杀掉所有现有的 AT-SPI2 相关进程（bus-launcher 和 registryd）。

    用于清理不一致的 AT-SPI2 实例，确保后续重新启动的实例
    绑定到当前 D-Bus session。
    """
    for proc_name in ["at-spi-bus-launcher", "at-spi2-registryd", "at-spi2-registr"]:
        try:
            subprocess.run(
                ["pkill", "-f", proc_name],
                capture_output=True, timeout=5,
            )
        except Exception:
            pass
    # 给进程一点时间退出
    time.sleep(0.5)
    logger.info("Killed all existing AT-SPI2 processes")


def ensure_atspi() -> bool:
    """
    确保 AT-SPI2 registryd 正在运行，**且绑定到当前 D-Bus session**。

    增强逻辑（修复多 registryd 实例导致微信注册到错误 bus 的问题）:
      1. 检查 registryd 进程是否存在
      2. 如果存在，通过 D-Bus 验证 AT-SPI2 bus 是否可达
      3. 如果进程存在但 bus 不可达 → 杀掉所有 AT-SPI2 进程，重新启动
      4. 重新启动后再次验证

    Returns:
        True if AT-SPI2 registryd is running and reachable, False if failed.
    """
    if _is_atspi_running():
        # 关键: 不仅检查进程，还要验证 bus 可达性
        if _verify_atspi_bus_reachable():
            logger.info("AT-SPI2 registryd already running and bus reachable")
            return True
        else:
            # 进程在跑但 bus 不可达 — 说明 registryd 绑定到了另一个 D-Bus session
            logger.warning(
                "AT-SPI2 registryd is running but bus NOT reachable "
                "from current D-Bus session. Killing stale instances and restarting..."
            )
            _kill_all_atspi()

    launcher = _find_atspi_launcher()
    if not launcher:
        logger.error("at-spi-bus-launcher not found (install at-spi2-core)")
        return False

    try:
        logger.info(f"Starting AT-SPI2 bus launcher: {launcher}")
        # 关键: 将当前 D-Bus session 地址传递给 bus-launcher，
        # 确保它注册到当前 session 而非创建一个新的
        env = os.environ.copy()
        subprocess.Popen(
            [launcher],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            env=env,
            # 不使用 start_new_session=True，保持与当前进程相同的 session
        )
        # 等待 registryd 启动 + bus 可达
        for attempt in range(15):
            time.sleep(0.5)
            if _is_process_running("at-spi2-registryd"):
                if _verify_atspi_bus_reachable():
                    logger.info("AT-SPI2 registryd started and bus verified reachable")
                    return True
                logger.debug(f"AT-SPI2 registryd running but bus not yet reachable (attempt {attempt+1})")

        logger.warning("AT-SPI2 registryd did not become reachable within timeout")
        return False
    except Exception as e:
        logger.error(f"Failed to start AT-SPI2: {e}")
        return False


def wait_for_atspi_registration(timeout: float = 30.0) -> bool:
    """
    等待微信在 AT-SPI2 desktop 中注册。

    启动微信后，Qt Accessibility Bridge 需要时间完成以下步骤:
      1. Qt 初始化
      2. Accessibility Bridge 连接 D-Bus
      3. 向 AT-SPI2 registryd 注册应用
      4. desktop.get_child_count() > 0

    Args:
        timeout: 最大等待秒数

    Returns:
        True if WeChat appeared in AT-SPI2 desktop, False if timeout.
    """
    try:
        import gi
        gi.require_version("Atspi", "2.0")
        from gi.repository import Atspi
        Atspi.init()
    except Exception as e:
        logger.warning(f"Cannot import Atspi for registration wait: {e}")
        return False

    start = time.time()
    while time.time() - start < timeout:
        try:
            desktop = Atspi.get_desktop(0)
            child_count = desktop.get_child_count()
            if child_count > 0:
                # 进一步检查是否有 wechat 应用
                for i in range(child_count):
                    try:
                        app = desktop.get_child_at_index(i)
                        if app and "wechat" in (app.get_name() or "").lower():
                            elapsed = time.time() - start
                            logger.info(
                                f"WeChat registered in AT-SPI2 desktop after {elapsed:.1f}s "
                                f"(child_count={child_count})"
                            )
                            return True
                    except Exception:
                        pass
                logger.debug(f"AT-SPI2 desktop has {child_count} children but no wechat yet")
            else:
                logger.debug("AT-SPI2 desktop child_count=0, waiting...")
        except Exception as e:
            logger.debug(f"AT-SPI2 desktop query error: {e}")

        time.sleep(1.0)

    logger.warning(f"WeChat did not register in AT-SPI2 desktop within {timeout}s")
    return False


# ---------------------------------------------------------------------------
# DISPLAY 确保
# ---------------------------------------------------------------------------


def ensure_display() -> None:
    """确保 DISPLAY 环境变量已设置。"""
    if not os.environ.get("DISPLAY"):
        os.environ["DISPLAY"] = DEFAULT_DISPLAY
        logger.info(f"DISPLAY set to {DEFAULT_DISPLAY}")


# ---------------------------------------------------------------------------
# 微信客户端查找与启动
# ---------------------------------------------------------------------------


def find_wechat_bin() -> str | None:
    """
    查找微信可执行文件。

    Returns:
        微信二进制路径，未找到返回 None。
    """
    for p in WECHAT_BIN_PATHS:
        if os.path.isfile(p) and os.access(p, os.X_OK):
            return p
    found = shutil.which("wechat")
    return found


def launch_wechat(foreground: bool = False) -> subprocess.Popen | None:
    """
    启动微信客户端。

    调用前请先确保 ensure_environment() 已执行。

    Args:
        foreground: True = exec（替换当前进程），False = Popen 后台启动

    Returns:
        Popen 对象（background 模式），或 None（foreground 模式不返回 / 失败）。
    """
    wechat_bin = find_wechat_bin()
    if not wechat_bin:
        logger.error("WeChat binary not found")
        return None

    # 确保 session 目录存在
    os.makedirs(SESSION_DIR, exist_ok=True)

    # 设置 Qt 无障碍环境变量
    env = os.environ.copy()
    env["QT_ACCESSIBILITY"] = "1"
    env["QT_LINUX_ACCESSIBILITY_ALWAYS_ON"] = "1"

    logger.info(f"Starting WeChat: {wechat_bin}")
    logger.info(f"  DISPLAY={env.get('DISPLAY', 'N/A')}")
    logger.info(f"  DBUS_SESSION_BUS_ADDRESS={env.get('DBUS_SESSION_BUS_ADDRESS', 'N/A')}")
    logger.info(f"  QT_ACCESSIBILITY={env.get('QT_ACCESSIBILITY')}")

    if foreground:
        # exec — 替换当前进程（用于 s6 daemon 模式）
        os.execve(wechat_bin, [wechat_bin], env)
        # 不会执行到这里
        return None
    else:
        # 后台启动
        # ⚠️ 不使用 start_new_session=True:
        # 微信必须与当前进程在同一 session 中，确保共享相同的
        # D-Bus session 和 AT-SPI2 bus。使用 start_new_session=True
        # 会导致微信注册到不同的 AT-SPI2 registryd 实例，
        # 使得 main.py 的 Atspi.get_desktop(0) 查不到微信。
        try:
            proc = subprocess.Popen(
                [wechat_bin],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                env=env,
            )
            logger.info(f"WeChat launched (PID={proc.pid})")
            return proc
        except Exception as e:
            logger.error(f"Failed to launch WeChat: {e}")
            return None


def is_wechat_running() -> bool:
    """检查微信进程是否正在运行。"""
    return _is_process_running("wechat")


# ---------------------------------------------------------------------------
# 综合 API
# ---------------------------------------------------------------------------


def ensure_environment() -> bool:
    """
    确保 WeChat 运行所需的全部环境就绪:
      1. DISPLAY
      2. D-Bus session bus
      3. AT-SPI2

    Returns:
        True if all ready, False if critical failure.
    """
    ensure_display()

    addr = ensure_dbus()
    if not addr:
        return False

    ensure_atspi()  # AT-SPI2 失败不是致命的，微信照样能跑，只是 MCP 没法操控

    return True


def wait_for_desktop(timeout: float = 120) -> bool:
    """
    等待桌面环境就绪（linuxserver/webtop 的 /defaults/pid 文件出现）。

    Args:
        timeout: 最大等待秒数

    Returns:
        True if ready, False if timeout.
    """
    start = time.time()
    while not os.path.isfile(DESKTOP_PID_FILE):
        if time.time() - start > timeout:
            logger.error(f"Desktop not ready after {timeout}s (no {DESKTOP_PID_FILE})")
            return False
        time.sleep(0.5)

    logger.info("Desktop environment is ready")
    return True


# ---------------------------------------------------------------------------
# Daemon 模式 (s6 longrun 入口)
# ---------------------------------------------------------------------------


def daemon_main() -> None:
    """
    s6 longrun 服务入口。

    流程:
      1. 等待桌面就绪
      2. ensure_environment()
      3. 循环检测微信是否已安装
      4. exec 微信（前台运行，由 s6 管理生命周期）
    """
    logger.info("WeChat daemon starting...")

    # 1. 等待桌面
    if not wait_for_desktop():
        logger.error("Desktop not ready, exiting")
        sys.exit(1)

    # 2. 环境准备
    if not ensure_environment():
        logger.error("Environment setup failed, exiting")
        sys.exit(1)

    # 3. 等待微信安装
    wechat_bin = find_wechat_bin()
    if not wechat_bin:
        logger.info("WeChat not installed. Waiting for installation...")
        logger.info("Install with: apt-get install -y wechat")
        logger.info("Or download from: https://linux.weixin.qq.com/")

        while True:
            time.sleep(30)
            wechat_bin = find_wechat_bin()
            if wechat_bin:
                logger.info(f"WeChat detected at: {wechat_bin}")
                break

    # 4. 创建 session 目录
    os.makedirs(SESSION_DIR, exist_ok=True)

    # 5. 启动微信（前台，exec 替换当前进程）
    logger.info(f"Launching WeChat (foreground): {wechat_bin}")
    launch_wechat(foreground=True)

    # 如果 exec 失败（不应该走到这里）
    logger.error("exec failed, exiting")
    sys.exit(1)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


if __name__ == "__main__":
    if "--daemon" in sys.argv:
        daemon_main()
    elif "--launch" in sys.argv:
        # 按需启动模式（由 main.py 调用）
        if not ensure_environment():
            logger.error("Environment setup failed")
            sys.exit(1)
        proc = launch_wechat(foreground=False)
        if proc:
            logger.info(f"WeChat launched in background (PID={proc.pid})")
            sys.exit(0)
        else:
            sys.exit(1)
    elif "--check" in sys.argv:
        # 仅检查环境
        ensure_display()
        addr = ensure_dbus()
        atspi = ensure_atspi()
        wechat = find_wechat_bin()
        running = is_wechat_running()
        print(f"DISPLAY={os.environ.get('DISPLAY', 'N/A')}")
        print(f"DBUS_SESSION_BUS_ADDRESS={addr or 'N/A'}")
        print(f"AT-SPI2 running: {atspi}")
        print(f"WeChat binary: {wechat or 'NOT FOUND'}")
        print(f"WeChat running: {running}")
    else:
        print("Usage: wechat_launcher.py [--daemon|--launch|--check]")
        print()
        print("  --daemon  s6 longrun mode: wait for desktop, setup env, exec wechat")
        print("  --launch  Start WeChat in background and exit")
        print("  --check   Check environment status and exit")
        sys.exit(0)
