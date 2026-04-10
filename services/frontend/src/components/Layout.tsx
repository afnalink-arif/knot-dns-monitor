import { type Component, type JSX, createSignal, onMount, Show, For } from "solid-js";
import { A, useLocation, useSearchParams } from "@solidjs/router";
import { logout, getToken, getUser, setUserInfo, authHeaders } from "~/lib/auth";
import { clusterAPI } from "~/lib/api";
import { t, getLang, setLang } from "~/lib/i18n";

interface LayoutProps {
  children: JSX.Element;
}

// Inline brand logo SVG (DNS node network)
const BrandLogo = (props: { class?: string }) => (
  <svg viewBox="0 0 32 32" fill="none" class={props.class || "w-8 h-8"}>
    <defs>
      <linearGradient id="logo-grad" x1="0%" y1="0%" x2="100%" y2="100%">
        <stop offset="0%" stop-color="#60a5fa"/>
        <stop offset="100%" stop-color="#2563eb"/>
      </linearGradient>
    </defs>
    <rect width="32" height="32" rx="8" fill="url(#logo-grad)"/>
    <circle cx="16" cy="10" r="2.5" fill="white"/>
    <circle cx="9" cy="20" r="2" fill="white" opacity="0.8"/>
    <circle cx="23" cy="20" r="2" fill="white" opacity="0.8"/>
    <circle cx="16" cy="25" r="1.5" fill="white" opacity="0.6"/>
    <line x1="16" y1="12.5" x2="9" y2="18" stroke="white" stroke-width="1.2" opacity="0.5"/>
    <line x1="16" y1="12.5" x2="23" y2="18" stroke="white" stroke-width="1.2" opacity="0.5"/>
    <line x1="9" y1="22" x2="16" y2="23.5" stroke="white" stroke-width="1" opacity="0.35"/>
    <line x1="23" y1="22" x2="16" y2="23.5" stroke="white" stroke-width="1" opacity="0.35"/>
  </svg>
);

// Nav item groups - use i18n keys for labels
const navGroupDefs = [
  {
    labelKey: "nav.monitor",
    items: [
      { href: "/", labelKey: "nav.overview", icon: "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" },
      { href: "/queries", labelKey: "nav.query_metrics", icon: "M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" },
      { href: "/logs", labelKey: "nav.query_logs", icon: "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" },
      { href: "/system", labelKey: "nav.system", icon: "M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" },
    ],
  },
  {
    labelKey: "nav.dns",
    items: [
      { href: "/cache", labelKey: "nav.cache", icon: "M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21-3.582 4-8 4s-8-1.79-8-4" },
      { href: "/dnssec", labelKey: "nav.dnssec", icon: "M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" },
      { href: "/upstreams", labelKey: "nav.upstreams", icon: "M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" },
      { href: "/filtering", labelKey: "nav.filtering", icon: "M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" },
      { href: "/dns-lookup", labelKey: "nav.dns_lookup", icon: "M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" },
    ],
  },
  {
    labelKey: "nav.manage",
    items: [
      { href: "/alerts", labelKey: "nav.alerts", icon: "M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" },
      { href: "/settings", labelKey: "nav.settings", icon: "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z" },
    ],
  },
];

const clusterNavItem = { href: "/cluster", label: "Cluster", icon: "M5 12a7 7 0 0114 0M12 5a7 7 0 010 14m0-14v14m7-7H5" };

