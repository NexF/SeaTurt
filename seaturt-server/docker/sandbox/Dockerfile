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

# Install MCP servers
COPY --from=builder /mcp-server-core /usr/local/bin/mcp-server-core
COPY --from=builder /mcp-server-desktop /usr/local/bin/mcp-server-desktop
RUN chmod +x /usr/local/bin/mcp-server-core /usr/local/bin/mcp-server-desktop

# Create workspace directory
RUN mkdir -p /workspace && chown 1000:1000 /workspace
VOLUME /workspace

# Ports: 3000 (HTTP KasmVNC), 3001 (HTTPS KasmVNC)
EXPOSE 3000 3001
