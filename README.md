<p align="center">
  <img src="https://raw.githubusercontent.com/afnalink-arif/knot-dns-manager/main/.github/logo.svg" width="80" alt="Knot DNS Manager" />
</p>

<h1 align="center">Knot DNS Manager</h1>

<p align="center">
  <strong>DNS resolver management with real-time monitoring, filtering & analytics.</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/version-1.1.0-blue?style=flat-square" alt="Version" />
  <img src="https://img.shields.io/badge/license-MIT-green?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/knot_resolver-6.2-orange?style=flat-square" alt="Knot Resolver" />
  <img src="https://img.shields.io/badge/docker-10_services-2496ED?style=flat-square&logo=docker&logoColor=white" alt="Docker" />
</p>

<p align="center">
  <img src="https://raw.githubusercontent.com/afnalink-arif/knot-dns-manager/main/.github/Knot-DNS-Manager-Overview.png" width="800" alt="Dashboard Overview" />
</p>

<p align="center">
  <img src="https://raw.githubusercontent.com/afnalink-arif/knot-dns-manager/main/.github/Knot-DNS-Manager.png" width="800" alt="DNS Filtering" />
</p>

---

## Features

- **High-Performance DNS** — Knot Resolver 6.2 with DNSSEC, DoT, DoH, 28k+ QPS capacity
- **Real-time Dashboard** — Live QPS, latency, cache hit ratio, and system metrics via WebSocket
- **Query Logging** — Full dnstap capture stored in ClickHouse with search & analytics
- **DNS Filtering** — Custom block rules, bulk import, RPZ support for large-scale blocklists (17M+ domains)
- **DNS Lookup Tool** — Test resolution against local or external DNS, with block detection
- **Alerting** — Configurable threshold alerts with history tracking
- **Cluster Mode** — Multi-node controller/agent architecture with centralized updates
- **Bilingual UI** — English & Indonesian, switchable from the sidebar

## Quick Start

```bash
curl -fsSL https://get.docker.com | sh
git clone https://github.com/afnalink-arif/knot-dns-manager.git
cd knot-dns-manager && ./install.sh
```

The installer handles everything — server detection, config generation, TLS setup, and service deployment.

```bash
# Verify
dig @127.0.0.1 google.com +short

# Dashboard: https://your-domain.com
```

## Tech Stack

| Component | Technology |
|-----------|-----------|
| DNS Resolver | Knot Resolver 6.2 |
| Backend | Go 1.23 |
| Frontend | SolidJS + Tailwind CSS |
| Query Logs | ClickHouse |
| Metrics | Prometheus + node_exporter |
| Database | PostgreSQL 17 |
| Cache | Redis 8 |
| Proxy | Caddy 2 (auto HTTPS) |

## Update

From the dashboard: **Settings → Update & Services → Check for Updates**

Or via CLI:

```bash
./update.sh
```

## License

[MIT](LICENSE) — © 2026 Afnalink
