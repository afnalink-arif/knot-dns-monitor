#!/bin/bash
set -euo pipefail

echo "=== KResD Manager - Setup ==="

# Generate secrets
echo "Generating secrets..."
mkdir -p secrets
if [ ! -f secrets/pg_password.txt ]; then
    openssl rand -base64 32 > secrets/pg_password.txt
    echo "  Created: secrets/pg_password.txt"
fi
if [ ! -f secrets/jwt_secret.txt ]; then
    openssl rand -base64 64 > secrets/jwt_secret.txt
    echo "  Created: secrets/jwt_secret.txt"
fi

# Generate self-signed TLS certs (for development)
if [ ! -f config/kresd/tls/server.crt ]; then
    echo "Generating self-signed TLS certificate..."
    mkdir -p config/kresd/tls
    openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
        -keyout config/kresd/tls/server.key \
        -out config/kresd/tls/server.crt \
        -subj "/CN=216.afna.link" \
        -addext "subjectAltName=DNS:216.afna.link,DNS:localhost,IP:127.0.0.1"
    echo "  Created: config/kresd/tls/server.{crt,key}"
fi

# Create .env if not exists
if [ ! -f .env ]; then
    PG_PASS=$(cat secrets/pg_password.txt)
    echo "PG_PASSWORD=${PG_PASS}" > .env
    echo "  Created: .env"
fi

# Disable systemd-resolved if running (it binds port 53)
if systemctl is-active --quiet systemd-resolved 2>/dev/null; then
    echo ""
    echo "WARNING: systemd-resolved is running and binds port 53."
    echo "Run the following to disable it:"
    echo "  sudo systemctl disable --now systemd-resolved"
    echo "  sudo rm /etc/resolv.conf"
    echo "  echo 'nameserver 127.0.0.1' | sudo tee /etc/resolv.conf"
fi

echo ""
echo "=== Setup complete! ==="
echo ""
echo "To start all services:"
echo "  docker compose up -d"
echo ""
echo "Dashboard will be available at:"
echo "  http://localhost:3000"
echo ""
echo "DNS resolver will listen on:"
echo "  DNS:  port 53  (UDP/TCP)"
echo "  DoT:  port 853"
echo "  DoH:  port 443"
echo "  DoQ:  port 8853"
