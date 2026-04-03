import { authHeaders, logout } from "./auth";

const API_BASE = import.meta.env.VITE_API_URL || "";

async function fetchAPI<T>(path: string, params?: Record<string, string>): Promise<T> {
  const url = new URL(`${API_BASE}${path}`, window.location.origin);
  if (params) {
    Object.entries(params).forEach(([k, v]) => url.searchParams.set(k, v));
  }

  const res = await fetch(url.toString(), {
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
    },
  });

  if (res.status === 401) {
    logout();
    throw new Error("Session expired");
  }

  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`);
  }

  return res.json();
}

// Metrics API
export const metricsAPI = {
  overview: () => fetchAPI<any>("/api/metrics/overview"),
  qps: (params?: Record<string, string>) => fetchAPI<any>("/api/metrics/qps", params),
  latency: (params?: Record<string, string>) => fetchAPI<any>("/api/metrics/latency", params),
  cache: (params?: Record<string, string>) => fetchAPI<any>("/api/metrics/cache", params),
  dnssec: (params?: Record<string, string>) => fetchAPI<any>("/api/metrics/dnssec", params),
  system: (params?: Record<string, string>) => fetchAPI<any>("/api/metrics/system", params),
  upstreams: () => fetchAPI<any>("/api/metrics/upstreams"),
};

// Query Log API
export const queriesAPI = {
  search: (params?: Record<string, string>) =>
    fetchAPI<{ data: QueryLogEntry[]; limit: number; offset: number }>("/api/queries", params),
  topDomains: (params?: Record<string, string>) =>
    fetchAPI<TopDomain[]>("/api/queries/top-domains", params),
  typeDistribution: (params?: Record<string, string>) =>
    fetchAPI<Distribution[]>("/api/queries/type-distribution", params),
  rcodeDistribution: (params?: Record<string, string>) =>
    fetchAPI<Distribution[]>("/api/queries/rcode-distribution", params),
  protocolDistribution: (params?: Record<string, string>) =>
    fetchAPI<Distribution[]>("/api/queries/protocol-distribution", params),
  timeline: (params?: Record<string, string>) =>
    fetchAPI<TimelinePoint[]>("/api/queries/timeline", params),
};

// Alerts API
export const alertsAPI = {
  list: () => fetchAPI<AlertRule[]>("/api/alerts"),
  create: (rule: Partial<AlertRule>) =>
    fetch(`/api/alerts`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify(rule),
    }).then((r) => r.json()),
  update: (id: number, rule: Partial<AlertRule>) =>
    fetch(`/api/alerts/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify(rule),
    }).then((r) => r.json()),
  delete: (id: number) =>
    fetch(`/api/alerts/${id}`, { method: "DELETE", headers: authHeaders() }).then((r) => r.json()),
  history: (params?: Record<string, string>) =>
    fetchAPI<AlertEvent[]>("/api/alerts/history", params),
};

// Health API
export const healthAPI = {
  check: () => fetchAPI<HealthStatus>("/api/health"),
};

// Version API
export const versionAPI = {
  get: () => fetchAPI<{ version: string }>("/api/version"),
};

// Admin Update API
export const updateAPI = {
  check: () => fetchAPI<UpdateCheckResult>("/api/admin/update/check"),
  status: () => fetchAPI<{ in_progress: boolean }>("/api/admin/update/status"),
};

export interface UpdateCheckResult {
  current_version: string;
  current_commit: string;
  latest_commit: string;
  update_available: boolean;
  commits_behind: number;
  commit_log: string[];
}

// Resolver info API
export const resolverAPI = {
  info: () => fetchAPI<ResolverInfo>("/api/resolver/info"),
};

export interface ResolverInfo {
  config: any;
  cache: { storage: string; "size-max": string };
  network: any;
  options: any;
  monitoring: any;
  workers: any;
  server: { hostname: string; cpus: number };
}

// Auth API (public, no auth header needed)
export const authAPI = {
  login: (username: string, password: string) =>
    fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    }).then(async (r) => {
      const data = await r.json();
      if (!r.ok) throw new Error(data.error || "Login failed");
      return data;
    }),
  register: (username: string, password: string) =>
    fetch("/api/auth/register", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify({ username, password }),
    }).then(async (r) => {
      const data = await r.json();
      if (!r.ok) throw new Error(data.error || "Registration failed");
      return data;
    }),
  check: () =>
    fetch("/api/auth/check").then((r) => r.json()) as Promise<{ has_users: boolean; setup_needed: boolean }>,
};

// Types
export interface QueryLogEntry {
  timestamp: string;
  client_ip: string;
  qname: string;
  qtype: number;
  rcode: number;
  latency_us: number;
  protocol: string;
  dnssec_status: string;
  upstream_ip: string;
  cached: boolean;
  response_size: number;
}

export interface TopDomain {
  qname: string;
  query_count: number;
}

export interface Distribution {
  label: string;
  count: number;
}

export interface TimelinePoint {
  timestamp: string;
  count: number;
  latency: number;
}

export interface AlertRule {
  id: number;
  name: string;
  metric: string;
  condition: string;
  threshold: number;
  duration_sec: number;
  enabled: boolean;
  notify_channels: string[];
  created_at: string;
  updated_at: string;
}

export interface AlertEvent {
  id: number;
  rule_id: number;
  rule_name: string;
  status: string;
  value: number;
  message: string;
  fired_at: string;
  resolved_at?: string;
}

export interface HealthStatus {
  status: string;
  checks: Record<string, string>;
}
