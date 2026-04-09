import { createResource, createSignal, Show, For } from "solid-js";
import Layout from "~/components/Layout";
import KPICard from "~/components/KPICard";
import { clusterAPI } from "~/lib/api";
import { authHeaders } from "~/lib/auth";
import { extractValue, fmt } from "~/lib/prometheus";

export default function ClusterPage() {
  const [overview, { refetch }] = createResource(() => clusterAPI.getOverview());
  const [updatingNode, setUpdatingNode] = createSignal<number | null>(null);
  const [updateOutput, setUpdateOutput] = createSignal<string[]>([]);
  const [updateDone, setUpdateDone] = createSignal(false);

  const onlineCount = () => {
    const o = overview();
    if (!o) return 0;
    return o.nodes.filter((n: any) => n.status === "online" || n.status === "degraded").length;
  };

  const totalNodes = () => overview()?.node_count || 0;

  const aggregateMetric = (key: string): number | null => {
    const o = overview();
    if (!o || o.nodes.length === 0) return null;
    let sum = 0;
    let count = 0;
    for (const n of o.nodes) {
      if (!n.metrics) continue;
      const v = extractValue(n.metrics[key]);
      if (v !== null && !isNaN(v)) {
        sum += v;
        count++;
      }
    }
    return count > 0 ? sum : null;
  };

  const avgMetric = (key: string): number | null => {
    const o = overview();
    if (!o || o.nodes.length === 0) return null;
    let sum = 0;
    let count = 0;
    for (const n of o.nodes) {
      if (!n.metrics) continue;
      const v = extractValue(n.metrics[key]);
      if (v !== null && !isNaN(v)) {
        sum += v;
        count++;
      }
    }
    return count > 0 ? sum / count : null;
  };

  const statusColor = (status: string) => {
    switch (status) {
      case "online": return "bg-emerald-500";
      case "degraded": return "bg-amber-500";
      case "offline": return "bg-red-500";
      default: return "bg-slate-500";
    }
  };

  const timeSince = (date: string | null) => {
    if (!date) return "Never";
    const diff = Date.now() - new Date(date).getTime();
    if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`;
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    return `${Math.floor(diff / 3600000)}h ago`;
  };

  const handleNodeUpdate = async (nodeId: number, isLocal: boolean) => {
    setUpdatingNode(nodeId);
    setUpdateOutput([]);
    setUpdateDone(false);

    const url = isLocal
      ? "/api/admin/update/execute"
      : `/api/cluster/nodes/${nodeId}/update`;

    try {
      const res = await fetch(url, { method: "POST", headers: authHeaders() });
      if (!res.ok) {
        setUpdateOutput(["[ERROR] Failed to start update"]);
        setUpdatingNode(null);
        return;
      }
      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) {
          if (line.startsWith("data: ")) setUpdateOutput((prev) => [...prev, line.slice(6)]);
          else if (line.startsWith("event: done")) { setUpdateDone(true); setUpdatingNode(null); }
        }
      }
      if (!updateDone()) { setUpdateDone(true); setUpdatingNode(null); }
    } catch {
      setUpdateOutput((prev) => [...prev, "[ERROR] Connection failed"]);
      setUpdatingNode(null);
    }
    refetch();
  };

  // Auto-refresh every 15s
  setInterval(() => refetch(), 15000);

  return (
    <Layout>
      <div class="space-y-6">
        <div class="flex items-center justify-between">
          <div>
            <h1 class="text-2xl font-bold text-white">Cluster Overview</h1>
            <p class="text-sm text-slate-400 mt-1">Monitoring all DNS nodes</p>
          </div>
          <span class="text-sm text-slate-500">{totalNodes()} nodes</span>
        </div>

        {/* Aggregated KPIs */}
        <div class="grid grid-cols-1 md:grid-cols-4 gap-4">
          <KPICard
            title="Nodes Online"
            value={`${onlineCount()} / ${totalNodes()}`}
            subtitle="Active nodes in cluster"
            color={onlineCount() === totalNodes() ? "#22c55e" : "#eab308"}
          />
          <KPICard
            title="Total QPS"
            value={aggregateMetric("qps") !== null ? fmt(aggregateMetric("qps")!, 1) : "--"}
            subtitle="Queries/sec across all nodes"
            color="#3b82f6"
          />
          <KPICard
            title="Avg Cache Hit"
            value={avgMetric("cache_hit_ratio") !== null ? fmt(avgMetric("cache_hit_ratio")! * 100, 1) + "%" : "--"}
            subtitle="Average cache hit ratio"
            color="#22c55e"
          />
          <KPICard
            title="Avg Latency"
            value={avgMetric("avg_latency_ms") !== null ? fmt(avgMetric("avg_latency_ms")!, 1) + "ms" : "--"}
            subtitle="Average query latency"
            color="#a855f7"
          />
        </div>

        {/* Update output */}
        <Show when={updateOutput().length > 0}>
          <div class="bg-slate-950 rounded-xl p-4 font-mono text-[11px] leading-5 max-h-64 overflow-y-auto border border-slate-700/50"
            ref={(el) => {
              const observer = new MutationObserver(() => { el.scrollTop = el.scrollHeight; });
              observer.observe(el, { childList: true, subtree: true });
            }}>
            <For each={updateOutput()}>
              {(line) => (
                <div class={
                  line.includes("[OK]") ? "text-emerald-400" :
                  line.includes("[WARN]") ? "text-amber-400" :
                  line.includes("[ERROR]") ? "text-red-400" :
                  line.includes("[INFO]") ? "text-blue-400" :
                  line.startsWith("===") ? "text-white font-bold mt-2" :
                  "text-slate-400"
                }>{line}</div>
              )}
            </For>
            <Show when={updatingNode() !== null}>
              <div class="text-amber-400 animate-pulse mt-1">Updating...</div>
            </Show>
          </div>
        </Show>

        {/* Node Grid */}
        <Show
          when={overview() && overview()!.nodes.length > 0}
          fallback={
            <div class="bg-slate-800 rounded-xl p-8 border border-slate-700 text-center">
              <p class="text-slate-400">No nodes registered yet.</p>
              <p class="text-sm text-slate-500 mt-1">Go to Settings → Cluster to add agent nodes.</p>
            </div>
          }
        >
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            <For each={overview()!.nodes}>
              {(node: any) => {
                const nodeQps = () => node.metrics ? extractValue(node.metrics.qps) : null;
                const nodeCacheHit = () => node.metrics ? extractValue(node.metrics.cache_hit_ratio) : null;
                const nodeLatency = () => node.metrics ? extractValue(node.metrics.avg_latency_ms) : null;
                const isUpdating = () => updatingNode() === node.id;

                return (
                  <div class={`bg-slate-800 rounded-xl p-5 border transition-colors ${
                    node.is_local ? "border-blue-500/30" : "border-slate-700 hover:border-slate-600"
                  }`}>
                    {/* Header */}
                    <div class="flex items-center justify-between mb-4">
                      <div class="flex items-center gap-3">
                        <span class={`w-2.5 h-2.5 rounded-full ${statusColor(node.status)} ${node.status === "online" ? "animate-pulse" : ""}`} />
                        <div>
                          <div class="flex items-center gap-2">
                            <h3 class="text-white font-medium text-sm">{node.name || node.domain}</h3>
                            <Show when={node.is_local}>
                              <span class="px-1.5 py-0.5 bg-blue-500/15 text-blue-400 text-[9px] font-medium rounded">LOCAL</span>
                            </Show>
                          </div>
                          <p class="text-[10px] text-slate-500">{node.domain || "localhost"}</p>
                        </div>
                      </div>
                    </div>

                    {/* Version badge */}
                    <div class="mb-4">
                      <Show when={node.version} fallback={
                        <span class="text-[10px] text-slate-600">Version unknown</span>
                      }>
                        <span class="inline-flex items-center gap-1.5 px-2 py-1 bg-slate-700/50 rounded text-[11px] font-mono text-slate-300">
                          <svg class="w-3 h-3 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z" />
                          </svg>
                          v{node.version}
                        </span>
                      </Show>
                    </div>

                    {/* Metrics */}
                    <div class="grid grid-cols-3 gap-3">
                      <div>
                        <p class="text-[10px] text-slate-500">QPS</p>
                        <p class="text-lg font-medium text-white">{nodeQps() !== null ? fmt(nodeQps()!, 1) : "--"}</p>
                      </div>
                      <div>
                        <p class="text-[10px] text-slate-500">Cache</p>
                        <p class="text-lg font-medium text-emerald-400">
                          {nodeCacheHit() !== null ? fmt(nodeCacheHit()! * 100, 1) + "%" : "--"}
                        </p>
                      </div>
                      <div>
                        <p class="text-[10px] text-slate-500">Latency</p>
                        <p class="text-lg font-medium text-purple-400">
                          {nodeLatency() !== null ? fmt(nodeLatency()!, 1) + "ms" : "--"}
                        </p>
                      </div>
                    </div>

                    {/* Footer: status + actions */}
                    <div class="mt-3 pt-3 border-t border-slate-700 flex items-center justify-between">
                      <div class="flex items-center gap-2">
                        <span class={`text-xs capitalize ${
                          node.status === "online" ? "text-emerald-400" :
                          node.status === "degraded" ? "text-amber-400" :
                          node.status === "offline" ? "text-red-400" : "text-slate-400"
                        }`}>{node.status}</span>
                        <span class="text-[10px] text-slate-600">{timeSince(node.last_seen_at)}</span>
                      </div>
                      <div class="flex gap-1.5">
                        <button
                          onClick={() => handleNodeUpdate(node.id, node.is_local)}
                          disabled={updatingNode() !== null}
                          class="px-2 py-1 text-[10px] bg-amber-500/10 text-amber-400 rounded hover:bg-amber-500/20 transition-colors disabled:opacity-50"
                        >
                          {isUpdating() ? "..." : "Update"}
                        </button>
                        <button
                          onClick={() => handleNodeUpdate(node.id, node.is_local)}
                          disabled={updatingNode() !== null}
                          class="px-2 py-1 text-[10px] bg-slate-700 text-slate-400 rounded hover:bg-slate-600 transition-colors disabled:opacity-50"
                        >
                          {isUpdating() ? "..." : "Rebuild"}
                        </button>
                      </div>
                    </div>

                    <Show when={node.last_error}>
                      <div class="mt-2 p-2 bg-red-500/10 rounded text-[10px] text-red-400 truncate" title={node.last_error}>
                        {node.last_error}
                      </div>
                    </Show>
                  </div>
                );
              }}
            </For>
          </div>
        </Show>
      </div>
    </Layout>
  );
}
