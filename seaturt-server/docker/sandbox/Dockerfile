# Stage 1: Build mcp-server-core
FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY mcp-servers/core/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /mcp-server-core .

# Stage 2: Runtime image
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Basic tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl wget git vim nano \
    python3 python3-pip \
    build-essential \
    jq tree ripgrep \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install mcp-server-core
COPY --from=builder /mcp-server-core /usr/local/bin/mcp-server-core
RUN chmod +x /usr/local/bin/mcp-server-core

# Install gosu and sudo
RUN apt-get update && apt-get install -y --no-install-recommends gosu sudo && rm -rf /var/lib/apt/lists/*

# Create default agent user (UID may be changed at runtime) with passwordless sudo
RUN useradd -m -s /bin/bash agent \
    && echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent \
    && chmod 0440 /etc/sudoers.d/agent

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

WORKDIR /workspace

ENTRYPOINT ["/entrypoint.sh"]
# Keep container alive; MCP Servers are launched via docker exec
CMD ["tail", "-f", "/dev/null"]
