#!/usr/bin/env bash
set -euo pipefail

echo "== rumpty-agent binary =="
/usr/bin/rumpty-agent version

echo
echo "== one-shot metrics =="
/usr/bin/rumpty-agent metrics once --sample-window 1s --pretty >/tmp/rumpty-agent-metrics.json
test -s /tmp/rumpty-agent-metrics.json
head -n 20 /tmp/rumpty-agent-metrics.json

echo
echo "== systemd unit syntax =="
systemd-analyze verify /etc/systemd/system/rumpty-agent.service

echo
echo "== service dry start =="
systemctl daemon-reload
timeout 20s systemctl start rumpty-agent.service || true
systemctl status rumpty-agent.service --no-pager || true
journalctl -u rumpty-agent.service -n 50 --no-pager || true

echo
echo "Verification complete. A VSOCK collector must be listening on CID 2 port 5000 for successful pushes."
