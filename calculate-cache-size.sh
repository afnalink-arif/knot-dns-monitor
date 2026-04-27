#!/bin/bash
# calculate-cache-size.sh — Dynamically compute optimal kresd cache size
# Based on: min(50% free disk, 70% RAM, 8G)
#
# Usage:
#   source calculate-cache-size.sh
#   CACHE_SIZE=$(calculate_cache_size)
#
# Or directly:
#   ./calculate-cache-size.sh          # prints e.g. "2G"

calculate_cache_size() {
    # 1. Available disk space (for the root partition or PROJECT_DIR)
    local target_dir="${PROJECT_DIR:-/root/knot-dns-monitor}"
    local disk_avail_mb
    disk_avail_mb=$(df -BM --output=avail "$target_dir" 2>/dev/null | tail -1 | tr -d ' M')

    # Reserve: keep at least 5GB free on disk after cache allocation
    local disk_reserve_mb=5120
    local disk_budget_mb=$(( disk_avail_mb - disk_reserve_mb ))
    if (( disk_budget_mb < 256 )); then
        disk_budget_mb=256
    fi

    # 2. Available RAM — use 50% of total (leave room for ClickHouse, Prometheus, etc.)
    local total_mem_mb
    total_mem_mb=$(free -m 2>/dev/null | awk '/^Mem:/{print $2}' || echo "4096")
    local ram_budget_mb=$(( total_mem_mb * 50 / 100 ))

    # 3. Take the minimum of disk budget and RAM budget, cap at 8G
    local cache_mb=$disk_budget_mb
    if (( ram_budget_mb < cache_mb )); then
        cache_mb=$ram_budget_mb
    fi
    if (( cache_mb > 8192 )); then
        cache_mb=8192
    fi

    # 4. Floor at 256M
    if (( cache_mb < 256 )); then
        cache_mb=256
    fi

    # 5. Format: use GB if >= 1024MB, else MB
    if (( cache_mb >= 1024 )); then
        echo "$((cache_mb / 1024))G"
    else
        echo "${cache_mb}M"
    fi
}

# If executed directly (not sourced), print the result
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    calculate_cache_size
fi
