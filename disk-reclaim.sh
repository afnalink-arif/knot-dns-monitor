#!/bin/bash
# disk-reclaim.sh — Automatic disk reclaim for knot-dns-monitor
# Run via cron: 0 3 * * * /root/knot-dns-monitor/disk-reclaim.sh >> /var/log/disk-reclaim.log 2>&1

set -euo pipefail
cd /root/knot-dns-monitor

DISK_WARN_PERCENT=85
CH_RETENTION_DAYS=14
LOG_PREFIX="[disk-reclaim $(date '+%Y-%m-%d %H:%M:%S')]"

log() { echo "$LOG_PREFIX $*"; }

disk_usage_percent() {
    df / --output=pcent | tail -1 | tr -d ' %'
}

log "=== Starting disk reclaim ==="
log "Disk usage before: $(disk_usage_percent)%"

# 1. ClickHouse: force TTL cleanup and drop old partitions beyond retention
log "Cleaning ClickHouse data older than ${CH_RETENTION_DAYS} days..."
CUTOFF_DATE=$(date -d "-${CH_RETENTION_DAYS} days" '+%Y-%m-%d')
# Get partitions older than cutoff
OLD_PARTITIONS=$(docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
    --query "SELECT DISTINCT partition FROM system.parts WHERE database='dnsmonitor' AND table='dns_queries' AND active AND partition < '${CUTOFF_DATE}' FORMAT TabSeparated" 2>/dev/null || true)

if [ -n "$OLD_PARTITIONS" ]; then
    while IFS= read -r part; do
        [ -z "$part" ] && continue
        log "  Dropping partition: $part"
        docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
            --query "ALTER TABLE dns_queries DROP PARTITION '${part}';" 2>/dev/null || true
    done <<< "$OLD_PARTITIONS"
fi

# Also clean aggregation tables
for tbl in dns_queries_1m top_domains_1h; do
    docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
        --query "ALTER TABLE ${tbl} DELETE WHERE toDate(timestamp) < today() - 60;" 2>/dev/null || true
done

# Optimize tables to reclaim disk
docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
    --query "OPTIMIZE TABLE dns_queries FINAL;" 2>/dev/null || true
docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
    --query "OPTIMIZE TABLE dns_queries_1m FINAL;" 2>/dev/null || true
docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
    --query "OPTIMIZE TABLE top_domains_1h FINAL;" 2>/dev/null || true

log "ClickHouse cleanup done"

# 2. Truncate old container logs (for containers without log rotation)
log "Truncating large container logs..."
find /var/lib/docker/containers/ -name "*-json.log" -size +50M -exec truncate -s 0 {} \; 2>/dev/null || true

# 3. Docker system prune (unused images, build cache, dead containers)
log "Pruning Docker system..."
docker system prune -f --volumes 2>/dev/null | tail -1 || true
docker builder prune -f 2>/dev/null | tail -1 || true

# 4. Clean old journal logs (keep 3 days)
if command -v journalctl &>/dev/null; then
    log "Vacuuming journal logs..."
    journalctl --vacuum-time=3d --quiet 2>/dev/null || true
fi

# 5. Clean apt cache
if command -v apt-get &>/dev/null; then
    apt-get clean -qq 2>/dev/null || true
fi

USAGE_AFTER=$(disk_usage_percent)
log "Disk usage after: ${USAGE_AFTER}%"

# 6. Emergency: if still above threshold, reduce ClickHouse to 7 days
if [ "$USAGE_AFTER" -ge "$DISK_WARN_PERCENT" ]; then
    log "WARNING: Disk still at ${USAGE_AFTER}% — emergency cleanup, reducing to 7 days retention"
    EMERGENCY_DATE=$(date -d "-7 days" '+%Y-%m-%d')
    EMERGENCY_PARTS=$(docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
        --query "SELECT DISTINCT partition FROM system.parts WHERE database='dnsmonitor' AND table='dns_queries' AND active AND partition < '${EMERGENCY_DATE}' FORMAT TabSeparated" 2>/dev/null || true)
    if [ -n "$EMERGENCY_PARTS" ]; then
        while IFS= read -r part; do
            [ -z "$part" ] && continue
            log "  Emergency drop partition: $part"
            docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
                --query "ALTER TABLE dns_queries DROP PARTITION '${part}';" 2>/dev/null || true
        done <<< "$EMERGENCY_PARTS"
        docker compose exec -T clickhouse clickhouse-client -d dnsmonitor \
            --query "OPTIMIZE TABLE dns_queries FINAL;" 2>/dev/null || true
    fi
    log "Disk usage final: $(disk_usage_percent)%"
fi

# 7. Adjust kresd cache size if it changed
if [ -f "${PROJECT_DIR}/calculate-cache-size.sh" ]; then
    source "${PROJECT_DIR}/calculate-cache-size.sh"
    NEW_CACHE=$(calculate_cache_size)
    CURRENT_CACHE=$(grep 'size-max:' config/kresd/config.yaml 2>/dev/null | awk '{print $2}' || echo "")
    if [ -n "$NEW_CACHE" ] && [ "$NEW_CACHE" != "$CURRENT_CACHE" ]; then
        log "Adjusting kresd cache: ${CURRENT_CACHE} -> ${NEW_CACHE}"
        sed -i "s/size-max: .*/size-max: ${NEW_CACHE}/" config/kresd/config.yaml
        # Restart kresd to apply new cache size
        docker compose stop kresd dnstap-ingester 2>/dev/null || true
        docker compose up -d dnstap-ingester 2>/dev/null || true
        sleep 2
        docker compose up -d kresd 2>/dev/null || true
        log "kresd restarted with cache ${NEW_CACHE}"
    fi
fi

log "=== Disk reclaim complete ==="
