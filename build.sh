#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
SERVER_DIR="$PROJECT_ROOT/seaturt-server"
WEB_DIR="$PROJECT_ROOT/seaturt-web"
MCP_SERVERS_DIR="$SERVER_DIR/docker/sandbox/mcp-servers"
EMBED_DIR="$SERVER_DIR/cmd/server/web/dist"

# ---- Detect host platform ----
detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)       echo "linux" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)             echo "amd64" ;;
    esac
}

# Defaults: current host platform
BUILD_OS="${OS:-$(detect_os)}"
BUILD_ARCH="${ARCH:-$(detect_arch)}"

usage() {
    cat <<EOF
Usage: $0 [TARGET] [OPTIONS]

Targets:
  web       Build frontend only
  server    Build backend only (supports cross-compilation)
  mcp       Build MCP servers only (always linux, for Docker sandbox)
  image     Build Docker sandbox image only
  release   Full build: web + mcp + server + image (default)

Options:
  --os      Target OS: linux, darwin, windows (default: current host → $BUILD_OS)
  --arch    Target arch: amd64, arm64 (default: current host → $BUILD_ARCH)

Output layout:
  seaturt-server/release/<os>_<arch>/seaturt[.exe]
  seaturt-server/release/<os>_<arch>/mcp-bins/

Notes:
  - Each platform dir is self-contained: seaturt + mcp-bins/ side by side
  - MCP servers compile for linux (they run inside Docker sandbox)
  - Docker image is always linux/<arch>

Examples:
  $0                              # Build for current host (${BUILD_OS}/${BUILD_ARCH})
  $0 server                      # Build server for current host
  $0 server --os linux --arch amd64   # Cross-compile server for linux/amd64
  $0 mcp --arch arm64            # Build MCP bins for linux/arm64
  $0 release --os darwin --arch arm64 # Full build for macOS ARM
EOF
    exit 1
}

# ---- Argument parsing ----
TARGET=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --os)
            BUILD_OS="$2"
            shift 2
            ;;
        --arch)
            BUILD_ARCH="$2"
            shift 2
            ;;
        --help|-h)
            usage
            ;;
        web|server|mcp|image|release)
            TARGET="$1"
            shift
            ;;
        *)
            echo "Error: unknown argument '$1'"
            usage
            ;;
    esac
done
TARGET="${TARGET:-release}"

# Validate inputs
case "$BUILD_OS" in
    linux|darwin|windows) ;;
    *) echo "Error: unsupported OS '$BUILD_OS' (use linux/darwin/windows)"; exit 1 ;;
esac
case "$BUILD_ARCH" in
    amd64|arm64) ;;
    *) echo "Error: unsupported arch '$BUILD_ARCH' (use amd64/arm64)"; exit 1 ;;
esac

# Platform-specific output directory: release/<os>_<arch>/
PLATFORM_TAG="${BUILD_OS}_${BUILD_ARCH}"
BUILD_DIR="$SERVER_DIR/release/$PLATFORM_TAG"
MCP_BINS_DIR="$BUILD_DIR/mcp-bins"

# Binary suffix
EXT=""
[[ "$BUILD_OS" == "windows" ]] && EXT=".exe"

echo "==> Build config: target=$TARGET os=$BUILD_OS arch=$BUILD_ARCH"
echo "    Output dir: $BUILD_DIR"

# ---- Step 1: Frontend ----
build_web() {
    echo "==> Building frontend..."
    cd "$WEB_DIR"
    npm ci --prefer-offline 2>/dev/null || npm install
    npm run build
    rm -rf "$EMBED_DIR"
    cp -r dist "$EMBED_DIR"
    echo "    Frontend built → $EMBED_DIR"
}

