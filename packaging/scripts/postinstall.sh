#!/bin/sh
set -e

# Create config directory.
mkdir -p /etc/tsd

# Generate default config files if absent.
# tsd writes a default config and exits with an error (no tailscaled),
# which is fine — the config file is the only thing we need here.
for role in agent central; do
    cfg="/etc/tsd/${role}.toml"
    if [ ! -f "$cfg" ]; then
        /usr/bin/tsd "$role" daemon --config "$cfg" 2>/dev/null &
        pid=$!
        # Wait up to 3 seconds for the config file to appear, then kill.
        i=0
        while [ $i -lt 30 ] && [ ! -f "$cfg" ]; do
            sleep 0.1
            i=$((i + 1))
        done
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
done

# Detect init system and enable services.
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable tsd-agent.service 2>/dev/null || true
    systemctl enable tsd-central.service 2>/dev/null || true
elif command -v update-rc.d >/dev/null 2>&1; then
    update-rc.d tsd-agent defaults 2>/dev/null || true
    update-rc.d tsd-central defaults 2>/dev/null || true
elif command -v chkconfig >/dev/null 2>&1; then
    chkconfig --add tsd-agent 2>/dev/null || true
    chkconfig --add tsd-central 2>/dev/null || true
fi

echo ""
echo "tsd installed. Edit /etc/tsd/agent.toml or /etc/tsd/central.toml, then start:"
echo "  systemctl start tsd-agent    # or: service tsd-agent start"
echo "  systemctl start tsd-central  # or: service tsd-central start"
