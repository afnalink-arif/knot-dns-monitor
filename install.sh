#!/bin/bash
set -euo pipefail

# ============================================
# Knot DNS Manager - Installer
# ============================================
# Usage:
#   Interactive:  ./install.sh
#   Non-interactive: ./install.sh --ip 1.2.3.4 --domain dns.example.com --subnets "10.0.0.0/24,192.168.1.0/24"
# ============================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
ok()    { echo -e "${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# ---- Parse CLI arguments ----
ARG_IP=""
ARG_DOMAIN=""
ARG_SUBNETS=""
ARG_CACHE=""
ARG_ROLE=""
ARG_NODE_NAME=""

REINSTALL=0

while [[ $# -gt 0 ]]; do
    case $1 in
        --ip)       ARG_IP="$2"; shift 2 ;;
        --domain)   ARG_DOMAIN="$2"; shift 2 ;;
        --subnets)  ARG_SUBNETS="$2"; shift 2 ;;
        --cache)    ARG_CACHE="$2"; shift 2 ;;
        --role)     ARG_ROLE="$2"; shift 2 ;;
        --node-name) ARG_NODE_NAME="$2"; shift 2 ;;
        --reinstall) REINSTALL=1; shift ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --ip IP           Server IP address"
            echo "  --domain DOMAIN   Domain for dashboard (e.g. dns.example.com)"
            echo "  --subnets CIDRS   Comma-separated subnets to allow (e.g. 10.0.0.0/24,192.168.1.0/24)"
            echo "  --cache SIZE      DNS cache size (default: 8G)"
            echo "  --role ROLE       Cluster role: standalone, controller, agent"
            echo "  --node-name NAME  Node name for cluster identification"
            echo "  --reinstall       Clean reinstall: stop services, remove volumes & configs, start fresh"
            echo "  -h, --help        Show this help"
            echo ""
            echo "If options are omitted, the installer will ask interactively."
            exit 0
            ;;
        *) error "Unknown option: $1. Use --help for usage." ;;
    esac
done

# ---- Banner ----
echo ""
echo -e "${BLUE}╔══════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║   Knot DNS Manager - Installer v1.0      ║${NC}"
echo -e "${BLUE}║   Knot Resolver 6.2 + Monitoring Stack   ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════╝${NC}"
echo ""

# ---- Reinstall: clean up previous installation ----
if [[ "$REINSTALL" == "1" ]]; then
    warn "Reinstall mode: this will remove all data (databases, configs, secrets)."
    read -rp "  Are you sure? [y/N]: " confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 0
    fi
    info "Stopping services..."
    docker compose down -v --remove-orphans 2>/dev/null || true
    info "Removing generated configs and secrets..."
    rm -f "${PROJECT_DIR}/config/kresd/config.yaml"
    rm -f "${PROJECT_DIR}/config/kresd/rpz.zone"
    rm -f "${PROJECT_DIR}/config/Caddyfile"
    rm -f "${PROJECT_DIR}/.env"
    rm -rf "${PROJECT_DIR}/secrets"
    ok "Previous installation cleaned"
elif docker compose ps --quiet 2>/dev/null | grep -q .; then
    warn "Services are already running."
    echo "  Options:"
    echo "    1) Continue install (may fail if configs conflict)"
    echo "    2) Reinstall fresh (removes all data)"
    echo "    3) Abort"
    read -rp "  Choose [1/2/3]: " choice
    case "$choice" in
        2)
            info "Stopping services and removing volumes..."
            docker compose down -v --remove-orphans 2>/dev/null || true
            rm -f "${PROJECT_DIR}/config/kresd/config.yaml"
            rm -f "${PROJECT_DIR}/config/kresd/rpz.zone"
            rm -f "${PROJECT_DIR}/config/Caddyfile"
            rm -f "${PROJECT_DIR}/.env"
            rm -rf "${PROJECT_DIR}/secrets"
            ok "Previous installation cleaned"
            ;;
        3) echo "Aborted."; exit 0 ;;
        *) info "Continuing with existing state..." ;;
    esac
fi

# ---- Check prerequisites ----
info "Checking prerequisites..."

if ! command -v docker &>/dev/null; then
    error "Docker is not installed. Install it first: https://docs.docker.com/engine/install/"
fi

