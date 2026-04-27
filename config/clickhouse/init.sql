CREATE DATABASE IF NOT EXISTS dnsmonitor;

-- Main query log table
CREATE TABLE IF NOT EXISTS dnsmonitor.dns_queries (
    timestamp     DateTime64(3),
    client_ip     IPv6,
    qname         String,
    qtype         UInt16,
    qclass        UInt16 DEFAULT 1,
    rcode         UInt8,
    latency_us    UInt32,
    protocol      Enum8('udp'=0, 'tcp'=1, 'dot'=2, 'doh'=3, 'doq'=4),
    dnssec_status Enum8('unknown'=0, 'secure'=1, 'insecure'=2, 'bogus'=3, 'indeterminate'=4),
    upstream_ip   IPv6,
    cached        Bool,
    response_size UInt32 DEFAULT 0
) ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (timestamp, qname)
TTL toDate(timestamp) + INTERVAL 14 DAY
SETTINGS index_granularity = 8192;

-- Materialized view: per-minute aggregation for fast dashboard queries
CREATE TABLE IF NOT EXISTS dnsmonitor.dns_queries_1m (
    timestamp     DateTime,
    qtype         UInt16,
    rcode         UInt8,
    protocol      Enum8('udp'=0, 'tcp'=1, 'dot'=2, 'doh'=3, 'doq'=4),
    dnssec_status Enum8('unknown'=0, 'secure'=1, 'insecure'=2, 'bogus'=3, 'indeterminate'=4),
    cached        Bool,
    query_count   UInt64,
    avg_latency   Float64,
    p95_latency   Float64,
    p99_latency   Float64
) ENGINE = SummingMergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (timestamp, qtype, rcode, protocol, dnssec_status, cached)
TTL toDate(timestamp) + INTERVAL 90 DAY;

CREATE MATERIALIZED VIEW IF NOT EXISTS dnsmonitor.dns_queries_1m_mv
TO dnsmonitor.dns_queries_1m
AS SELECT
    toStartOfMinute(timestamp) AS timestamp,
    qtype,
    rcode,
    protocol,
    dnssec_status,
    cached,
    count() AS query_count,
    avg(latency_us) AS avg_latency,
    quantile(0.95)(latency_us) AS p95_latency,
    quantile(0.99)(latency_us) AS p99_latency
FROM dnsmonitor.dns_queries
GROUP BY timestamp, qtype, rcode, protocol, dnssec_status, cached;

-- Materialized view: top domains per hour
CREATE TABLE IF NOT EXISTS dnsmonitor.top_domains_1h (
    timestamp   DateTime,
    qname       String,
    query_count UInt64
) ENGINE = SummingMergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (timestamp, qname)
TTL toDate(timestamp) + INTERVAL 90 DAY;

CREATE MATERIALIZED VIEW IF NOT EXISTS dnsmonitor.top_domains_1h_mv
TO dnsmonitor.top_domains_1h
AS SELECT
    toStartOfHour(timestamp) AS timestamp,
    qname,
    count() AS query_count
FROM dnsmonitor.dns_queries
GROUP BY timestamp, qname;
