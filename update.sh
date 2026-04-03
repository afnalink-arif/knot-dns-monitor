#!/bin/bash
set -euo pipefail

# ============================================
# Knot DNS Monitor - Updater
# ============================================
# Pulls latest code, rebuilds custom images,
# and restarts services with zero-downtime DNS.
# ============================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

NONINTERACTIVE="${NONINTERACTIVE:-0}"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$PROJECT_DIR"

if [[ "$NONINTERACTIVE" == "1" ]]; then
  info()  { echo "[INFO] $1"; }
  ok()    { echo "[OK] $1"; }
  warn()  { echo "[WARN] $1"; }
  error() { echo "[ERROR] $1"; exit 1; }
else
  info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
  ok()    { echo -e "${GREEN}[OK]${NC} $1"; }
  warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
  error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }
fi

echo ""
echo -e "${BLUE}╔══════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║   Knot DNS Monitor - Updater             ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════╝${NC}"
echo ""

# ---- Pre-flight checks ----
if [[ ! -f .env ]]; then
    error ".env not found. Run install.sh first."
fi

if [[ ! -f docker-compose.yml ]]; then
    error "docker-compose.yml not found. Are you in the project directory?"
fi

# Load current config
source .env

# ---- Step 1: Pull latest code ----
info "Pulling latest code from git..."
if git rev-parse --is-inside-work-tree &>/dev/null; then
    BEFORE=$(git rev-parse HEAD)
    git pull --ff-only 2>&1 || {
        warn "git pull failed. You may have local changes."
        if [[ "$NONINTERACTIVE" == "1" ]]; then
            warn "Non-interactive mode, continuing anyway."
        else
            read -rp "  Continue anyway? [y/N]: " CONT
            [[ "$CONT" =~ ^[Yy]$ ]] || exit 1
        fi
    }
    AFTER=$(git rev-parse HEAD)

    if [[ "$BEFORE" == "$AFTER" ]]; then
        info "Already up to date."
        if [[ "$NONINTERACTIVE" == "1" ]]; then
            info "Non-interactive mode, rebuilding anyway."
        else
            read -rp "Force rebuild anyway? [y/N]: " FORCE
            if [[ ! "$FORCE" =~ ^[Yy]$ ]]; then
                echo "Nothing to do."
                exit 0
            fi
        fi
    else
        COMMITS=$(git log --oneline "${BEFORE}..${AFTER}" | wc -l)
        ok "Pulled ${COMMITS} new commit(s)"
        echo ""
        git log --oneline "${BEFORE}..${AFTER}" | head -10
        echo ""
    fi
else
    warn "Not a git repository, skipping pull"
fi

# ---- Step 2: Regenerate configs from templates (if templates changed) ----
info "Regenerating configs from templates..."

if [[ -f config/Caddyfile.template ]]; then
    sed "s/__DOMAIN__/${DOMAIN}/g" config/Caddyfile.template > config/Caddyfile
    ok "Regenerated: config/Caddyfile"
fi

if [[ -f config/kresd/config.yaml.template ]]; then
    SUBNET_VIEWS=""
    IFS=',' read -ra SUBNET_ARRAY <<< "$ALLOWED_SUBNETS"
    for subnet in "${SUBNET_ARRAY[@]}"; do
        subnet=$(echo "$subnet" | xargs)
        SUBNET_VIEWS="${SUBNET_VIEWS}  - subnets: ['${subnet}']
    answer: allow
"
    done

    TEMP_CONFIG=$(mktemp)
    while IFS= read -r line; do
        if [[ "$line" == *"__SUBNET_VIEWS__"* ]]; then
            printf '%s' "$SUBNET_VIEWS"
        elif [[ "$line" == *"__CACHE_SIZE__"* ]]; then
            echo "${line//__CACHE_SIZE__/$CACHE_SIZE}"
        else
            echo "$line"
        fi
    done < config/kresd/config.yaml.template > "$TEMP_CONFIG"
    mv "$TEMP_CONFIG" config/kresd/config.yaml
    ok "Regenerated: config/kresd/config.yaml"
fi

# ---- Step 3: Rebuild custom images ----
info "Rebuilding custom images..."
export APP_VERSION=$(cat VERSION 2>/dev/null || echo "dev")
docker compose build --parallel 2>&1 | tail -5
ok "Images rebuilt (version: ${APP_VERSION})"

# ---- Step 4: Rolling restart ----
info "Restarting services..."

# Restart infra first (they have health checks)
info "  Restarting infrastructure services..."
docker compose up -d clickhouse redis postgres
sleep 3

# Restart dnstap-ingester before kresd (socket dependency)
info "  Restarting dnstap-ingester..."
docker compose up -d dnstap-ingester
sleep 2

# Restart kresd
info "  Restarting kresd..."
docker compose up -d kresd
sleep 2

# Restart monitoring
info "  Restarting monitoring..."
docker compose up -d prometheus node-exporter

# Restart app layer
info "  Restarting backend & frontend..."
docker compose up -d backend frontend

# Restart reverse proxy last
info "  Restarting caddy..."
docker compose up -d caddy

ok "All services restarted"

# ---- Step 5: Health check ----
echo ""
info "Running health checks..."

# Wait for services
sleep 5

# Check all containers running
RUNNING=$(docker compose ps --status running --format json 2>/dev/null | wc -l || echo "0")
TOTAL=$(docker compose ps --format json 2>/dev/null | wc -l || echo "0")
if [[ "$RUNNING" -ge 10 ]]; then
    ok "All ${RUNNING} services running"
else
    warn "Only ${RUNNING}/${TOTAL} services running"
    docker compose ps
fi

# Check DNS
if command -v dig &>/dev/null; then
    RESULT=$(dig @127.0.0.1 google.com +short +timeout=5 2>/dev/null | head -1 || echo "FAILED")
    if [[ "$RESULT" != "FAILED" && -n "$RESULT" ]]; then
        ok "DNS resolver working (google.com -> ${RESULT})"
    else
        warn "DNS not responding. Check: docker compose logs kresd --tail 20"
    fi
fi

# Check dashboard
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:3000" 2>/dev/null || echo "000")
if [[ "$HTTP_CODE" == "200" ]]; then
    ok "Dashboard accessible (HTTP ${HTTP_CODE})"
else
    warn "Dashboard returned HTTP ${HTTP_CODE}"
fi

echo ""
echo -e "${GREEN}Update complete!${NC}"
echo "  Dashboard: https://${DOMAIN}"
echo ""