# ---- Step 2: Backend server ----
build_server() {
    echo "==> Building server (GOOS=$BUILD_OS GOARCH=$BUILD_ARCH)..."
    cd "$SERVER_DIR"
    mkdir -p "$BUILD_DIR"
    CGO_ENABLED=0 GOOS="$BUILD_OS" GOARCH="$BUILD_ARCH" \
        go build -ldflags="-s -w" -o "$BUILD_DIR/seaturt${EXT}" ./cmd/server/

    # Copy config.yaml.example as template (never ship real config with secrets)
    if [[ -f "$SERVER_DIR/config.yaml.example" ]]; then
        cp "$SERVER_DIR/config.yaml.example" "$BUILD_DIR/config.yaml"
        echo "    Config template copied → $BUILD_DIR/config.yaml"
    else
        log_warn "config.yaml.example not found, skipping config copy"
    fi

    # Copy Docker sandbox files into release dir
    # The Dockerfile references: svc-selkies-run, svc-browser-daemon-run,
    # svc-wechat-run, mcp-servers/browser/, mcp-servers/wechat/
    local DOCKER_SRC="$SERVER_DIR/docker/sandbox"
    local DOCKER_DST="$BUILD_DIR/docker"
    mkdir -p "$DOCKER_DST"
    cp "$DOCKER_SRC/Dockerfile" "$DOCKER_DST/Dockerfile"
    # s6 service scripts
    for svc in svc-selkies-run svc-browser-daemon-run svc-wechat-run svc-wechat-keyextract-run; do
        if [[ -f "$DOCKER_SRC/$svc" ]]; then
            cp "$DOCKER_SRC/$svc" "$DOCKER_DST/$svc"
        fi
    done
    # mcp-servers directories (browser daemon + wechat code/deps)
    # These are used by Dockerfile for building the image AND
    # by manager.go for deploying code to each agent's workspace.
    if [[ -d "$DOCKER_SRC/mcp-servers" ]]; then
        cp -r "$DOCKER_SRC/mcp-servers" "$DOCKER_DST/mcp-servers"
    fi
    echo "    Docker files copied → $DOCKER_DST/"

    # Copy MCP server source code to release dir for agent workspace deployment.
    # manager.go's deployMCPServers() copies from <serverDir>/mcp-servers/ to workspace.
    local MCP_SERVERS_DST="$BUILD_DIR/mcp-servers"
    mkdir -p "$MCP_SERVERS_DST/wechat" "$MCP_SERVERS_DST/browser"
    # WeChat: Python code + wrapper (exclude test files, __pycache__, build artifacts)
    for f in main.py wechat_ui.py wechat_db.py wechat_db_query.py wechat_launcher.py \
             db_utils.py key_extract.py key_extract_daemon.py mcp-server-wechat requirements.txt; do
        if [[ -f "$DOCKER_SRC/mcp-servers/wechat/$f" ]]; then
            cp "$DOCKER_SRC/mcp-servers/wechat/$f" "$MCP_SERVERS_DST/wechat/$f"
        fi
    done
    chmod +x "$MCP_SERVERS_DST/wechat/mcp-server-wechat" 2>/dev/null || true
    # Browser: only server.js
    if [[ -f "$DOCKER_SRC/mcp-servers/browser/server.js" ]]; then
        cp "$DOCKER_SRC/mcp-servers/browser/server.js" "$MCP_SERVERS_DST/browser/server.js"
    fi
    echo "    MCP server sources copied → $MCP_SERVERS_DST/"

    # Copy prompts directory
    local PROMPTS_SRC="$SERVER_DIR/prompts"
    local PROMPTS_DST="$BUILD_DIR/prompts"
    if [[ -d "$PROMPTS_SRC" ]]; then
        mkdir -p "$PROMPTS_DST"
        cp -r "$PROMPTS_SRC"/* "$PROMPTS_DST"/
        echo "    Prompts copied → $PROMPTS_DST/"
    fi

    # Generate setup.sh for end users
    cat > "$BUILD_DIR/setup.sh" <<'SETUP_EOF'
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "==> Building Docker sandbox image..."
docker build -t seaturt/sandbox:latest "$SCRIPT_DIR/docker/"
echo "    Done: seaturt/sandbox:latest"

echo ""
echo "==> Setup complete! Start seaturt with:"
echo "    cd $SCRIPT_DIR && ./seaturt"
SETUP_EOF
    chmod +x "$BUILD_DIR/setup.sh"
    echo "    setup.sh generated → $BUILD_DIR/setup.sh"

    echo "    Server built → $BUILD_DIR/seaturt${EXT}"
}

# ---- Step 3: MCP Servers (always linux) ----
build_mcp() {
    echo "==> Building MCP servers (GOOS=linux GOARCH=$BUILD_ARCH)..."
    mkdir -p "$MCP_BINS_DIR"

    # Build Go-based MCP servers
    for dir in "$MCP_SERVERS_DIR"/*/; do
        [ -d "$dir" ] || continue
        [ -f "$dir/go.mod" ] || continue
        name=$(basename "$dir")
        echo "    -> mcp-server-$name"
        (
            cd "$dir"
            CGO_ENABLED=0 GOOS=linux GOARCH="$BUILD_ARCH" \
                go build -ldflags="-s -w" -o "$MCP_BINS_DIR/mcp-server-$name" .
        )
    done

    # Build Python-based MCP servers (PyInstaller → single binary)
    # PyInstaller cannot cross-compile — it packages native .so libs from the current OS.
    # If host is not linux (or arch mismatches), we use Docker to build inside a linux container.
    HOST_OS="$(detect_os)"
    HOST_ARCH="$(detect_arch)"
    NEED_DOCKER_BUILD=false
    if [[ "$HOST_OS" != "linux" ]]; then
        echo "    [INFO] Host is $HOST_OS, will use Docker to build Python MCP servers for linux/$BUILD_ARCH"
        NEED_DOCKER_BUILD=true
    elif [[ "$HOST_ARCH" != "$BUILD_ARCH" ]]; then
        echo "    [INFO] Host arch is $HOST_ARCH but target is $BUILD_ARCH, will use Docker to build"
        NEED_DOCKER_BUILD=true
    fi

    for dir in "$MCP_SERVERS_DIR"/*/; do
        [ -d "$dir" ] || continue
        [ -f "$dir/requirements.txt" ] || continue
        [ -f "$dir/go.mod" ] && continue  # skip Go projects (already built above)
        name=$(basename "$dir")

        # Check for a pre-built wrapper script (used when PyInstaller won't work
        # due to system dependencies like gi.repository.Atspi, pysqlcipher3, etc.)
        # The wrapper is a bash script that sets up env and execs python3 main.py.
        # Python deps + code are pre-installed in the Docker image via Dockerfile.
        if [[ -f "$dir/mcp-server-$name" ]]; then
            echo "    -> mcp-server-$name (wrapper script, skip PyInstaller)"
            cp "$dir/mcp-server-$name" "$MCP_BINS_DIR/mcp-server-$name"
            chmod +x "$MCP_BINS_DIR/mcp-server-$name"
            continue
        fi

        echo "    -> mcp-server-$name (python/pyinstaller)"

        if [[ "$NEED_DOCKER_BUILD" == "true" ]]; then
            # Build inside a linux container, mount source dir + output dir
            docker run --rm \
                --platform "linux/$BUILD_ARCH" \
                -v "$dir:/src" \
                -v "$MCP_BINS_DIR:/out" \
                python:3.11-slim \
                sh -c "
                    apt-get update -qq && apt-get install -y -qq binutils > /dev/null 2>&1 &&
                    cd /src &&
                    pip install -q -r requirements.txt &&
                    pip install -q pyinstaller &&
                    pyinstaller --onefile --name 'mcp-server-$name' --clean --noconfirm --distpath /out main.py
                "
        else
            (
                cd "$dir"
                pip install -q -r requirements.txt
                pyinstaller --onefile --name "mcp-server-$name" --clean --noconfirm --distpath "$MCP_BINS_DIR" main.py
            )
        fi
    done

    echo "    MCP bins → $MCP_BINS_DIR:"
    ls -lh "$MCP_BINS_DIR/" 2>/dev/null || echo "    (empty)"
}

# ---- Step 4: Docker sandbox image ----
build_image() {
    echo "==> Building Docker image (platform=linux/$BUILD_ARCH)..."
    cd "$SERVER_DIR"
    docker build --platform "linux/$BUILD_ARCH" \
        -t seaturt/sandbox:latest \
        ./docker/sandbox/
    echo "    Docker image built: seaturt/sandbox:latest"
}

# ---- Dispatch ----
case "$TARGET" in
    web)     build_web ;;
    server)  build_server ;;
    mcp)     build_mcp ;;
    image)   build_image ;;
    release) build_web; build_mcp; build_server; build_image ;;
esac

echo "==> Done! (os=$BUILD_OS arch=$BUILD_ARCH target=$TARGET)"
