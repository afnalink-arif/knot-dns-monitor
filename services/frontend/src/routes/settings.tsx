import { createSignal, onMount, Show, For } from "solid-js";
import Layout from "~/components/Layout";
import { authHeaders, getToken, logout } from "~/lib/auth";
import { updateAPI, type UpdateCheckResult } from "~/lib/api";

export default function SettingsPage() {
  const [username, setUsername] = createSignal("");
  const [role, setRole] = createSignal("");

  // Change password
  const [oldPass, setOldPass] = createSignal("");
  const [newPass, setNewPass] = createSignal("");
  const [confirmPass, setConfirmPass] = createSignal("");
  const [passMsg, setPassMsg] = createSignal("");
  const [passError, setPassError] = createSignal(false);
  const [passLoading, setPassLoading] = createSignal(false);

  // Add user (admin only)
  const [newUser, setNewUser] = createSignal("");
  const [newUserPass, setNewUserPass] = createSignal("");
  const [newUserRole, setNewUserRole] = createSignal("viewer");
  const [userMsg, setUserMsg] = createSignal("");
  const [userError, setUserError] = createSignal(false);

  // System update (admin only)
  const [updateInfo, setUpdateInfo] = createSignal<UpdateCheckResult | null>(null);
  const [updateChecking, setUpdateChecking] = createSignal(false);
  const [updateRunning, setUpdateRunning] = createSignal(false);
  const [updateOutput, setUpdateOutput] = createSignal<string[]>([]);
  const [updateDone, setUpdateDone] = createSignal(false);
  const [updateError, setUpdateError] = createSignal("");
  const [restarting, setRestarting] = createSignal(false);

  onMount(async () => {
    try {
      const res = await fetch("/api/auth/me", { headers: authHeaders() });
      if (res.ok) {
        const data = await res.json();
        setUsername(data.username);
        setRole(data.role);
      }
    } catch {}
  });

  const handleChangePassword = async (e: Event) => {
    e.preventDefault();
    setPassMsg("");
    setPassError(false);

    if (newPass() !== confirmPass()) {
      setPassMsg("Password baru tidak cocok");
      setPassError(true);
      return;
    }
    if (newPass().length < 8) {
      setPassMsg("Password minimal 8 karakter");
      setPassError(true);
      return;
    }

    setPassLoading(true);
    try {
      const res = await fetch("/api/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ old_password: oldPass(), new_password: newPass() }),
      });
      const data = await res.json();
      if (!res.ok) {
        setPassMsg(data.error || "Gagal ganti password");
        setPassError(true);
      } else {
        setPassMsg("Password berhasil diganti! Silakan login ulang.");
        setPassError(false);
        setOldPass("");
        setNewPass("");
        setConfirmPass("");
        setTimeout(() => logout(), 2000);
      }
    } catch (err: any) {
      setPassMsg(err.message);
      setPassError(true);
    } finally {
      setPassLoading(false);
    }
  };

  const handleAddUser = async (e: Event) => {
    e.preventDefault();
    setUserMsg("");
    setUserError(false);

    if (newUserPass().length < 8) {
      setUserMsg("Password minimal 8 karakter");
      setUserError(true);
      return;
    }

    try {
      const res = await fetch("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ username: newUser(), password: newUserPass(), role: newUserRole() }),
      });
      const data = await res.json();
      if (!res.ok) {
        setUserMsg(data.error || "Gagal membuat user");
        setUserError(true);
      } else {
        setUserMsg(`User "${data.username}" (${data.role}) berhasil dibuat`);
        setUserError(false);
        setNewUser("");
        setNewUserPass("");
      }
    } catch (err: any) {
      setUserMsg(err.message);
      setUserError(true);
    }
  };

  const checkForUpdates = async () => {
    setUpdateChecking(true);
    setUpdateError("");
    try {
      const data = await updateAPI.check();
      setUpdateInfo(data);
    } catch (err: any) {
      setUpdateError(err.message || "Failed to check for updates");
    } finally {
      setUpdateChecking(false);
    }
  };

  const pollHealth = () => {
    setRestarting(true);
    const interval = setInterval(async () => {
      try {
        const res = await fetch("/api/health", { signal: AbortSignal.timeout(3000) });
        if (res.ok) {
          clearInterval(interval);
          setRestarting(false);
          setUpdateDone(true);
          setUpdateRunning(false);
          // refresh update info
          checkForUpdates();
        }
      } catch {}
    }, 3000);
  };

  const executeUpdate = async () => {
    setUpdateRunning(true);
    setUpdateOutput([]);
    setUpdateDone(false);
    setUpdateError("");

    try {
      const res = await fetch("/api/admin/update/execute", {
        method: "POST",
        headers: authHeaders(),
      });

      if (!res.ok) {
        const data = await res.json();
        setUpdateError(data.error || "Failed to start update");
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
          if (line.startsWith("data: ")) {
            const msg = line.slice(6);
            setUpdateOutput((prev) => [...prev, msg]);
          } else if (line.startsWith("event: done")) {
            setUpdateDone(true);
            setUpdateRunning(false);
          } else if (line.startsWith("event: error")) {
            // next data line will have the error
          }
        }
      }

      if (!updateDone()) {
        // Stream ended without done event — backend probably restarted itself
        pollHealth();
      }
    } catch {
      // Connection lost — backend is restarting
      if (!updateDone()) {
        pollHealth();
      }
    }
  };

  return (
    <Layout>
      <div class="space-y-6 max-w-2xl">
        <div>
          <h1 class="text-2xl font-bold text-white">Settings</h1>
          <p class="text-sm text-slate-400 mt-1">Account and system management</p>
        </div>

        {/* Current User Info */}
        <div class="bg-slate-800 rounded-xl p-5 border border-slate-700">
          <h3 class="text-sm font-medium text-slate-400 mb-4">Current User</h3>
          <div class="flex items-center gap-4">
            <div class="w-12 h-12 bg-blue-600 rounded-full flex items-center justify-center text-white text-lg font-bold">
              {username() ? username()[0].toUpperCase() : "?"}
            </div>
            <div>
              <p class="text-white font-medium">{username()}</p>
              <p class="text-sm text-slate-400 capitalize">{role()}</p>
            </div>
          </div>
        </div>

        {/* Change Password */}
        <div class="bg-slate-800 rounded-xl p-5 border border-slate-700">
          <h3 class="text-sm font-medium text-slate-400 mb-4">Change Password</h3>

          {passMsg() && (
            <div class={`mb-4 p-3 rounded-lg border text-sm ${
              passError()
                ? "bg-red-500/10 border-red-500/20 text-red-400"
                : "bg-emerald-500/10 border-emerald-500/20 text-emerald-400"
            }`}>
              {passMsg()}
            </div>
          )}

          <form onSubmit={handleChangePassword} class="space-y-4">
            <div>
              <label class="block text-sm text-slate-400 mb-1">Password Lama</label>
              <input
                type="password"
                required
                class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                value={oldPass()}
                onInput={(e) => setOldPass(e.target.value)}
              />
            </div>
            <div>
              <label class="block text-sm text-slate-400 mb-1">Password Baru</label>
              <input
                type="password"
                required
                class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                placeholder="Minimal 8 karakter"
                value={newPass()}
                onInput={(e) => setNewPass(e.target.value)}
              />
            </div>
            <div>
              <label class="block text-sm text-slate-400 mb-1">Konfirmasi Password Baru</label>
              <input
                type="password"
                required
                class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                value={confirmPass()}
                onInput={(e) => setConfirmPass(e.target.value)}
              />
            </div>
            <button
              type="submit"
              disabled={passLoading()}
              class="px-6 py-2.5 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-800 text-white text-sm font-medium rounded-lg transition-colors"
            >
              {passLoading() ? "Menyimpan..." : "Ganti Password"}
            </button>
          </form>
        </div>

        {/* Add User (Admin Only) */}
        {role() === "admin" && (
          <div class="bg-slate-800 rounded-xl p-5 border border-slate-700">
            <h3 class="text-sm font-medium text-slate-400 mb-4">Tambah User Baru</h3>

            {userMsg() && (
              <div class={`mb-4 p-3 rounded-lg border text-sm ${
                userError()
                  ? "bg-red-500/10 border-red-500/20 text-red-400"
                  : "bg-emerald-500/10 border-emerald-500/20 text-emerald-400"
              }`}>
                {userMsg()}
              </div>
            )}

            <form onSubmit={handleAddUser} class="space-y-4">
              <div class="grid grid-cols-2 gap-4">
                <div>
                  <label class="block text-sm text-slate-400 mb-1">Username</label>
                  <input
                    type="text"
                    required
                    class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                    value={newUser()}
                    onInput={(e) => setNewUser(e.target.value)}
                  />
                </div>
                <div>
                  <label class="block text-sm text-slate-400 mb-1">Role</label>
                  <select
                    class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                    value={newUserRole()}
                    onChange={(e) => setNewUserRole(e.target.value)}
                  >
                    <option value="viewer">Viewer</option>
                    <option value="admin">Admin</option>
                  </select>
                </div>
              </div>
              <div>
                <label class="block text-sm text-slate-400 mb-1">Password</label>
                <input
                  type="password"
                  required
                  class="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-2.5 text-white focus:outline-none focus:border-blue-500 transition"
                  placeholder="Minimal 8 karakter"
                  value={newUserPass()}
                  onInput={(e) => setNewUserPass(e.target.value)}
                />
              </div>
              <button
                type="submit"
                class="px-6 py-2.5 bg-emerald-600 hover:bg-emerald-700 text-white text-sm font-medium rounded-lg transition-colors"
              >
                Tambah User
              </button>
            </form>
          </div>
        )}

        {/* System Update (Admin Only) */}
        {role() === "admin" && (
          <div class="bg-slate-800 rounded-xl p-5 border border-slate-700">
            <div class="flex items-center justify-between mb-4">
              <div>
                <h3 class="text-sm font-medium text-slate-400">System Update</h3>
                <p class="text-xs text-slate-500 mt-0.5">Check and apply updates from GitHub</p>
              </div>
              <Show when={updateInfo()}>
                <span class="text-xs font-mono text-slate-500">
                  v{updateInfo()!.current_version}
                  <Show when={updateInfo()!.current_commit}>
                    {" "}({updateInfo()!.current_commit})
                  </Show>
                </span>
              </Show>
            </div>

            {/* Error message */}
            <Show when={updateError()}>
              <div class="mb-4 p-3 rounded-lg border text-sm bg-red-500/10 border-red-500/20 text-red-400">
                {updateError()}
              </div>
            </Show>

            {/* Update available banner */}
            <Show when={updateInfo()?.update_available}>
              <div class="mb-4 p-3 rounded-lg border bg-amber-500/10 border-amber-500/20">
                <div class="flex items-center gap-2 mb-2">
                  <div class="w-2 h-2 bg-amber-400 rounded-full animate-pulse" />
                  <span class="text-sm font-medium text-amber-400">
                    {updateInfo()!.commits_behind} update{updateInfo()!.commits_behind > 1 ? "s" : ""} available
                  </span>
                </div>
                <div class="space-y-1 ml-4">
                  <For each={updateInfo()!.commit_log.slice(0, 10)}>
                    {(commit) => (
                      <p class="text-xs text-slate-400 font-mono">{commit}</p>
                    )}
                  </For>
                  <Show when={updateInfo()!.commit_log.length > 10}>
                    <p class="text-xs text-slate-500">... and {updateInfo()!.commit_log.length - 10} more</p>
                  </Show>
                </div>
              </div>
            </Show>

            {/* No updates */}
            <Show when={updateInfo() && !updateInfo()!.update_available && !updateDone()}>
              <div class="mb-4 p-3 rounded-lg border bg-emerald-500/10 border-emerald-500/20">
                <span class="text-sm text-emerald-400">System is up to date</span>
              </div>
            </Show>

            {/* Update complete */}
            <Show when={updateDone()}>
              <div class="mb-4 p-3 rounded-lg border bg-emerald-500/10 border-emerald-500/20">
                <span class="text-sm text-emerald-400">Update completed successfully!</span>
              </div>
            </Show>

            {/* Terminal output */}
            <Show when={updateOutput().length > 0}>
              <div
                class="mb-4 bg-slate-950 rounded-lg p-4 font-mono text-xs leading-5 max-h-80 overflow-y-auto border border-slate-700"
                ref={(el) => {
                  const observer = new MutationObserver(() => {
                    el.scrollTop = el.scrollHeight;
                  });
                  observer.observe(el, { childList: true, subtree: true });
                }}
              >
                <For each={updateOutput()}>
                  {(line) => (
                    <div class={
                      line.includes("[OK]") ? "text-emerald-400" :
                      line.includes("[WARN]") ? "text-amber-400" :
                      line.includes("[ERROR]") ? "text-red-400" :
                      line.includes("[INFO]") ? "text-blue-400" :
                      "text-slate-300"
                    }>{line}</div>
                  )}
                </For>
                <Show when={restarting()}>
                  <div class="text-amber-400 animate-pulse mt-1">Services restarting, please wait...</div>
                </Show>
              </div>
            </Show>

            {/* Action buttons */}
            <div class="flex gap-3">
              <button
                onClick={checkForUpdates}
                disabled={updateChecking() || updateRunning()}
                class="px-4 py-2 bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:text-slate-500 text-white text-sm rounded-lg transition-colors"
              >
                {updateChecking() ? "Checking..." : "Check for Updates"}
              </button>
              <Show when={updateInfo()?.update_available}>
                <button
                  onClick={executeUpdate}
                  disabled={updateRunning()}
                  class="px-4 py-2 bg-amber-600 hover:bg-amber-700 disabled:bg-amber-800 text-white text-sm font-medium rounded-lg transition-colors"
                >
                  {updateRunning() ? "Updating..." : "Update Now"}
                </button>
              </Show>
            </div>
          </div>
        )}

        {/* Logout */}
        <div class="bg-slate-800 rounded-xl p-5 border border-red-500/20">
          <div class="flex items-center justify-between">
            <div>
              <h3 class="text-sm font-medium text-slate-400">Session</h3>
              <p class="text-xs text-slate-500 mt-1">Logout dari dashboard</p>
            </div>
            <button
              onClick={() => logout()}
              class="px-4 py-2 bg-red-600/20 hover:bg-red-600/30 text-red-400 text-sm rounded-lg border border-red-500/20 transition-colors"
            >
              Logout
            </button>
          </div>
        </div>
      </div>
    </Layout>
  );
}
