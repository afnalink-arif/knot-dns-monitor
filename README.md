# Knot DNS Manager

**Production-grade DNS resolver with real-time monitoring dashboard.**

Built on [Knot Resolver 6.2](https://www.knot-resolver.cz/) with a full observability stack — query logging, metrics, alerting, and a modern web UI.

```
         Client
           |
     DNS / DoT / DoH
           |
   +-------v--------+
   |  Knot Resolver  |  DNSSEC validated, 28k+ QPS capacity
   +---+----+---+----+
       |    |   |
    dnstap  | RPZ (shared LMDB)    <-- domain filtering at scale
       |    |   |
       |  metrics & custom filters
       v    v
  ClickHouse  Prometheus    <-- query logs + time-series
       |        |
       +---+----+
           |
       Go Backend           <-- REST API + WebSocket + DNS tools
           |
     SolidJS Dashboard      <-- real-time charts, filtering & lookup
           |
         Caddy              <-- HTTPS + auto Let's Encrypt
```

---

## Features

- **DNS Resolver** — Knot Resolver 6.2 with DNSSEC validation, DNS-over-TLS (853), DNS-over-HTTPS (443)
- **Query Logging** — Every DNS query captured via dnstap, stored in ClickHouse (30-day retention)
- **Real-time Dashboard** — Live QPS, latency, cache hit ratio, DNSSEC stats via WebSocket
- **Query Search** — Filter by domain, client IP, query type, response code, protocol
- **DNS Filtering** — Custom domain block rules with categories, bulk import from URL lists, and built-in RPZ support for large-scale government blocklists
- **RPZ Support** — Native [Response Policy Zone](https://datatracker.ietf.org/doc/html/draft-vixie-dnsop-dns-rpz-00) integration via AXFR zone transfer, loaded into shared LMDB ruledb (~180 MB for 17.5M+ domains). Ships with built-in support for Indonesia's Komdigi Trust Positif, but the RPZ engine works with any standard RPZ provider
- **Block Page** — Customizable glassmorphism block page shown to users visiting blocked domains, with editable branding, text, and colors
- **DNS Lookup Tool** — Test DNS resolution from dashboard against local resolver or external DNS (Google, Cloudflare, Quad9), with block detection and comparison
- **System Metrics** — CPU, RAM, disk, network monitoring via Prometheus + node_exporter
- **Alerting** — Configurable alert rules with history tracking
- **Cluster Management** — Multi-node controller/agent architecture with centralized monitoring and remote updates
- **Multi-server Deploy** — Interactive installer with template-based config per server
- **Security** — JWT auth, ACL-based DNS access, firewall rules, anti-amplification

## Tech Stack

| Layer | Technology |
|-------|-----------|
| DNS Resolver | Knot Resolver 6.2 |
| Backend API | Go 1.23 |
| Frontend | SolidJS + Tailwind CSS + uPlot |
| Query Logs | ClickHouse 24.12 |
| Metrics | Prometheus 2.55 + node_exporter |
| App Database | PostgreSQL 17 |
| Cache | Redis 8 |
| HTTPS Proxy | Caddy 2 (auto Let's Encrypt) |
| Container | Docker Compose (10 services) |

## Quick Start

### Prerequisites

| Requirement | Minimum |
|-------------|---------|
| OS | Debian 12+ / Ubuntu 22.04+ |
| CPU | 4 cores |
| RAM | 4 GB (8 GB+ recommended, 4 GB sufficient with RPZ) |
| Disk | 30 GB SSD (RPZ zone file ~1 GB + ruledb ~2 GB) |
| Docker | 24+ with Compose v2 |
| Ports | 53, 80, 443, 853 open |
| Domain | Pointed to server IP |

### Install

```bash
# Install Docker if needed
curl -fsSL https://get.docker.com | sh

# Clone and install
git clone https://github.com/afnalink-arif/knot-dns-manager.git /root/knot-dns-manager
cd /root/knot-dns-manager
./install.sh
```

The installer will ask for:
1. **Server IP** — auto-detected
2. **Domain** — for HTTPS dashboard (e.g. `dns.example.com`)
3. **Allowed subnets** — CIDRs permitted to query (e.g. `10.0.0.0/24,192.168.1.0/24`)
4. **Cache size** — auto-calculated (~70% RAM, max 8G)

Or run non-interactively:

```bash
./install.sh --ip 10.0.0.1 --domain dns.example.com --subnets "10.0.0.0/24" --cache 4G
```

To reinstall from scratch (removes all data and configs):

```bash
./install.sh --reinstall
```

### Post-Install

```bash
# Test DNS resolution
dig @127.0.0.1 google.com +short

# Open dashboard
# https://your-domain.com
# Default login: admin / KnotDNS@2026!
# >>> Change the password immediately <<<
```

## Architecture

```
knot-dns-manager/
├── install.sh                  # Interactive installer
├── update.sh                   # Update & rebuild script
├── docker-compose.yml          # 10-service stack
├── config/
│   ├── Caddyfile.template      # HTTPS reverse proxy template
│   ├── kresd/
│   │   ├── config.yaml.template  # Resolver config template (YAML + RPZ)
│   │   ├── rpz.zone             # RPZ zone file (Komdigi Trust Positif)
│   │   └── tls/                  # DoT/DoH certificates
│   ├── clickhouse/
│   │   └── init.sql              # Tables + materialized views
│   └── prometheus/
│       └── prometheus.yml
├── services/
│   ├── backend/                # Go API server
│   ├── frontend/               # SolidJS dashboard
│   ├── dnstap-ingester/        # Go dnstap-to-ClickHouse bridge
│   └── kresd/                  # Custom Knot Resolver image
└── .env.example
```

### Services (10 containers)

| Service | Image | Port | Role |
|---------|-------|------|------|
| kresd | cznic/knot-resolver | 53, 443, 853 | DNS resolver + RPZ filtering |
| backend | custom (Go) | 8080 | REST API + WebSocket + DNS tools |
| frontend | custom (SolidJS) | 3000 | Dashboard UI |
| dnstap-ingester | custom (Go) | - | Query log pipeline |
| caddy | caddy:2-alpine | 80, 443 | HTTPS reverse proxy + block page |
| prometheus | prom/prometheus | 9090 | Metrics storage |
| node-exporter | prom/node-exporter | 9100 | Host metrics |
| clickhouse | clickhouse-server | 8123 | Query log storage |
| postgres | postgres:17 | 5432 | Users, alerts, filters, RPZ config |
| redis | redis:8 | 6379 | Cache & sessions |

## Update

Updates can be triggered directly from the dashboard or via CLI.

### From Dashboard

Go to **Admin > Update** in the dashboard:
- **Check for updates** — compares local version with latest remote
- **Execute update** — pulls code, rebuilds, and restarts with live progress via SSE stream
- **Cluster update** — push updates to all connected nodes from the controller dashboard (Admin > Cluster > Update All)

### From CLI

```bash
cd /root/knot-dns-manager
./update.sh
```

This pulls the latest code, regenerates configs from templates, rebuilds custom images, and does a rolling restart with health checks.

### Multi-server update (CLI)

```bash
SERVERS=("root@10.0.0.1" "root@10.0.0.2" "root@10.0.0.3")
for srv in "${SERVERS[@]}"; do
  ssh "$srv" "cd /root/knot-dns-manager && ./update.sh"
done
```

> For managed clusters, use the dashboard's cluster update feature instead — it handles node-by-node updates with status tracking.

## DNS Filtering & RPZ

### Custom Filters

Add domain block rules from the dashboard (Admin > DNS Filtering):
- Block individual domains or import from URL-based blocklists (e.g. StevenBlack, OISD)
- Blocked domains redirect to a customizable block page
- Categories: ads, malware, adult, gambling, custom

### Response Policy Zone (RPZ)

[RPZ](https://datatracker.ietf.org/doc/html/draft-vixie-dnsop-dns-rpz-00) is a DNS-level filtering mechanism that allows a resolver to override responses for specific domains — commonly used by ISPs, enterprises, and governments to enforce content policies at scale. Instead of maintaining blocklists in application code, RPZ distributes them as standard DNS zone files via AXFR zone transfer.

This project uses kresd 6.2's native `local-data.rpz` YAML integration:

- **Shared LMDB ruledb** — the zone is parsed once by kresd's `policy-loader` process and stored in a memory-mapped LMDB database. All worker processes share this single database, eliminating redundant copies
- **Memory efficient** — ~180 MB total RAM for 17.5M+ domains (vs 2-5 GB with per-worker in-memory trie approaches)
- **Full worker utilization** — no need to limit CPU cores; all workers read from the same shared ruledb
- **Strict zone sanitization** — automatically strips invalid domain names (non-ASCII, RFC-violating labels), unsupported record types (NS), and normalizes CNAME targets to standard NXDOMAIN format
- **Enable/disable from dashboard** — toggle RPZ on or off, trigger sync, view stats (domain count, zone size, kresd memory)

#### Built-in: Komdigi Trust Positif (Indonesia)

Ships with pre-configured support for **Komdigi Trust Positif**, Indonesia's national DNS blocklist managed by the Ministry of Communication and Digital ([Komdigi](https://komdigi.go.id/)). This is one of the largest government RPZ feeds in the world:

- **17.5 million+ blocked domains** — gambling, pornography, fraud, and other categories regulated under Indonesian law
- **3 master servers** — `139.255.196.202`, `182.23.79.202`, `103.154.123.130`
- **AXFR zone transfer** — full zone (~1.4 GB raw, ~1 GB after sanitization) downloaded and converted automatically
- **Registration required** — server IP must be registered at [s.komdigi.go.id/FormKoneksiRPZ](https://s.komdigi.go.id/FormKoneksiRPZ) before zone transfers will work

> The RPZ engine itself is provider-agnostic. While Komdigi is the default, the same mechanism works with any RPZ feed that serves zones via AXFR (e.g. Spamhaus, SURBL, or your own internal zones).

### Block Page

Visitors to blocked domains see a customizable block page:
- Glassmorphism design with configurable branding
- Editable title, subtitle, message, contact info, colors
- Shows the blocked domain name
- Served directly by the backend (no external dependencies)

### DNS Lookup Tool

Test DNS resolution from the dashboard without SSH (Admin > DNS Lookup):
- Query local resolver (kresd) or external DNS (Google, Cloudflare, Quad9)
- Supports A, AAAA, CNAME, MX, NS, TXT, SOA, SRV, PTR record types
- Auto-detects if a domain is blocked (by custom filter or RPZ)
- Shows raw dig output and parsed records

## API Endpoints

<details>
<summary>Click to expand full API reference</summary>

### Public

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Health check |
| POST | `/api/auth/login` | Login (returns JWT) |
| POST | `/api/auth/register` | Register user |
| GET | `/api/auth/check` | Check if users exist |

### Protected (JWT required)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/metrics/overview` | QPS, latency, cache, DNSSEC KPIs |
| GET | `/api/metrics/qps` | QPS time-series |
| GET | `/api/metrics/latency` | Latency percentiles |
| GET | `/api/metrics/cache` | Cache hit/miss ratio |
| GET | `/api/metrics/system` | CPU, RAM, disk, network |
| GET | `/api/queries` | Search query logs |
| GET | `/api/queries/top-domains` | Top queried domains |
| GET | `/api/queries/timeline` | Query count per minute |
| GET | `/api/alerts` | List alert rules |
| POST | `/api/alerts` | Create alert rule |
| WS | `/api/ws/live` | Real-time metrics stream |

### Admin (JWT + admin role)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/admin/dns/lookup` | DNS lookup tool |
| GET | `/api/admin/rpz/config` | Get RPZ config |
| PUT | `/api/admin/rpz/config` | Update RPZ config (enable/disable) |
| POST | `/api/admin/rpz/sync` | Trigger AXFR zone sync (SSE stream) |
| GET | `/api/admin/rpz/stats` | RPZ stats (domain count, memory) |
| GET | `/api/admin/blockpage/config` | Get block page config |
| PUT | `/api/admin/blockpage/config` | Update block page config |
| GET | `/api/admin/filters` | List custom filter rules |
| POST | `/api/admin/filters` | Add filter rule |
| DELETE | `/api/admin/filters/{id}` | Delete filter rule |
| POST | `/api/admin/filters/import` | Import blocklist from URL |
| POST | `/api/admin/filters/apply` | Apply filters to resolver |
| GET | `/api/admin/services` | List service status |
| POST | `/api/admin/services/restart` | Restart a service |

### Cluster (admin or agent token)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/cluster/config` | Get cluster config |
| GET | `/api/cluster/nodes` | List cluster nodes |
| POST | `/api/cluster/nodes` | Add node |
| GET | `/api/cluster/overview` | Aggregated cluster metrics |

</details>

## Benchmark

Tested on Intel Xeon E5-2683 v4 (8 cores), 11 GB RAM:

| Scenario | QPS | Packet Loss | Avg Latency |
|----------|-----|-------------|-------------|
| Target load (60s) | **12,172** | 0% | 1.07 ms |
| Max capacity (60s) | **28,671** | 0% | 3.36 ms |

1.7 million queries in 60 seconds with zero packet loss.

## Security

- **DNS ACL** — Only configured subnets can query; all others get REFUSED
- **DNS Filtering** — RPZ + custom rules block malicious/unwanted domains at the resolver level
- **Firewall** — iptables rules drop DNS packets from unauthorized sources
- **Anti-amplification** — Tested against real attack (82.9% attack traffic blocked after ACL)
- **JWT Authentication** — Dashboard protected with token-based auth, role-based access (admin/viewer)
- **Cluster Auth** — Machine-to-machine communication secured with per-node API tokens
- **Secrets Management** — Passwords and keys stored in Docker secrets, never in images
- **Input Validation** — DNS lookup tool validates domain names and restricts query types

> Sensitive files (`secrets/`, `.env`, TLS keys) are excluded from git via `.gitignore`.

## License

[MIT License](LICENSE)