const Layout: Component<LayoutProps> = (props) => {
  const location = useLocation();
  const [clusterRole, setClusterRole] = createSignal("standalone");
  const [searchParams] = useSearchParams();

  onMount(async () => {
    if (!getUser() && getToken()) {
      try {
        const res = await fetch("/api/auth/me", { headers: authHeaders() });
        if (res.ok) setUserInfo(await res.json());
      } catch {}
    }
    try {
      const cfg = await clusterAPI.getConfig();
      setClusterRole(cfg.node_role);
    } catch {}
  });

  const getNavGroups = () => {
    const groups = navGroupDefs.map(g => ({
      label: t(g.labelKey),
      items: g.items.map(i => ({ ...i, label: t(i.labelKey) })),
    }));
    if (clusterRole() === "controller") {
      groups[0].items.splice(1, 0, { ...clusterNavItem, label: t("nav.cluster"), labelKey: "nav.cluster" });
    }
    return groups;
  };

  const isActive = (href: string) => {
    if (href === "/") return location.pathname === "/";
    return location.pathname.startsWith(href);
  };

  const settingsSubItems = (): { tab: string; label: string }[] => {
    const items = [{ tab: "account", label: t("nav.settings.account") }];
    if (getUser()?.role === "admin") {
      items.push({ tab: "users", label: t("nav.settings.users") });
      items.push({ tab: "server", label: t("nav.settings.server") });
      items.push({ tab: "update", label: t("nav.settings.update") });
      items.push({ tab: "cluster", label: t("nav.settings.cluster") });
    }
    return items;
  };

  const activeSettingsTab = () => searchParams.tab || "account";

  const roleLabel = () => {
    switch (clusterRole()) {
      case "controller": return "Controller";
      case "agent": return "Agent";
      default: return "Standalone";
    }
  };

  return (
    <div class="flex h-screen overflow-hidden bg-[var(--color-bg)]">
      {/* Sidebar */}
      <aside class="w-[260px] bg-[var(--color-bg-sidebar)] border-r border-[var(--color-border)] flex flex-col flex-shrink-0">
        {/* Brand header */}
        <div class="px-4 py-4">
          <div class="flex items-center gap-3">
            <img src="/logo-kdm.png" alt="KResD Manager" class="h-9 object-contain flex-shrink-0" />
            <div>
              <h1 class="text-[14px] font-bold text-white tracking-tight leading-tight">KResD</h1>
              <p class="text-[10px] text-[var(--color-text-faint)] font-medium tracking-wider uppercase">Manager</p>
            </div>
          </div>
        </div>

        {/* Navigation */}
        <nav class="flex-1 px-3 pb-3 overflow-y-auto space-y-5">
          <For each={getNavGroups()}>
            {(group) => (
              <div>
                <p class="px-3 mb-1.5 text-[10px] font-semibold text-[var(--color-text-faint)] uppercase tracking-wider">{group.label as string}</p>
                <div class="space-y-0.5">
                  <For each={group.items}>
                    {(item) => (
                      item.href === "/settings" ? (
                        <div>
                          <A
                            href="/settings?tab=account"
                            class={`flex items-center gap-3 px-3 py-2 rounded-lg text-[13px] transition-all ${
                              isActive("/settings")
                                ? "bg-[var(--color-brand-500)]/15 text-[var(--color-brand-400)] font-medium"
                                : "text-[var(--color-text-muted)] hover:text-white hover:bg-white/5"
                            }`}
                          >
                            <svg class="w-[18px] h-[18px] flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                              <path stroke-linecap="round" stroke-linejoin="round" d={item.icon} />
                            </svg>
                            {item.label}
                          </A>
                          <Show when={isActive("/settings")}>
                            <div class="ml-[30px] mt-0.5 space-y-0.5 border-l border-[var(--color-border)] pl-3">
                              <For each={settingsSubItems()}>
                                {(sub) => (
                                  <A
                                    href={`/settings?tab=${sub.tab}`}
                                    class={`block px-2.5 py-1.5 rounded-md text-[11px] transition-colors ${
                                      activeSettingsTab() === sub.tab
                                        ? "text-[var(--color-brand-400)] bg-[var(--color-brand-500)]/10 font-medium"
                                        : "text-[var(--color-text-faint)] hover:text-[var(--color-text-muted)] hover:bg-white/5"
                                    }`}
                                  >
                                    {sub.label}
                                  </A>
                                )}
                              </For>
                            </div>
                          </Show>
                        </div>
                      ) : (
                        <A
                          href={item.href}
                          class={`flex items-center gap-3 px-3 py-2 rounded-lg text-[13px] transition-all ${
                            isActive(item.href)
                              ? "bg-[var(--color-brand-500)]/15 text-[var(--color-brand-400)] font-medium"
                              : "text-[var(--color-text-muted)] hover:text-white hover:bg-white/5"
                          }`}
                        >
                          <svg class="w-[18px] h-[18px] flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                            <path stroke-linecap="round" stroke-linejoin="round" d={item.icon} />
                          </svg>
                          {item.label}
                        </A>
                      )
                    )}
                  </For>
                </div>
              </div>
            )}
          </For>
        </nav>

        {/* Footer */}
        <div class="px-4 py-3 border-t border-[var(--color-border)]">
          {/* Status + Language */}
          <div class="flex items-center justify-between mb-3">
            <div class="flex items-center gap-2">
              <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              <span class="text-[10px] text-[var(--color-text-faint)]">{t("footer.resolver_active")}</span>
            </div>
            <button
              onClick={() => setLang(getLang() === "en" ? "id" : "en")}
              class="flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] text-[var(--color-text-faint)] hover:text-white hover:bg-white/5 transition-colors"
              title={t("common.language")}
            >
              <span class="font-mono font-bold">{getLang() === "en" ? "EN" : "ID"}</span>
              <svg class="w-3 h-3 opacity-50" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4" />
              </svg>
            </button>
          </div>
          <div class="flex items-center gap-1.5 mb-3">
            <span class={`w-1.5 h-1.5 rounded-full ${
              clusterRole() === "controller" ? "bg-blue-500" :
              clusterRole() === "agent" ? "bg-purple-500" : "bg-slate-600"
            }`} />
            <span class="text-[10px] text-[var(--color-text-faint)]">{roleLabel()}</span>
          </div>

          {/* User + Logout */}
          <Show when={getUser()}>
            <button
              onClick={() => logout()}
              class="flex items-center gap-2.5 w-full px-2.5 py-2 text-[12px] text-[var(--color-text-faint)] hover:text-red-400 hover:bg-red-500/5 rounded-lg transition-colors group"
            >
              <div class="w-6 h-6 bg-[var(--color-brand-500)]/20 rounded-full flex items-center justify-center text-[10px] font-bold text-[var(--color-brand-400)] flex-shrink-0 group-hover:bg-red-500/20 group-hover:text-red-400 transition-colors">
                {getUser()!.username[0].toUpperCase()}
              </div>
              <span class="truncate">{getUser()!.username}</span>
              <svg class="w-3.5 h-3.5 ml-auto flex-shrink-0 opacity-0 group-hover:opacity-100 transition-opacity" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
              </svg>
            </button>
          </Show>

          {/* Copyright */}
          <p class="text-[9px] text-[var(--color-text-faint)]/60 text-center mt-2 select-none">
            {t("footer.copyright")}
          </p>
        </div>
      </aside>

      {/* Main Content */}
      <main class="flex-1 overflow-y-auto">
        <div class="p-6">
          {props.children}
        </div>
      </main>
    </div>
  );
};

export default Layout;
