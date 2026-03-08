# Unified sandbox image based on LinuxServer Webtop (KDE + Selkies)
# MCP server binaries are no longer built inside the image.
# They are pre-compiled on the host (via build.sh mcp) and copied into
# the container at agent creation time by manager.go (docker cp).
FROM lscr.io/linuxserver/webtop:ubuntu-kde

# Run as root inside the container
ENV PUID=0
ENV PGID=0

# Dev tools + CJK fonts + desktop automation utilities
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl wget git vim nano \
    python3 python3-pip \
    build-essential jq tree ripgrep ca-certificates \
    scrot imagemagick xdotool wmctrl \
    fonts-noto-cjk fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/*

# Create workspace directory
RUN mkdir -p /workspace
VOLUME /workspace

# Selkies config: lock 1080p resolution + enable shared session
ENV SELKIES_IS_MANUAL_RESOLUTION_MODE=true \
    SELKIES_MANUAL_WIDTH=1920 \
    SELKIES_MANUAL_HEIGHT=1080 \
    SELKIES_ENABLE_SHARED=true

# Override Selkies launch script to add --enable-shared (multi-connection)
# and --is-manual-resolution-mode (lock 1080p).
COPY svc-selkies-run /etc/s6-overlay/s6-rc.d/svc-selkies/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-selkies/run

# ---- Node.js 20 (for browser MCP daemon) ----
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# ---- Browser MCP daemon: install deps + Playwright Chromium ----
COPY mcp-servers/browser/package.json mcp-servers/browser/server.js /opt/browser-daemon/
RUN cd /opt/browser-daemon \
    && npm install --production \
    && npx playwright install chromium \
    && npx playwright install-deps chromium \
    && mkdir -p /opt/browser-daemon/user-data

# ---- Register browser daemon as s6 service ----
COPY svc-browser-daemon-run /etc/s6-overlay/s6-rc.d/svc-browser-daemon/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-browser-daemon/run \
    && echo "longrun" > /etc/s6-overlay/s6-rc.d/svc-browser-daemon/type \
    && mkdir -p /etc/s6-overlay/s6-rc.d/svc-browser-daemon/dependencies.d \
    && touch /etc/s6-overlay/s6-rc.d/svc-browser-daemon/dependencies.d/svc-de \
    && mkdir -p /etc/s6-overlay/s6-rc.d/user/contents.d \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-browser-daemon

# Ports: 3000 (HTTP Selkies), 3001 (HTTPS Selkies)
EXPOSE 3000 3001
