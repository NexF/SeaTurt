#!/bin/bash
set -e

# If HOST_UID is set and differs from agent's current UID, update it
if [ -n "$HOST_UID" ]; then
    CURRENT_UID=$(id -u agent)
    if [ "$HOST_UID" != "$CURRENT_UID" ]; then
        usermod -u "$HOST_UID" agent 2>/dev/null || true
        # Fix home directory ownership
        chown -R agent:agent /home/agent 2>/dev/null || true
    fi
fi

# If HOST_GID is set, update agent's group
if [ -n "$HOST_GID" ]; then
    CURRENT_GID=$(id -g agent)
    if [ "$HOST_GID" != "$CURRENT_GID" ]; then
        groupmod -g "$HOST_GID" agent 2>/dev/null || true
    fi
fi

# Ensure workspace is accessible
chown agent:agent /workspace 2>/dev/null || true

# Start desktop environment if enabled (runs as root, then drops privileges)
if [ "$DESKTOP_ENABLED" = "true" ] && [ -x /usr/local/bin/start-desktop.sh ]; then
    echo "Desktop mode enabled, starting VNC + GNOME..."
    gosu agent /usr/local/bin/start-desktop.sh
fi

# Step down to agent user and exec the CMD
exec gosu agent "$@"