if ! docker compose version &>/dev/null; then
    error "Docker Compose v2 is not available. Update Docker or install docker-compose-plugin."
fi

if ! command -v openssl &>/dev/null; then
    error "openssl is not installed. Install it: apt install openssl"
fi

DOCKER_VER=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "unknown")
COMPOSE_VER=$(docker compose version --short 2>/dev/null || echo "unknown")
ok "Docker ${DOCKER_VER}, Compose ${COMPOSE_VER}"

# ---- Gather configuration ----
echo ""
info "Server Configuration"
echo "--------------------"

# Server IP
if [[ -n "$ARG_IP" ]]; then
    SERVER_IP="$ARG_IP"
else
    DEFAULT_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "")
    read -rp "  Server IP address [${DEFAULT_IP}]: " SERVER_IP
    SERVER_IP="${SERVER_IP:-$DEFAULT_IP}"
fi
[[ -z "$SERVER_IP" ]] && error "Server IP is required"
ok "Server IP: ${SERVER_IP}"

# Domain
if [[ -n "$ARG_DOMAIN" ]]; then
    DOMAIN="$ARG_DOMAIN"
else
    read -rp "  Domain for dashboard (e.g. dns.example.com): " DOMAIN
fi
[[ -z "$DOMAIN" ]] && error "Domain is required"
ok "Domain: ${DOMAIN}"

# Subnets
if [[ -n "$ARG_SUBNETS" ]]; then
    SUBNETS="$ARG_SUBNETS"
else
    echo ""
    info "Allowed subnets (clients that can use this DNS resolver)"
    echo "  Enter comma-separated CIDRs."
    echo "  Example: 103.186.204.0/24,103.138.53.0/24"
    read -rp "  Subnets: " SUBNETS
fi
[[ -z "$SUBNETS" ]] && error "At least one subnet is required"
ok "Subnets: ${SUBNETS}"

# Cache size
if [[ -n "$ARG_CACHE" ]]; then
    CACHE_SIZE="$ARG_CACHE"
else
    # Auto-detect: use ~70% of available RAM, capped at 8G
    TOTAL_MEM_MB=$(free -m 2>/dev/null | awk '/^Mem:/{print $2}' || echo "8192")
    AUTO_CACHE_MB=$((TOTAL_MEM_MB * 70 / 100))
    if [[ $AUTO_CACHE_MB -gt 8192 ]]; then
        AUTO_CACHE="8G"
    elif [[ $AUTO_CACHE_MB -gt 1024 ]]; then
        AUTO_CACHE="$((AUTO_CACHE_MB / 1024))G"
    else
        AUTO_CACHE="${AUTO_CACHE_MB}M"
    fi
    read -rp "  DNS cache size [${AUTO_CACHE}]: " CACHE_SIZE
    CACHE_SIZE="${CACHE_SIZE:-$AUTO_CACHE}"
fi
ok "Cache size: ${CACHE_SIZE}"

# Node role
if [[ -n "$ARG_ROLE" ]]; then
    NODE_ROLE="$ARG_ROLE"
else
    echo ""
    info "Cluster Mode"
    echo "  1) Standalone (default) - Single server, no cluster"
    echo "  2) Controller (Pusat)   - Manage multiple DNS servers"
    echo "  3) Agent (Node)         - Managed by a controller"
    read -rp "  Select role [1]: " ROLE_CHOICE
    ROLE_CHOICE="${ROLE_CHOICE:-1}"
    case $ROLE_CHOICE in
        2) NODE_ROLE="controller" ;;
        3) NODE_ROLE="agent" ;;
        *) NODE_ROLE="standalone" ;;
    esac
fi
ok "Role: ${NODE_ROLE}"

# Node name
if [[ -n "$ARG_NODE_NAME" ]]; then
    NODE_NAME="$ARG_NODE_NAME"
else
    DEFAULT_NODE_NAME=$(hostname -s 2>/dev/null || echo "dns-server")
    read -rp "  Node name [${DEFAULT_NODE_NAME}]: " NODE_NAME
    NODE_NAME="${NODE_NAME:-$DEFAULT_NODE_NAME}"
fi
ok "Node name: ${NODE_NAME}"

