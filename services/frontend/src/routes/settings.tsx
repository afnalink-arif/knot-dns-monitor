import { createSignal, onMount, Show, For } from "solid-js";
import Layout from "~/components/Layout";
import { authHeaders, getToken, logout } from "~/lib/auth";
import { updateAPI, clusterAPI, type UpdateCheckResult, type ClusterConfig, type ClusterNode } from "~/lib/api";

type Tab = "account" | "users" | "cluster" | "system";

export default function SettingsPage() {
  const [activeTab, setActiveTab] = createSignal<Tab>("account");
  const [username, setUsername] = createSignal("");
  const [role, setRole] = createSignal("");

  // Change password
  const [oldPass, setOldPass] = createSignal("");
  const [newPass, setNewPass] = createSignal("");
  const [confirmPass, setConfirmPass] = createSignal("");
  const [passMsg, setPassMsg] = createSignal("");
  const [passError, setPassError] = createSignal(false);
  const [passLoading, setPassLoading] = createSignal(false);

  // Add user
  const [newUser, setNewUser] = createSignal("");
  const [newUserPass, setNewUserPass] = createSignal("");
  const [newUserRole, setNewUserRole] = createSignal("viewer");
  const [userMsg, setUserMsg] = createSignal("");
  const [userError, setUserError] = createSignal(false);

  // System update
  const [updateInfo, setUpdateInfo] = createSignal<UpdateCheckResult | null>(null);
  const [updateChecking, setUpdateChecking] = createSignal(false);
  const [updateRunning, setUpdateRunning] = createSignal(false);
  const [updateOutput, setUpdateOutput] = createSignal<string[]>([]);
  const [updateDone, setUpdateDone] = createSignal(false);
  const [updateError, setUpdateError] = createSignal("");
  const [restarting, setRestarting] = createSignal(false);

  // Cluster
  const [clusterConfig, setClusterConfig] = createSignal<ClusterConfig | null>(null);
  const [clusterNodes, setClusterNodes] = createSignal<ClusterNode[]>([]);
  const [clusterMsg, setClusterMsg] = createSignal("");
  const [clusterError, setClusterError] = createSignal(false);
  const [newNodeName, setNewNodeName] = createSignal("");
  const [newNodeDomain, setNewNodeDomain] = createSignal("");
  const [generatedToken, setGeneratedToken] = createSignal("");
  const [agentControllerDomain, setAgentControllerDomain] = createSignal("");
  const [agentToken, setAgentToken] = createSignal("");

  onMount(async () => {
    try {
      const res = await fetch("/api/auth/me", { headers: authHeaders() });
      if (res.ok) {
        const data = await res.json();
        setUsername(data.username);
        setRole(data.role);
      }
    } catch {}
    loadClusterConfig();
  });

  // --- Cluster handlers ---
  const loadClusterConfig = async () => {
    try {
      const cfg = await clusterAPI.getConfig();
      setClusterConfig(cfg);
      setAgentControllerDomain(cfg.controller_domain || "");
      setAgentToken(cfg.controller_token || "");
      if (cfg.node_role === "controller") {
        const nodes = await clusterAPI.listNodes();
        setClusterNodes(nodes);
      }
    } catch {}
  };

  const handleRoleChange = async (newRole: string) => {
    setClusterMsg("");
    setClusterError(false);
    try {
      await clusterAPI.updateConfig({ node_role: newRole });
      setClusterConfig((prev) => prev ? { ...prev, node_role: newRole } : null);
      setClusterMsg(`Role diubah ke ${newRole}`);
      if (newRole === "controller") {
        const nodes = await clusterAPI.listNodes();
        setClusterNodes(nodes);
      }
      setTimeout(() => window.location.reload(), 1000);
    } catch (err: any) {
      setClusterMsg(err.message);
      setClusterError(true);
    }
  };

  const handleAddNode = async (e: Event) => {
    e.preventDefault();
    setClusterMsg("");
    setClusterError(false);
    setGeneratedToken("");
    try {
      const result = await clusterAPI.addNode({ name: newNodeName(), domain: newNodeDomain() });
      setGeneratedToken(result.api_token);
      setClusterMsg(`Node "${result.name}" ditambahkan. Copy token di bawah.`);
      setNewNodeName("");
      setNewNodeDomain("");
      const nodes = await clusterAPI.listNodes();
      setClusterNodes(nodes);
    } catch (err: any) {
      setClusterMsg(err.message);
      setClusterError(true);
    }
  };

  const handleDeleteNodeAction = async (id: number) => {
    try {
      await clusterAPI.deleteNode(id);
      setClusterNodes((prev) => prev.filter((n) => n.id !== id));
    } catch {}
  };

  const handleSaveAgentConfig = async () => {
    setClusterMsg("");
    setClusterError(false);
    try {
      await clusterAPI.updateConfig({
        controller_domain: agentControllerDomain(),
        controller_token: agentToken(),
      });
      setClusterMsg("Konfigurasi agent tersimpan");
    } catch (err: any) {
      setClusterMsg(err.message);
      setClusterError(true);
    }
  };

  const handleRemoteNodeUpdate = async (id: number) => {
    setActiveTab("system");
    setUpdateRunning(true);
    setUpdateOutput([]);
    setUpdateDone(false);
    setUpdateError("");
    try {
      const res = await fetch(`/api/cluster/nodes/${id}/update`, {
        method: "POST",
        headers: authHeaders(),
      });
      if (!res.ok) {
        const data = await res.json();
        setUpdateError(data.error || "Failed");
        setUpdateRunning(false);
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
          else if (line.startsWith("event: done")) { setUpdateDone(true); setUpdateRunning(false); }
        }
      }
      if (!updateDone()) { setUpdateDone(true); setUpdateRunning(false); }
    } catch {
      setUpdateDone(true);
      setUpdateRunning(false);
    }
  };

  // --- Account handlers ---
  const handleChangePassword = async (e: Event) => {
    e.preventDefault();
    setPassMsg("");
    setPassError(false);
    if (newPass() !== confirmPass()) { setPassMsg("Password baru tidak cocok"); setPassError(true); return; }
    if (newPass().length < 8) { setPassMsg("Password minimal 8 karakter"); setPassError(true); return; }
    setPassLoading(true);
    try {
      const res = await fetch("/api/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ old_password: oldPass(), new_password: newPass() }),
      });
      const data = await res.json();
      if (!res.ok) { setPassMsg(data.error || "Gagal ganti password"); setPassError(true); }
      else { setPassMsg("Password berhasil diganti!"); setPassError(false); setOldPass(""); setNewPass(""); setConfirmPass(""); setTimeout(() => logout(), 2000); }
    } catch (err: any) { setPassMsg(err.message); setPassError(true); }
    finally { setPassLoading(false); }
  };

  const handleAddUser = async (e: Event) => {
    e.preventDefault();
    setUserMsg("");
    setUserError(false);
    if (newUserPass().length < 8) { setUserMsg("Password minimal 8 karakter"); setUserError(true); return; }
    try {
      const res = await fetch("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ username: newUser(), password: newUserPass(), role: newUserRole() }),
      });
      const data = await res.json();
      if (!res.ok) { setUserMsg(data.error || "Gagal membuat user"); setUserError(true); }
      else { setUserMsg(`User "${data.username}" (${data.role}) berhasil dibuat`); setUserError(false); setNewUser(""); setNewUserPass(""); }
    } catch (err: any) { setUserMsg(err.message); setUserError(true); }
  };

  // --- Update handlers ---
  const checkForUpdates = async () => {
    setUpdateChecking(true);
    setUpdateError("");
    try { const data = await updateAPI.check(); setUpdateInfo(data); }
    catch (err: any) { setUpdateError(err.message || "Failed to check"); }
    finally { setUpdateChecking(false); }
  };

  const pollHealth = () => {
    setRestarting(true);
    const interval = setInterval(async () => {
      try {
        const res = await fetch("/api/health", { signal: AbortSignal.timeout(3000) });
        if (res.ok) { clearInterval(interval); setRestarting(false); setUpdateDone(true); setUpdateRunning(false); checkForUpdates(); }
      } catch {}
    }, 3000);
  };

  const executeUpdate = async () => {
    setUpdateRunning(true);
    setUpdateOutput([]);
    setUpdateDone(false);
    setUpdateError("");
    try {
      const res = await fetch("/api/admin/update/execute", { method: "POST", headers: authHeaders() });
      if (!res.ok) { const data = await res.json(); setUpdateError(data.error || "Failed"); setUpdateRunning(false); return; }
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
          else if (line.startsWith("event: done")) { setUpdateDone(true); setUpdateRunning(false); }
        }
      }
      if (!updateDone()) pollHealth();
    } catch { if (!updateDone()) pollHealth(); }
  };

  // --- Shared UI helpers ---
  const inputClass = "w-full bg-slate-900/50 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white placeholder-slate-600 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500/30 transition";
  const labelClass = "block text-xs font-medium text-slate-500 mb-1";

  const Alert = (props: { msg: string; error: boolean }) => (
    <div class={`p-2.5 rounded-lg text-xs ${
      props.error ? "bg-red-500/10 text-red-400" : "bg-emerald-500/10 text-emerald-400"
    }`}>{props.msg}</div>
  );

  const tabs = (): { id: Tab; label: string; icon: string }[] => {
    const t: { id: Tab; label: string; icon: string }[] = [
      { id: "account", label: "Akun", icon: "M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" },
    ];
    if (role() === "admin") {
      t.push({ id: "users", label: "Users", icon: "M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" });
      t.push({ id: "cluster", label: "Cluster", icon: "M5 12a7 7 0 0114 0M12 5a7 7 0 010 14m0-14v14m7-7H5" });
      t.push({ id: "system", label: "System", icon: "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" });
    }
    return t;
  };

  return (
    <Layout>
      <div class="max-w-3xl">
        {/* Header with user info inline */}
        <div class="flex items-center justify-between mb-5">
          <div>
            <h1 class="text-xl font-bold text-white">Settings</h1>
            <p class="text-xs text-slate-500 mt-0.5">Pengaturan akun dan sistem</p>
          </div>
          <div class="flex items-center gap-3">
            <div class="text-right">
              <p class="text-sm text-white font-medium">{username()}</p>
              <p class="text-xs text-slate-500 capitalize">{role()}</p>
            </div>
            <div class="w-9 h-9 bg-blue-600 rounded-full flex items-center justify-center text-white text-sm font-bold">
              {username() ? username()[0].toUpperCase() : "?"}
            </div>
          </div>
        </div>

        {/* Tabs */}
        <div class="flex gap-1 mb-4 bg-slate-800/50 rounded-lg p-1">
          <For each={tabs()}>
            {(tab) => (
              <button
                onClick={() => setActiveTab(tab.id)}
                class={`flex items-center gap-2 px-3.5 py-2 rounded-md text-xs font-medium transition-all ${
                  activeTab() === tab.id
                    ? "bg-slate-700 text-white shadow-sm"
                    : "text-slate-400 hover:text-slate-300 hover:bg-slate-800/50"
                }`}
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d={tab.icon} />
                </svg>
                {tab.label}
              </button>
            )}
          </For>
        </div>

        {/* Tab Content */}
        <div class="bg-slate-800 rounded-xl border border-slate-700 overflow-hidden">

          {/* === ACCOUNT TAB === */}
          <Show when={activeTab() === "account"}>
            <div class="p-5 space-y-5">
              {/* Change Password */}
              <div>
                <h3 class="text-sm font-medium text-white mb-3">Ganti Password</h3>
                {passMsg() && <div class="mb-3"><Alert msg={passMsg()} error={passError()} /></div>}
                <form onSubmit={handleChangePassword} class="space-y-3">
                  <div class="grid grid-cols-3 gap-3">
                    <div>
                      <label class={labelClass}>Password Lama</label>
                      <input type="password" required class={inputClass} value={oldPass()} onInput={(e) => setOldPass(e.target.value)} />
                    </div>
                    <div>
                      <label class={labelClass}>Password Baru</label>
                      <input type="password" required class={inputClass} placeholder="Min. 8 karakter" value={newPass()} onInput={(e) => setNewPass(e.target.value)} />
                    </div>
                    <div>
                      <label class={labelClass}>Konfirmasi</label>
                      <input type="password" required class={inputClass} value={confirmPass()} onInput={(e) => setConfirmPass(e.target.value)} />
                    </div>
                  </div>
                  <button type="submit" disabled={passLoading()} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white text-xs font-medium rounded-lg transition-colors">
                    {passLoading() ? "Menyimpan..." : "Ganti Password"}
                  </button>
                </form>
              </div>

              {/* Logout */}
              <div class="pt-4 border-t border-slate-700/50 flex items-center justify-between">
                <div>
                  <p class="text-sm text-slate-300">Keluar dari dashboard</p>
                  <p class="text-xs text-slate-500">Session akan berakhir</p>
                </div>
                <button onClick={() => logout()} class="px-4 py-2 bg-red-500/10 hover:bg-red-500/20 text-red-400 text-xs font-medium rounded-lg border border-red-500/20 transition-colors">
                  Logout
                </button>
              </div>
            </div>
          </Show>

          {/* === USERS TAB === */}
          <Show when={activeTab() === "users"}>
            <div class="p-5">
              <h3 class="text-sm font-medium text-white mb-3">Tambah User Baru</h3>
              {userMsg() && <div class="mb-3"><Alert msg={userMsg()} error={userError()} /></div>}
              <form onSubmit={handleAddUser} class="space-y-3">
                <div class="grid grid-cols-3 gap-3">
                  <div>
                    <label class={labelClass}>Username</label>
                    <input type="text" required class={inputClass} value={newUser()} onInput={(e) => setNewUser(e.target.value)} />
                  </div>
                  <div>
                    <label class={labelClass}>Password</label>
                    <input type="password" required class={inputClass} placeholder="Min. 8 karakter" value={newUserPass()} onInput={(e) => setNewUserPass(e.target.value)} />
                  </div>
                  <div>
                    <label class={labelClass}>Role</label>
                    <select class={inputClass} value={newUserRole()} onChange={(e) => setNewUserRole(e.target.value)}>
                      <option value="viewer">Viewer</option>
                      <option value="admin">Admin</option>
                    </select>
                  </div>
                </div>
                <button type="submit" class="px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white text-xs font-medium rounded-lg transition-colors">
                  Tambah User
                </button>
              </form>
            </div>
          </Show>

          {/* === CLUSTER TAB === */}
          <Show when={activeTab() === "cluster"}>
            <div class="p-5 space-y-4">
              {clusterMsg() && <Alert msg={clusterMsg()} error={clusterError()} />}

              {/* Role selector */}
              <div>
                <label class={labelClass}>Node Role</label>
                <div class="flex gap-1.5 mt-1">
                  {(["standalone", "controller", "agent"] as const).map((r) => (
                    <button
                      onClick={() => handleRoleChange(r)}
                      class={`flex-1 px-3 py-2.5 rounded-lg text-xs font-medium transition-all border ${
                        clusterConfig()?.node_role === r
                          ? r === "controller" ? "bg-blue-600/20 text-blue-400 border-blue-500/30"
                          : r === "agent" ? "bg-purple-600/20 text-purple-400 border-purple-500/30"
                          : "bg-slate-600/20 text-slate-300 border-slate-500/30"
                          : "bg-slate-900/30 text-slate-500 border-slate-700 hover:border-slate-600 hover:text-slate-400"
                      }`}
                    >
                      <div class="font-semibold">{r === "standalone" ? "Standalone" : r === "controller" ? "Controller" : "Agent"}</div>
                      <div class="text-[10px] mt-0.5 opacity-70">
                        {r === "standalone" ? "Single server" : r === "controller" ? "Pusat monitoring" : "Node terpantau"}
                      </div>
                    </button>
                  ))}
                </div>
              </div>

              {/* Controller: registered agents */}
              <Show when={clusterConfig()?.node_role === "controller"}>
                <div class="space-y-3 pt-2 border-t border-slate-700/50">
                  <div class="flex items-center justify-between">
                    <h4 class="text-xs font-medium text-slate-400">Registered Agents</h4>
                    <span class="text-[10px] text-slate-600">{clusterNodes().length} node</span>
                  </div>

                  <Show when={clusterNodes().length > 0}>
                    <div class="space-y-1.5">
                      <For each={clusterNodes()}>
                        {(node) => (
                          <div class="flex items-center justify-between bg-slate-900/50 rounded-lg px-3 py-2.5 group">
                            <div class="flex items-center gap-2.5 min-w-0">
                              <span class={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${
                                node.status === "online" ? "bg-emerald-500" :
                                node.status === "offline" ? "bg-red-500" : "bg-slate-500"
                              }`} />
                              <div class="min-w-0">
                                <p class="text-xs text-white truncate">{node.name || node.domain}</p>
                                <p class="text-[10px] text-slate-600 truncate">{node.domain}{node.version ? ` - ${node.version}` : ""}</p>
                              </div>
                            </div>
                            <div class="flex items-center gap-1.5 opacity-70 group-hover:opacity-100 transition-opacity">
                              <button onClick={() => handleRemoteNodeUpdate(node.id)} disabled={updateRunning()}
                                class="px-2 py-1 text-[10px] bg-amber-500/10 text-amber-400 rounded hover:bg-amber-500/20 transition-colors">
                                Update
                              </button>
                              <button onClick={() => handleDeleteNodeAction(node.id)}
                                class="px-2 py-1 text-[10px] bg-red-500/10 text-red-400 rounded hover:bg-red-500/20 transition-colors">
                                Hapus
                              </button>
                            </div>
                          </div>
                        )}
                      </For>
                    </div>
                  </Show>

                  <Show when={generatedToken()}>
                    <div class="p-2.5 bg-amber-500/10 rounded-lg">
                      <p class="text-[10px] text-amber-400 mb-1.5">API Token (hanya ditampilkan sekali):</p>
                      <div class="flex items-center gap-2">
                        <code class="flex-1 bg-slate-950 px-2.5 py-1.5 rounded text-[10px] text-white font-mono break-all select-all">{generatedToken()}</code>
                        <button onClick={() => navigator.clipboard.writeText(generatedToken())}
                          class="px-2.5 py-1.5 bg-amber-600 text-white text-[10px] rounded hover:bg-amber-700 transition-colors flex-shrink-0">
                          Copy
                        </button>
                      </div>
                    </div>
                  </Show>

                  {/* Add node inline */}
                  <form onSubmit={handleAddNode} class="flex gap-2 items-end">
                    <div class="flex-1">
                      <label class={labelClass}>Nama</label>
                      <input type="text" required placeholder="DNS SG-2" class={inputClass} value={newNodeName()} onInput={(e) => setNewNodeName(e.target.value)} />
                    </div>
                    <div class="flex-1">
                      <label class={labelClass}>Domain</label>
                      <input type="text" required placeholder="dns2.example.com" class={inputClass} value={newNodeDomain()} onInput={(e) => setNewNodeDomain(e.target.value)} />
                    </div>
                    <button type="submit" class="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white text-xs rounded-lg transition-colors flex-shrink-0">
                      + Tambah
                    </button>
                  </form>
                </div>
              </Show>

              {/* Agent: connection config */}
              <Show when={clusterConfig()?.node_role === "agent"}>
                <div class="space-y-3 pt-2 border-t border-slate-700/50">
                  <h4 class="text-xs font-medium text-slate-400">Koneksi ke Controller</h4>
                  <div class="grid grid-cols-2 gap-3">
                    <div>
                      <label class={labelClass}>Domain Controller</label>
                      <input type="text" placeholder="dns1.example.com" class={inputClass}
                        value={agentControllerDomain()} onInput={(e) => setAgentControllerDomain(e.target.value)} />
                    </div>
                    <div>
                      <label class={labelClass}>Cluster Token</label>
                      <input type="password" placeholder="Paste token dari controller" class={inputClass}
                        value={agentToken()} onInput={(e) => setAgentToken(e.target.value)} />
                    </div>
                  </div>
                  <button onClick={handleSaveAgentConfig}
                    class="px-4 py-2 bg-purple-600 hover:bg-purple-700 text-white text-xs font-medium rounded-lg transition-colors">
                    Simpan
                  </button>
                </div>
              </Show>
            </div>
          </Show>

          {/* === SYSTEM TAB === */}
          <Show when={activeTab() === "system"}>
            <div class="p-5 space-y-4">
              {/* Version + check */}
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-sm font-medium text-white">System Update</h3>
                  <p class="text-[10px] text-slate-500 mt-0.5">
                    <Show when={updateInfo()} fallback="Cek update dari GitHub">
                      v{updateInfo()!.current_version}
                      <Show when={updateInfo()!.current_commit}>{" "}({updateInfo()!.current_commit})</Show>
                    </Show>
                  </p>
                </div>
                <div class="flex gap-2">
                  <button onClick={checkForUpdates} disabled={updateChecking() || updateRunning()}
                    class="px-3 py-1.5 bg-slate-700 hover:bg-slate-600 disabled:opacity-50 text-white text-xs rounded-lg transition-colors">
                    {updateChecking() ? "Checking..." : "Check Update"}
                  </button>
                  <Show when={updateInfo()?.update_available}>
                    <button onClick={executeUpdate} disabled={updateRunning()}
                      class="px-3 py-1.5 bg-amber-600 hover:bg-amber-700 disabled:opacity-50 text-white text-xs font-medium rounded-lg transition-colors">
                      {updateRunning() ? "Updating..." : "Update Now"}
                    </button>
                  </Show>
                </div>
              </div>

              {/* Status messages */}
              <Show when={updateError()}>
                <Alert msg={updateError()} error={true} />
              </Show>

              <Show when={updateInfo()?.update_available}>
                <div class="p-2.5 rounded-lg bg-amber-500/10">
                  <div class="flex items-center gap-2 mb-1.5">
                    <span class="w-1.5 h-1.5 bg-amber-400 rounded-full animate-pulse" />
                    <span class="text-xs font-medium text-amber-400">
                      {updateInfo()!.commits_behind} update tersedia
                    </span>
                  </div>
                  <div class="space-y-0.5 ml-3.5">
                    <For each={updateInfo()!.commit_log.slice(0, 5)}>
                      {(commit) => <p class="text-[10px] text-slate-400 font-mono truncate">{commit}</p>}
                    </For>
                    <Show when={updateInfo()!.commit_log.length > 5}>
                      <p class="text-[10px] text-slate-600">+{updateInfo()!.commit_log.length - 5} lainnya</p>
                    </Show>
                  </div>
                </div>
              </Show>

              <Show when={updateInfo() && !updateInfo()!.update_available && !updateDone()}>
                <Alert msg="System sudah up to date" error={false} />
              </Show>

              <Show when={updateDone()}>
                <Alert msg="Update berhasil!" error={false} />
              </Show>

              {/* Terminal output */}
              <Show when={updateOutput().length > 0}>
                <div class="bg-slate-950 rounded-lg p-3 font-mono text-[11px] leading-5 max-h-64 overflow-y-auto border border-slate-700/50"
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
                        "text-slate-400"
                      }>{line}</div>
                    )}
                  </For>
                  <Show when={restarting()}>
                    <div class="text-amber-400 animate-pulse mt-1">Restarting services...</div>
                  </Show>
                </div>
              </Show>
            </div>
          </Show>

        </div>
      </div>
    </Layout>
  );
}
