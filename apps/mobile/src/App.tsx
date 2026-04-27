import { useEffect, useMemo, useState } from "react";
import { AuthScreen } from "@/components/auth/AuthScreen";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import { SplitPanel } from "@/components/layout/SplitPanel";
import { useAuthStore } from "@/stores/authStore";
import { useAvatarSync } from "@/hooks/useAvatar";
import { useAvatarStore } from "@/stores/avatarStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useChatStore } from "@/stores/chatStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { findRole } from "@/types/role";

function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    const media = window.matchMedia(query);
    const listener = (event: MediaQueryListEvent) => {
      setMatches(event.matches);
    };
    setMatches(media.matches);
    media.addEventListener("change", listener);
    return () => {
      media.removeEventListener("change", listener);
    };
  }, [query]);

  return matches;
}

function RoleHeader() {
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const role = findRole(activeRole, customRoles);

  return (
    <div className="flex min-w-0 items-center">
      <div className="flex min-w-0 items-center gap-2.5 rounded-xl bg-[var(--surface-soft)] px-2.5 py-1.5 ring-1 ring-[var(--border-subtle)]">
        <img
          src={role.avatarUrl}
          alt={role.name}
          className="h-8 w-8 shrink-0 rounded-full object-cover ring-1 ring-[var(--border-subtle)]"
          draggable={false}
        />
        <div className="min-w-0 leading-none">
          <p className="truncate text-[12px] font-medium text-[var(--text-strong)]">{role.name}</p>
          <p className="mt-0.5 truncate text-[10px] text-[var(--text-soft)]">{role.description}</p>
        </div>
      </div>
    </div>
  );
}