# ---- Confirmation ----
echo ""
echo -e "${YELLOW}┌─────────────────────────────────────────┐${NC}"
echo -e "${YELLOW}│  Review Configuration                    │${NC}"
echo -e "${YELLOW}├─────────────────────────────────────────┤${NC}"
echo -e "${YELLOW}│${NC}  IP:       ${SERVER_IP}"
echo -e "${YELLOW}│${NC}  Domain:   ${DOMAIN}"
echo -e "${YELLOW}│${NC}  Subnets:  ${SUBNETS}"
echo -e "${YELLOW}│${NC}  Cache:    ${CACHE_SIZE}"
echo -e "${YELLOW}│${NC}  Role:     ${NODE_ROLE}"
echo -e "${YELLOW}│${NC}  Name:     ${NODE_NAME}"
echo -e "${YELLOW}│${NC}  Dir:      ${PROJECT_DIR}"
echo -e "${YELLOW}└─────────────────────────────────────────┘${NC}"
echo ""
read -rp "Proceed with installation? [Y/n]: " CONFIRM
CONFIRM="${CONFIRM:-Y}"
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    echo "Installation cancelled."
    exit 0
fi

# ---- Generate secrets ----
echo ""
info "Generating secrets..."
mkdir -p "${PROJECT_DIR}/secrets"

if [[ ! -f "${PROJECT_DIR}/secrets/pg_password.txt" ]]; then
    openssl rand -hex 24 > "${PROJECT_DIR}/secrets/pg_password.txt"
    ok "Created: secrets/pg_password.txt"
else
    warn "secrets/pg_password.txt already exists, skipping"
fi

if [[ ! -f "${PROJECT_DIR}/secrets/jwt_secret.txt" ]]; then
    openssl rand -hex 48 > "${PROJECT_DIR}/secrets/jwt_secret.txt"
    ok "Created: secrets/jwt_secret.txt"
else
    warn "secrets/jwt_secret.txt already exists, skipping"
fi

# ---- Generate TLS certificates ----
info "Generating TLS certificates for DoT/DoH..."
mkdir -p "${PROJECT_DIR}/config/kresd/tls"
# Ensure RPZ zone file exists (Docker bind mount requires it)
touch "${PROJECT_DIR}/config/kresd/rpz.zone"

if [[ ! -f "${PROJECT_DIR}/config/kresd/tls/server.crt" ]]; then
    openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
        -keyout "${PROJECT_DIR}/config/kresd/tls/server.key" \
        -out "${PROJECT_DIR}/config/kresd/tls/server.crt" \
        -subj "/CN=${DOMAIN}" \
        -addext "subjectAltName=DNS:${DOMAIN},DNS:localhost,IP:${SERVER_IP},IP:127.0.0.1" \
        2>/dev/null
    ok "Created: config/kresd/tls/server.{crt,key} (valid 10 years)"
else
    warn "TLS certificates already exist, skipping"
fi

# ---- Generate Caddyfile ----
info "Generating Caddyfile..."
sed "s/__DOMAIN__/${DOMAIN}/g" \
    "${PROJECT_DIR}/config/Caddyfile.template" \
    > "${PROJECT_DIR}/config/Caddyfile"
ok "Created: config/Caddyfile (domain: ${DOMAIN})"

# ---- Generate kresd config ----
info "Generating Knot Resolver config..."

# Build subnet views YAML
SUBNET_VIEWS=""
IFS=',' read -ra SUBNET_ARRAY <<< "$SUBNETS"
for subnet in "${SUBNET_ARRAY[@]}"; do
    subnet=$(echo "$subnet" | xargs)  # trim whitespace
    SUBNET_VIEWS="${SUBNET_VIEWS}  - subnets: ['${subnet}']
    answer: allow
"
done

# Generate config from template
TEMP_CONFIG=$(mktemp)
while IFS= read -r line; do
    if [[ "$line" == *"__SUBNET_VIEWS__"* ]]; then
        printf '%s' "$SUBNET_VIEWS"
    elif [[ "$line" == *"__WORKERS__"* ]]; then
        echo "${line//__WORKERS__/auto}"
    elif [[ "$line" == *"__CACHE_SIZE__"* ]]; then
        echo "${line//__CACHE_SIZE__/$CACHE_SIZE}"
    elif [[ "$line" == *"__LOCAL_DATA__"* ]]; then
        : # Empty on fresh install, populated by dashboard filtering
    else
        echo "$line"
    fi
