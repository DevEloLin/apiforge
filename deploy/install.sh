#!/usr/bin/env bash
# Install apiforge as a systemd service. Run as root on the target host.
#
#   sudo deploy/install.sh [/path/to/apiforge-binary] [service-user]
#
# - installs the binary to /usr/local/bin/apiforge
# - seeds /etc/apiforge/apiforge.env (does NOT overwrite an existing one)
# - installs /etc/systemd/system/apiforge.service (User= set to <service-user>)
# - reloads systemd (does not auto-start; edit the env file first)
set -euo pipefail

BIN="${1:-dist/apiforge-linux-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')}"
USER_NAME="${2:-$(logname 2>/dev/null || echo apiforge)}"
HERE="$(cd "$(dirname "$0")" && pwd)"

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }
[ -f "$BIN" ] || { echo "binary not found: $BIN (build with deploy/build.sh)"; exit 1; }

install -m 0755 "$BIN" /usr/local/bin/apiforge
install -d -m 0755 /etc/apiforge
if [ ! -f /etc/apiforge/apiforge.env ]; then
  install -m 0600 "$HERE/apiforge.env.example" /etc/apiforge/apiforge.env
  echo "seeded /etc/apiforge/apiforge.env — EDIT IT (set API_KEYS, credential paths) before starting"
else
  echo "/etc/apiforge/apiforge.env exists — left untouched"
fi
# The service runs as $USER_NAME, so it must be able to READ the config (0600
# root:root would be unreadable to it). Own the file by the service user, keep 0600
# (it holds API_KEYS/secrets — not world-readable).
if id "$USER_NAME" >/dev/null 2>&1; then
  chown "$USER_NAME" /etc/apiforge/apiforge.env
  chmod 0600 /etc/apiforge/apiforge.env
else
  echo "WARNING: user '$USER_NAME' does not exist — create it, or the service won't start"
fi

# Install unit with the chosen User= (Group defaults to that user's primary group).
sed "s/^User=apiforge/User=${USER_NAME}/" \
  "$HERE/apiforge.service" > /etc/systemd/system/apiforge.service
systemctl daemon-reload

cat <<EOF

installed. next:
  1. sudo \$EDITOR /etc/apiforge/apiforge.env      # set API_KEYS + *_AUTHS paths
  2. in the unit, uncomment ReadWritePaths for the credential dirs (token write-back)
  3. sudo systemctl enable --now apiforge
  4. journalctl -u apiforge -f
EOF
