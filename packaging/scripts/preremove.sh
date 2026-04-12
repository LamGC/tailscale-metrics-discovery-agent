#!/bin/sh
set -e

# Stop and disable services before removal.
if [ -d /run/systemd/system ]; then
    systemctl stop tsd-agent.service 2>/dev/null || true
    systemctl stop tsd-central.service 2>/dev/null || true
    systemctl disable tsd-agent.service 2>/dev/null || true
    systemctl disable tsd-central.service 2>/dev/null || true
    systemctl daemon-reload || true
elif command -v update-rc.d >/dev/null 2>&1; then
    service tsd-agent stop 2>/dev/null || true
    service tsd-central stop 2>/dev/null || true
    update-rc.d -f tsd-agent remove || true
    update-rc.d -f tsd-central remove || true
elif command -v chkconfig >/dev/null 2>&1; then
    service tsd-agent stop 2>/dev/null || true
    service tsd-central stop 2>/dev/null || true
    chkconfig --del tsd-agent || true
    chkconfig --del tsd-central || true
fi
