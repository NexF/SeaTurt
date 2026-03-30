# Unified sandbox image based on LinuxServer Webtop (KDE + Selkies)
# MCP server binaries are no longer built inside the image.
# They are pre-compiled on the host (via build.sh mcp) and copied into
# the container at agent creation time by manager.go (docker cp).
FROM lscr.io/linuxserver/webtop:ubuntu-kde

# Run as root inside the container
ENV PUID=0
ENV PGID=0

# Dev tools + CJK fonts + desktop automation utilities + AT-SPI2 (for wechat MCP)
# + DB decryption deps: gcc, libsqlcipher-dev, python3-dev (for pysqlcipher3)
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl wget git vim nano \
    python3 python3-pip \
    build-essential jq tree ripgrep ca-certificates \
    scrot imagemagick xdotool wmctrl \
    fonts-noto-cjk fonts-noto-color-emoji \
    at-spi2-core gir1.2-atspi-2.0 python3-gi dbus-x11 \
    gcc libsqlcipher-dev python3-dev \
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
# Code (server.js) is deployed to workspace at agent creation time.
# Only npm deps and Playwright Chromium binary are installed in the image.
COPY mcp-servers/browser/package.json /opt/browser-daemon-deps/package.json
RUN cd /opt/browser-daemon-deps \
    && npm install --production \
    && npx playwright install chromium \
    && npx playwright install-deps chromium
# Global install @playwright/mcp so npx can find it from any path
RUN npm install -g @playwright/mcp
# Create workspace mount-point directories (will be populated at runtime)
RUN mkdir -p /workspace/.seaturt/mcp-servers/browser/user-data

# ---- WeChat MCP server: install Python deps only ----
# Code is deployed to workspace at agent creation time.
# Only compiled deps (pysqlcipher3, zstandard) are installed in the image.
COPY mcp-servers/wechat/requirements.txt /tmp/wechat-requirements.txt
RUN pip install --no-cache-dir --break-system-packages -r /tmp/wechat-requirements.txt \
    && rm -f /tmp/wechat-requirements.txt
# Create workspace mount-point directories (will be populated at runtime)
RUN mkdir -p /workspace/.seaturt/mcp-servers/wechat/session

# ---- Register browser daemon as s6 service ----
COPY svc-browser-daemon-run /etc/s6-overlay/s6-rc.d/svc-browser-daemon/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-browser-daemon/run \
    && echo "longrun" > /etc/s6-overlay/s6-rc.d/svc-browser-daemon/type \
    && mkdir -p /etc/s6-overlay/s6-rc.d/svc-browser-daemon/dependencies.d \
    && touch /etc/s6-overlay/s6-rc.d/svc-browser-daemon/dependencies.d/svc-de \
    && mkdir -p /etc/s6-overlay/s6-rc.d/user/contents.d \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-browser-daemon

# ---- Register wechat launcher as s6 service ----
# Starts D-Bus + AT-SPI2 + WeChat with QT_ACCESSIBILITY=1.
# WeChat binary is NOT installed in the image — if missing, the service
# will exit and main.py will return an install prompt to the user.
COPY svc-wechat-run /etc/s6-overlay/s6-rc.d/svc-wechat/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-wechat/run \
    && echo "longrun" > /etc/s6-overlay/s6-rc.d/svc-wechat/type \
    && mkdir -p /etc/s6-overlay/s6-rc.d/svc-wechat/dependencies.d \
    && touch /etc/s6-overlay/s6-rc.d/svc-wechat/dependencies.d/svc-de \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-wechat

# ---- Register wechat key extraction daemon as s6 service ----
# Long-running daemon that polls for WeChat databases, extracts decryption
# keys via /proc/PID/mem scanning, and writes results to JSON cache.
# MCP tool calls (main.py) read from this cache file.
COPY svc-wechat-keyextract-run /etc/s6-overlay/s6-rc.d/svc-wechat-keyextract/run
RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-wechat-keyextract/run \
    && echo "longrun" > /etc/s6-overlay/s6-rc.d/svc-wechat-keyextract/type \
    && mkdir -p /etc/s6-overlay/s6-rc.d/svc-wechat-keyextract/dependencies.d \
    && touch /etc/s6-overlay/s6-rc.d/svc-wechat-keyextract/dependencies.d/svc-de \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-wechat-keyextract

# Ports: 3000 (HTTP Selkies), 3001 (HTTPS Selkies)
EXPOSE 3000 3001
