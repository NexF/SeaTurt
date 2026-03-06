# ===== Stage 1: Build MCP servers =====
FROM golang:1.23-alpine AS builder

WORKDIR /build/core
COPY mcp-servers/core/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /mcp-server-core .

WORKDIR /build/desktop
COPY mcp-servers/desktop/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /mcp-server-desktop .

# ===== Stage 2: Unified image based on LinuxServer Webtop (KDE + KasmVNC) =====
FROM lscr.io/linuxserver/webtop:ubuntu-kde

# Basic dev tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl wget git vim nano \
    python3 python3-pip \
    build-essential \
    jq tree ripgrep \
    ca-certificates \
    scrot imagemagick \
    xdotool wmctrl \
    && rm -rf /var/lib/apt/lists/*

# CJK fonts and emoji
RUN apt-get update && apt-get install -y --no-install-recommends \
    fonts-noto-cjk \
    fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/*

# Install MCP server binaries to a staging directory.
# At runtime, manager.go copies them to /workspace/.seaturt/tools/
# so that the Executor can locate them via the same toolsDir path.
COPY --from=builder /mcp-server-core /opt/seaturt/mcp-bins/mcp-server-core
COPY --from=builder /mcp-server-desktop /opt/seaturt/mcp-bins/mcp-server-desktop
RUN chmod +x /opt/seaturt/mcp-bins/mcp-server-core /opt/seaturt/mcp-bins/mcp-server-desktop

# Create workspace directory
RUN mkdir -p /workspace && chown 1000:1000 /workspace
VOLUME /workspace

# Selkies config: lock 1080p resolution + enable shared session
ENV SELKIES_IS_MANUAL_RESOLUTION_MODE=true \
    SELKIES_MANUAL_WIDTH=1920 \
    SELKIES_MANUAL_HEIGHT=1080 \
    SELKIES_ENABLE_SHARED=true

# Override Selkies launch script to add --enable-shared (multi-connection)
# and --is-manual-resolution-mode (lock 1080p). The stock script only passes
# --addr and --mode, ignoring SELKIES_* env vars for CLI flags.
COPY svc-selkies-run /etc/s6-overlay/s6-rc.d/svc-selkies/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-selkies/run

# Ports: 3000 (HTTP Selkies), 3001 (HTTPS Selkies)
EXPOSE 3000 3001
