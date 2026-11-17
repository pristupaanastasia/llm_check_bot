#!/usr/bin/env bash
# One-time setup of a fresh Ubuntu server:
# Docker, make, rsync, and a 4 GB swap file so a memory spike can't kill Ollama.
# Run on the server: sudo bash scripts/server-setup.sh
set -euo pipefail

if ! command -v docker >/dev/null; then
  curl -fsSL https://get.docker.com | sh
fi
apt-get update -y && apt-get install -y make rsync
systemctl enable --now docker
if [[ -n "${SUDO_USER:-}" ]]; then
  usermod -aG docker "$SUDO_USER" # docker without sudo (after re-login)
fi

if ! swapon --show | grep -q /swapfile; then
  fallocate -l 4G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  grep -q /swapfile /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

echo "✅ сервер готов: $(docker --version), swap: $(swapon --show --noheadings | awk '{print $3}')"
echo "   перелогиньтесь по ssh, чтобы docker работал без sudo"