function MobilePortraitLayout() {
  useAvatarSync();
  const account = useAuthStore((s) => s.account);
  const logout = useAuthStore((s) => s.logout);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const theme = useSettingsStore((s) => s.theme);
  const toggleTheme = useSettingsStore((s) => s.toggleTheme);
  const openSettings = useSettingsStore((s) => s.openSettings);
  const clearContext = useChatStore((s) => s.clearContext);
  const messageCount = useChatStore((s) => s.getActiveSession().messages.length);
  const [tab, setTab] = useState<"chat" | "metrics">("chat");

  const handleClearContext = () => {
    if (messageCount === 0) return;
    if (window.confirm("确定清空当前对话上下文吗？")) {
      clearContext();
    }
  };

  return (
    <div className="app-shell flex h-screen w-screen flex-col overflow-hidden pb-[env(safe-area-inset-bottom)]">
      <header className="flex items-center justify-between border-b border-[var(--border-subtle)] bg-[var(--surface-soft)] px-3 py-2">
        <RoleHeader />
        <div className="flex items-center gap-1">
          {account && (
            <div className="mr-1 hidden items-center gap-1 text-xs text-[var(--text-soft)] sm:flex">
              <AvatarDisplay size={20} avatarUrl={avatarUrl} username={account.username} />
              @{account.username}
            </div>
          )}
          <button onClick={toggleTheme} className="ui-icon-button" aria-label="切换主题" title="切换主题">
            {theme === "light" ? "🌙" : "☀️"}
          </button>
          <button
            onClick={handleClearContext}
            disabled={messageCount === 0}
            className="ui-icon-button"
            aria-label="清空上下文"
            title="清空上下文"
          >
            🗑️
          </button>
          <button onClick={openSettings} className="ui-icon-button" aria-label="设置" title="设置">⚙️</button>
          <button onClick={() => { void logout(); }} className="ui-icon-button" aria-label="退出登录" title="退出登录">⎋</button>
        </div>
      </header>

      <main className="flex flex-1 flex-col overflow-hidden px-3 pt-2 pb-[max(14px,env(safe-area-inset-bottom))]">
        <div className="min-h-0 flex-1 pb-4 [&_.chat-input-shell]:rounded-[22px] [&_.chat-input-shell]:bg-[color-mix(in_srgb,var(--surface-soft)_96%,transparent)] [&_.chat-input-shell]:px-3 [&_.chat-input-shell]:shadow-[0_10px_24px_rgba(0,0,0,0.18)]">
          <div className="h-full overflow-hidden rounded-3xl bg-[var(--surface-panel)]/60 shadow-[0_16px_40px_rgba(0,0,0,0.22)] ring-1 ring-[var(--border-subtle)]">
            {tab === "chat" ? (
              <ChatPanel showClearButton={false} sendVariant="pill" sendLabel="发送" />
            ) : (
              <section className="flex h-full w-full flex-col overflow-hidden px-2 pt-2 pb-1">
                <div className="mb-2 rounded-2xl bg-[var(--surface-soft)] px-3 py-2 ring-1 ring-[var(--border-subtle)]">
                  <p className="text-sm font-semibold text-[var(--text-strong)]">能力画像总览</p>
                  <p className="mt-0.5 text-[11px] text-[var(--text-soft)]">参考桌面端能力面板布局，保留 Rating、雷达图与历史轨迹。</p>
                </div>
                <div className="min-h-0 flex-1 overflow-hidden rounded-2xl bg-[var(--surface-panel)]/75 ring-1 ring-[var(--border-subtle)]">
                  <MetricsPanel />
                </div>
              </section>
            )}
          </div>
        </div>

        <div className="flex-none pb-1">
          <div className="w-full rounded-full bg-[color-mix(in_srgb,var(--surface-soft)_82%,transparent)] p-2 shadow-[0_20px_44px_rgba(0,0,0,0.32)] ring-1 ring-[var(--border-subtle)] backdrop-blur-2xl">
            <div className="grid grid-cols-2 gap-2">
              <button
                className={`group flex h-16 flex-col items-center justify-center rounded-full px-3 transition-all duration-300 ${tab === "chat" ? "scale-[1.01] bg-[linear-gradient(140deg,var(--accent-soft),color-mix(in_srgb,var(--accent-soft)_65%,white_35%))] text-[var(--text-strong)] shadow-[0_10px_26px_rgba(43,83,255,0.24)]" : "bg-[var(--surface-soft)] text-[var(--text-soft)]"}`}
                onClick={() => setTab("chat")}
              >
                <span className="text-xl leading-none">💬</span>
                <span className="mt-1 text-[11px] font-semibold tracking-wide">对话</span>
              </button>
              <button
                className={`group flex h-16 flex-col items-center justify-center rounded-full px-3 transition-all duration-300 ${tab === "metrics" ? "scale-[1.01] bg-[linear-gradient(140deg,var(--accent-soft),color-mix(in_srgb,var(--accent-soft)_65%,white_35%))] text-[var(--text-strong)] shadow-[0_10px_26px_rgba(43,83,255,0.24)]" : "bg-[var(--surface-soft)] text-[var(--text-soft)]"}`}
                onClick={() => setTab("metrics")}
              >
                <span className="text-xl leading-none">📊</span>
                <span className="mt-1 text-[11px] font-semibold tracking-wide">画像</span>
              </button>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

function TabletLandscapeLayout() {
  useAvatarSync();
  const account = useAuthStore((s) => s.account);
  const logout = useAuthStore((s) => s.logout);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const theme = useSettingsStore((s) => s.theme);
  const toggleTheme = useSettingsStore((s) => s.toggleTheme);
  const openSettings = useSettingsStore((s) => s.openSettings);
  const clearContext = useChatStore((s) => s.clearContext);
  const messageCount = useChatStore((s) => s.getActiveSession().messages.length);

  const handleClearContext = () => {
    if (messageCount === 0) return;
    if (window.confirm("确定清空当前对话上下文吗？")) {
      clearContext();
    }
  };

  return (
    <div className="app-shell flex h-screen w-screen flex-col overflow-hidden">
      <header className="flex h-12 items-center justify-between border-b border-[var(--border-subtle)] px-3">
        <RoleHeader />
        <div className="flex items-center gap-1.5">
          {account && (
            <div className="mr-2 flex items-center gap-1 text-xs text-[var(--text-soft)]">
              <AvatarDisplay size={20} avatarUrl={avatarUrl} username={account.username} />
              @{account.username}
            </div>
          )}
          <button onClick={toggleTheme} className="ui-icon-button" aria-label="切换主题" title="切换主题">
            {theme === "light" ? "🌙" : "☀️"}
          </button>
          <button
            onClick={handleClearContext}
            disabled={messageCount === 0}
            className="ui-icon-button"
            aria-label="清空上下文"
            title="清空上下文"
          >
            🗑️
          </button>
          <button onClick={openSettings} className="ui-icon-button" aria-label="设置" title="设置">⚙️</button>
          <button onClick={() => { void logout(); }} className="ui-icon-button" aria-label="退出登录" title="退出登录">⎋</button>
        </div>
      </header>
      <main className="flex-1 overflow-hidden px-[var(--app-gutter-x)] pb-[var(--app-gutter-y)] pt-3">
        <SplitPanel
          left={<ChatPanel showClearButton={false} sendVariant="pill" sendLabel="发送" />}
          right={<MetricsPanel />}
          defaultRatio={0.55}
          minRatio={0.3}
        />
      </main>
    </div>
  );
}

export default function App() {
  const theme = useSettingsStore((s) => s.theme);
  const authStatus = useAuthStore((s) => s.status);
  const bootstrap = useAuthStore((s) => s.bootstrap);
  const isTabletLandscape = useMediaQuery("(min-width: 960px) and (orientation: landscape)");

  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute("data-theme", theme);
    root.style.colorScheme = theme;
  }, [theme]);

  useEffect(() => {
    void bootstrap();
  }, [bootstrap]);

  const content = useMemo(() => {
    if (authStatus === "booting") {
      return <div className="flex h-screen w-screen items-center justify-center text-sm text-[var(--text-soft)]">启动中...</div>;
    }
    if (authStatus !== "authenticated") {
      return <AuthScreen />;
    }
    return isTabletLandscape ? <TabletLandscapeLayout /> : <MobilePortraitLayout />;
  }, [authStatus, isTabletLandscape]);

  return (
    <>
      {content}
      <SettingsDialog />
    </>
  );
}
