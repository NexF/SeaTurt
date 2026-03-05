#!/bin/bash
# start-desktop.sh — Starts Xvfb + GNOME + VNC + noVNC
# Called by entrypoint.sh when DESKTOP_ENABLED=true

set -e

export DISPLAY=:99
export RESOLUTION=${RESOLUTION:-1920x1080x24}

echo "Starting desktop environment..."

# Start D-Bus daemon
if [ ! -d /run/dbus ]; then
    mkdir -p /run/dbus
fi
dbus-daemon --system --fork 2>/dev/null || true

# Start Xvfb virtual display
Xvfb :99 -screen 0 "$RESOLUTION" -ac +extension GLX +render -noreset &
XVFB_PID=$!
sleep 1

# Verify Xvfb is running
if ! kill -0 $XVFB_PID 2>/dev/null; then
    echo "ERROR: Xvfb failed to start"
    exit 1
fi

# Configure VNC password
mkdir -p /home/agent/.vnc
echo "seaturt" | vncpasswd -f > /home/agent/.vnc/passwd
chmod 600 /home/agent/.vnc/passwd
chown -R agent:agent /home/agent/.vnc

# Start VNC Server (connects to Xvfb)
x0vncserver -display :99 -rfbport 5900 -SecurityTypes VncAuth \
    -PasswordFile /home/agent/.vnc/passwd &
VNC_PID=$!
sleep 1

# Verify VNC is running
if ! kill -0 $VNC_PID 2>/dev/null; then
    echo "ERROR: VNC server failed to start"
    exit 1
fi

# Start GNOME session via dbus-launch
dbus-launch --exit-with-session gnome-session --session=gnome 2>/dev/null &
sleep 2

# Disable screensaver, screen lock, and power management
gsettings set org.gnome.desktop.screensaver lock-enabled false 2>/dev/null || true
gsettings set org.gnome.desktop.screensaver idle-activation-enabled false 2>/dev/null || true
gsettings set org.gnome.desktop.session idle-delay 0 2>/dev/null || true
gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-ac-type 'nothing' 2>/dev/null || true

# Start noVNC (WebSocket proxy: HTTP port 6080 → VNC port 5900)
websockify --web /usr/share/novnc 6080 localhost:5900 &
NOVNC_PID=$!
sleep 1

echo "Desktop environment ready"
echo "  Xvfb PID:    $XVFB_PID"
echo "  VNC PID:     $VNC_PID (port 5900)"
echo "  noVNC PID:   $NOVNC_PID (port 6080)"
echo "  Display:     $DISPLAY"
echo "  Resolution:  $RESOLUTION"