done < "${PROJECT_DIR}/config/kresd/config.yaml.template" > "$TEMP_CONFIG"
mv "$TEMP_CONFIG" "${PROJECT_DIR}/config/kresd/config.yaml"
ok "Created: config/kresd/config.yaml (subnets: ${SUBNETS})"

# ---- Generate .env ----
info "Generating .env..."
PG_PASS=$(cat "${PROJECT_DIR}/secrets/pg_password.txt")
cat > "${PROJECT_DIR}/.env" <<EOF
# Generated by install.sh on $(date -u +"%Y-%m-%d %H:%M:%S UTC")
SERVER_IP=${SERVER_IP}
DOMAIN=${DOMAIN}
ALLOWED_SUBNETS=${SUBNETS}
CACHE_SIZE=${CACHE_SIZE}
PG_PASSWORD=${PG_PASS}
NODE_ROLE=${NODE_ROLE}
NODE_NAME="${NODE_NAME}"
EOF
ok "Created: .env"

# ---- Disable systemd-resolved if needed ----
if systemctl is-active --quiet systemd-resolved 2>/dev/null; then
    echo ""
    warn "systemd-resolved is running and binds port 53!"
    read -rp "  Disable systemd-resolved? [Y/n]: " DISABLE_RESOLVED
    DISABLE_RESOLVED="${DISABLE_RESOLVED:-Y}"
    if [[ "$DISABLE_RESOLVED" =~ ^[Yy]$ ]]; then
        systemctl disable --now systemd-resolved
        rm -f /etc/resolv.conf
        echo "nameserver 1.1.1.1" > /etc/resolv.conf
        echo "nameserver 8.8.8.8" >> /etc/resolv.conf
        ok "systemd-resolved disabled, using 1.1.1.1 + 8.8.8.8 temporarily"
    else
        warn "Port 53 conflict! kresd will fail to start until systemd-resolved is disabled."
    fi
fi

# ---- Build and start ----
echo ""
info "Building and starting services..."
cd "${PROJECT_DIR}"

export APP_VERSION=$(cat VERSION 2>/dev/null || echo "dev")
docker compose build --parallel 2>&1 | tail -5
ok "Images built"

docker compose up -d 2>&1
ok "All services started"

# ---- Wait for health checks ----
info "Waiting for services to be healthy..."
TIMEOUT=120
ELAPSED=0
while [[ $ELAPSED -lt $TIMEOUT ]]; do
    HEALTHY=$(docker compose ps --format json 2>/dev/null | grep -c '"healthy"' || true)
    TOTAL=$(docker compose ps --format json 2>/dev/null | wc -l || true)
    RUNNING=$(docker compose ps --status running --format json 2>/dev/null | wc -l || true)

    if [[ $RUNNING -ge 10 ]]; then
        break
    fi

    sleep 3
    ELAPSED=$((ELAPSED + 3))
    printf "\r  Waiting... %ds (running: %d/10)" "$ELAPSED" "$RUNNING"
done
echo ""

# ---- Verify DNS ----
info "Verifying DNS resolver..."
if command -v dig &>/dev/null; then
    RESULT=$(dig @127.0.0.1 google.com +short +timeout=5 2>/dev/null || echo "FAILED")
    if [[ "$RESULT" != "FAILED" && -n "$RESULT" ]]; then
        ok "DNS resolver working (google.com -> ${RESULT})"
    else
        warn "DNS resolver not responding yet. Check: docker compose logs kresd"
    fi
else
    warn "dig not installed, skipping DNS verification. Install: apt install dnsutils"
fi

# ---- Done ----
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║   Installation Complete!                      ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║${NC}                                              ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}  Dashboard:  https://${DOMAIN}$(printf '%*s' $((20 - ${#DOMAIN})) '')${GREEN}║${NC}"
echo -e "${GREEN}║${NC}  DNS:        ${SERVER_IP}:53 (UDP/TCP)          ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}  DoT:        ${SERVER_IP}:853                   ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}  DoH:        ${SERVER_IP}:8853                  ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}                                              ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}  First visit: create your admin account      ${GREEN}║${NC}"
echo -e "${GREEN}║${NC}                                              ${GREEN}║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo "Useful commands:"
echo "  docker compose ps          # Check service status"
echo "  docker compose logs -f     # Follow all logs"
echo "  ./update.sh                # Pull updates & restart"
echo ""
