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
echo "== claim permission smoke tests =="
GUEST_USER="rumpty"
HOME_DIR="/home/${GUEST_USER}"

if [ ! -d "${HOME_DIR}" ]; then
  echo "WARN: ${HOME_DIR} does not exist — mkhomedir_helper will be needed at claim time"
  mkhomedir_helper "${GUEST_USER}" && echo "OK: mkhomedir_helper created ${HOME_DIR}" \
    || echo "FAIL: mkhomedir_helper failed — mkdir fallback will be used"
else
  echo "OK: ${HOME_DIR} exists"
fi

touch "${HOME_DIR}/.rumpty-verify" && rm "${HOME_DIR}/.rumpty-verify" \
  && echo "OK: write to ${HOME_DIR} works" \
  || echo "FAIL: cannot write to ${HOME_DIR} — check ProtectHome in service unit"

touch /etc/.rumpty-verify && rm /etc/.rumpty-verify \
  && echo "OK: write to /etc works" \
  || echo "FAIL: cannot write to /etc — check ProtectSystem in service unit"

mkdir -p /var/lib/rumpty
touch /var/lib/rumpty/.rumpty-verify && rm /var/lib/rumpty/.rumpty-verify \
  && echo "OK: write to /var/lib/rumpty works" \
  || echo "FAIL: cannot write to /var/lib/rumpty — check ProtectSystem in service unit"

install -d -m 700 "${HOME_DIR}/.ssh-verify"
chown "${GUEST_USER}:${GUEST_USER}" "${HOME_DIR}/.ssh-verify" \
  && echo "OK: chown works" \
  || echo "FAIL: chown failed — check CapabilityBoundingSet in service unit"
rm -rf "${HOME_DIR}/.ssh-verify"

echo
echo "Verification complete. A VSOCK collector must be listening on CID 2 port 5000 for successful pushes."
