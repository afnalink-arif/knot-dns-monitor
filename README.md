# Knot DNS Monitor

**Production-grade DNS resolver with real-time monitoring dashboard.**

Built on [Knot Resolver 6.2](https://www.knot-resolver.cz/) with a full observability stack — query logging, metrics, alerting, and a modern web UI.

```
         Client
           |
     DNS / DoT / DoH
           |
   +-------v--------+
   |  Knot Resolver  |  DNSSEC validated, 28k+ QPS capacity
   +---+--------+----+
       |        |
    dnstap    metrics
       |        |
       v        v
  ClickHouse  Prometheus    <-- query logs + time-series
       |        |
       +---+----+
           |
       Go Backend           <-- REST API + WebSocket
           |
     SolidJS Dashboard      <-- real-time charts & search
           |
         Caddy              <-- HTTPS + auto Let's Encrypt
```

---

## Features

- **DNS Resolver** — Knot Resolver 6.2 with DNSSEC validation, DNS-over-TLS (853), DNS-over-HTTPS (443)
- **Query Logging** — Every DNS query captured via dnstap, stored in ClickHouse (30-day retention)
- **Real-time Dashboard** — Live QPS, latency, cache hit ratio, DNSSEC stats via WebSocket
- **Query Search** — Filter by domain, client IP, query type, response code, protocol
- **System Metrics** — CPU, RAM, disk, network monitoring via Prometheus + node_exporter
- **Alerting** — Configurable alert rules with history tracking
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
| RAM | 4 GB (recommended 8 GB+) |
| Disk | 20 GB SSD |
| Docker | 24+ with Compose v2 |
| Ports | 53, 80, 443, 853 open |
| Domain | Pointed to server IP |

### Install

```bash
# Install Docker if needed
curl -fsSL https://get.docker.com | sh

# Clone and install
git clone https://github.com/afnalink-arif/knot-dns-monitor.git /root/knot-dns-monitor
cd /root/knot-dns-monitor
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
knot-dns-monitor/
├── install.sh                  # Interactive installer
├── update.sh                   # Update & rebuild script
├── docker-compose.yml          # 10-service stack
├── config/
│   ├── Caddyfile.template      # HTTPS reverse proxy template
│   ├── kresd/
│   │   ├── config.yaml.template  # Resolver config template
│   │   └── kresd.conf            # Knot Resolver Lua config
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
| kresd | cznic/knot-resolver | 53, 853, 8853 | DNS resolver |
| backend | custom (Go) | 8080 | REST API + WebSocket |
| frontend | custom (SolidJS) | 3000 | Dashboard UI |
| dnstap-ingester | custom (Go) | - | Query log pipeline |
| caddy | caddy:2-alpine | 80, 443 | HTTPS reverse proxy |
| prometheus | prom/prometheus | 9090 | Metrics storage |
| node-exporter | prom/node-exporter | 9100 | Host metrics |
| clickhouse | clickhouse-server | 8123 | Query log storage |
| postgres | postgres:17 | 5432 | Users & alerts |
| redis | redis:8 | 6379 | Cache & sessions |

## Update

```bash
cd /root/knot-dns-monitor
./update.sh
```

This pulls the latest code, regenerates configs from templates, rebuilds custom images, and does a rolling restart with health checks.

### Multi-server update

```bash
SERVERS=("root@10.0.0.1" "root@10.0.0.2" "root@10.0.0.3")
for srv in "${SERVERS[@]}"; do
  ssh "$srv" "cd /root/knot-dns-monitor && ./update.sh"
done
```

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
- **Firewall** — iptables rules drop DNS packets from unauthorized sources
- **Anti-amplification** — Tested against real attack (82.9% attack traffic blocked after ACL)
- **JWT Authentication** — Dashboard protected with token-based auth
- **Secrets Management** — Passwords and keys stored in Docker secrets, never in images

> Sensitive files (`secrets/`, `.env`, TLS keys) are excluded from git via `.gitignore`.

## License

[MIT License](LICENSE)
