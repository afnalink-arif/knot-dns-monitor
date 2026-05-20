# CLAUDE.md — Project Memory for knot-dns-monitor

## Project Overview

Knot Resolver (kresd) 6.2 management system with monitoring dashboard. Full recursive DNS resolver with DNSSEC, DoT, DoH, RPZ filtering (Komdigi TrustPositif), and real-time monitoring.

## Key Files

- **Documentation**: `/root/dns.md` — comprehensive ops doc (26 sections)
- **Project dir**: `/root/knot-dns-monitor/`
- **docker-compose.yml**: 11 services (dnsdist, kresd, dnstap-ingester, prometheus, node-exporter, clickhouse, redis, postgres, backend, frontend, caddy)
- **Config templates**: `config/Caddyfile.template`, `config/kresd/config.yaml.template`
- **RPZ blocklist**: `config/kresd/rpz.zone` (~17.7M entries, Komdigi TrustPositif)
- **LMDB overrides**: `config/kresd/policy-loader.lua.j2`, `config/kresd/kresd.lua.j2` (8GB ruledb mapsize)
- **dnsdist config**: `config/dnsdist/dnsdist.conf.tpl` + `config/dnsdist/entrypoint.sh`
- **Scripts**: `install.sh` (deploy baru), `update.sh` (rolling update), `disk-reclaim.sh` (cron 03:00), `calculate-cache-size.sh` (dynamic cache)

## Architecture Quick Reference

```
Client → dnsdist (:53, packet cache) → kresd (:5353 internal, DoT:853, DoH:443) → upstream
                  ↓ dnstap                             ↓ /metrics (JSON)
            dnstap-ingester → ClickHouse             backend (exporter) → Prometheus
                                                       ↓
            Caddy (HTTPS) → Frontend SPA → Backend API → Prometheus/ClickHouse
```

## Server Fleet

| Server | IP | Domain | Role |
|--------|----|--------|------|
| VM 216 | 103.186.204.216 | 216.afna.link | MASTER (code origin, push to git) |
| VM 212 | 103.186.204.212 | 212.afna.link | REPLICA (git pull + update.sh) |
| VM 238 | 103.138.53.238 | dns.afna.link | REPLICA (git pull + update.sh) |

Other replicas pull from git and run `./update.sh`.

## Critical Dependencies (Startup Order)

1. clickhouse, redis, postgres (infra, health checks)
2. dnstap-ingester (MUST start before kresd — creates Unix socket)
3. kresd (connects to dnstap socket, internal port 5353)
4. dnsdist (packet cache proxy on port 53, depends on kresd)
5. node-exporter, prometheus
6. backend (depends on clickhouse, redis, postgres)
7. frontend (depends on backend)
8. caddy (depends on frontend, backend)

## Known Issues & Context

- **RPZ load time**: ~3 minutes on cold start (18M entries). **Mitigated** by dnsdist packet cache — cached domains still served during restart
- **RPZ memory spike**: Policy-loader peaks ~3.6GB during load. **Swap 4GB required** on servers with <= 11GB RAM (see dns.md Section 26)
- **Ruledb persistent across restart**: `kresd.lua.j2` uses `kr_rules_init(false)` — ruledb survives container restart. Only lost on container recreate (`docker compose rm`)
- **RPZ-aware cache sizing**: Backend `resolveCacheSize()` reduces cache when RPZ enabled: `RAM - 4GB ruledb - 2GB reserve`. Handles `CACHE_SIZE=auto` via `calculate-cache-size.sh`
- **dnsdist limitation**: Only cached domains served during kresd downtime; new/uncached domains timeout. DoT/DoH not proxied through dnsdist
- **Disk space**: Critical on 50GB servers. disk-reclaim.sh runs daily at 03:00. Emergency at 85%, early warning at 75%
- **iptables not persistent by default**: Already saved to /etc/iptables/rules.v4 via iptables-persistent
- **Admin password**: Already changed from default

## Swap Status Fleet

| Server | Swap | Status |
|--------|------|--------|
| VM 216 | TBD | Perlu setup |
| VM 212 | TBD | Perlu setup |
| VM 238 | 4 GB | Aktif |

## Tech Stack

Go 1.23 (backend + dnstap-ingester), SolidJS + Vite (frontend), uPlot (charts), Caddy (HTTPS), dnsdist 1.9 (packet cache), Prometheus 2.55, ClickHouse 24.12, PostgreSQL 17, Redis 8, Docker Compose

## Operational Commands

```bash
# Full restart (respect order)
cd /root/knot-dns-monitor
docker compose stop kresd dnstap-ingester
docker compose up -d dnstap-ingester && sleep 2 && docker compose up -d kresd

# Rebuild after code change
docker compose build backend && docker compose up -d backend

# Check health
curl -s http://127.0.0.1:8080/api/health
dig @127.0.0.1 google.com +short

# Disk reclaim (manual)
./disk-reclaim.sh
```

## Sensitive Files (never commit)

- `secrets/pg_password.txt`
- `secrets/jwt_secret.txt`
- `.env`
- `config/kresd/tls/server.key`
