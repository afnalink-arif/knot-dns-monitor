# CLAUDE.md — Project Memory for knot-dns-monitor

## Project Overview

Knot Resolver (kresd) 6.2 management system with monitoring dashboard. Full recursive DNS resolver with DNSSEC, DoT, DoH, RPZ filtering (Komdigi TrustPositif), and real-time monitoring.

## Key Files

- **Documentation**: `/root/dns.md` — comprehensive ops doc (25 sections)
- **Project dir**: `/root/knot-dns-monitor/`
- **docker-compose.yml**: 11 services (dnsdist, kresd, dnstap-ingester, prometheus, node-exporter, clickhouse, redis, postgres, backend, frontend, caddy)
- **Config templates**: `config/Caddyfile.template`, `config/kresd/config.yaml.template`
- **RPZ blocklist**: `config/kresd/rpz.zone` (~17.7M entries, Komdigi TrustPositif)
- **LMDB overrides**: `config/kresd/policy-loader.lua.j2`, `config/kresd/kresd.lua.j2` (6GB ruledb mapsize)
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
| VM 212 | 103.186.204.212 | _TODO_ | REPLICA (git pull + update.sh) |

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

- **RPZ load time**: ~38 seconds on cold start (17.7M entries). **Mitigated** by dnsdist packet cache — cached domains still served during restart
- **Ruledb in tmpfs**: Lost on restart, must rebuild from rpz.zone (~38s)
- **dnsdist limitation**: Only cached domains served during kresd downtime; new/uncached domains timeout. DoT/DoH not proxied through dnsdist
- **Disk space**: Critical on 50GB servers. disk-reclaim.sh runs daily at 03:00. Emergency at 85%, early warning at 75%
- **iptables not persistent by default**: Already saved to /etc/iptables/rules.v4 via iptables-persistent
- **Admin password**: Already changed from default

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
