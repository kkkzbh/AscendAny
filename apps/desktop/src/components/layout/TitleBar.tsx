import { useState } from "react";

import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import { LeaderboardDialog } from "@/components/leaderboard/LeaderboardDialog";
import { findRole } from "@/types/role";

export function TitleBar() {
  const [isLeaderboardOpen, setIsLeaderboardOpen] = useState(false);
  const openSettings = useSettingsStore((s) => s.openSettings);
  const theme = useSettingsStore((s) => s.theme);
  const toggleTheme = useSettingsStore((s) => s.toggleTheme);
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const account = useAuthStore((s) => s.account);
  const logout = useAuthStore((s) => s.logout);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  const toggleMetricsPanel = useLayoutStore((s) => s.toggleMetricsPanel);
  const setActiveFullscreenView = useLayoutStore((s) => s.setActiveFullscreenView);
  const api = window.electronAPI;
  const isMac = api?.platform === "darwin";
  const nextThemeLabel = theme === "light" ? "切换到暗色主题" : "切换到亮色主题";
  const metricsPanelLabel = isMetricsPanelVisible ? "收起能力栏" : "展开能力栏";
  const role = findRole(activeRole, customRoles);

  return (
    <>
      <header
        className="drag-region titlebar titlebar-pad relative flex h-12 w-full shrink-0 items-center"
      >
        <div
          className={`titlebar-brand flex min-w-0 items-center ${isMac ? "pl-20" : ""}`}
        >
          <div
            className={`flex min-w-0 items-center gap-2.5 rounded-xl bg-[var(--surface-soft)] px-2.5 py-1.5 ring-1 ring-[var(--border-subtle)] ${
              isMac ? "max-w-[460px]" : "max-w-[420px]"
            }`}
          >
            <img
              src={role.avatarUrl}
              alt={role.name}
              className="h-8 w-8 shrink-0 rounded-full object-cover ring-1 ring-[var(--border-subtle)]"
              draggable={false}
            />
            <div className="min-w-0 leading-none">
              <p className="truncate text-[12px] font-medium text-[var(--text-strong)]">
                {role.name}
              </p>
              <p className="mt-0.5 truncate text-[10px] text-[var(--text-soft)]">
                {role.description}
              </p>
            </div>
          </div>
        </div>

        <div
          className={`titlebar-actions flex h-full items-center ${
            isMac ? "ml-auto" : "absolute right-2 top-0"
          }`}
        >
          <div className="titlebar-actions-capsule no-drag flex items-center gap-0.5">
            {account && (
              <div className="titlebar-account-chip mr-1 hidden items-center gap-1.5 rounded-full px-2 py-1 text-[11px] text-[var(--text-soft)] sm:flex">
                <AvatarDisplay
                  size={20}
                  avatarUrl={avatarUrl}
                  username={account.displayName ?? account.username}
                />
                @{account.displayName ?? account.username}
              </div>
            )}
            <button
              onClick={() => setIsLeaderboardOpen(true)}
              className="ui-icon-button"
              title="排行榜"
              aria-label="打开排行榜"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M8 21h8" />
                <path d="M12 17v4" />
                <path d="M7 4h10l-1.2 5.5a4 4 0 0 1-3.9 3.2h0a4 4 0 0 1-3.9-3.2L7 4Z" />
                <path d="M6.2 5.6H4a2 2 0 0 0 2 2" />
                <path d="M17.8 5.6H20a2 2 0 0 1-2 2" />
              </svg>
            </button>
            <button
              onClick={() => setActiveFullscreenView("achievements")}
              className="ui-icon-button"
              title="成就图鉴"
              aria-label="打开成就页面"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M4.5 5.5A2.5 2.5 0 0 1 7 3h12v16.5H7A2.5 2.5 0 0 0 4.5 22z" />
                <path d="M7 3v16.5" />
                <path d="M19 19.5H7A2.5 2.5 0 0 0 4.5 22" />
                <path d="M10 7h6" />
                <path d="M10 10h6" />
              </svg>
            </button>
            <button
              onClick={toggleMetricsPanel}
              className="ui-icon-button"
              title={metricsPanelLabel}
              aria-label={metricsPanelLabel}
            >
              {isMetricsPanelVisible ? (
                <svg
                  width="15"
                  height="15"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <rect x="3.2" y="4" width="17.6" height="16" rx="2.6" />
                  <path d="M13.5 4v16" />
                  <path d="M17.2 9.5l-3 2.5 3 2.5" />
                </svg>
              ) : (
                <svg
                  width="15"
                  height="15"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <rect x="3.2" y="4" width="17.6" height="16" rx="2.6" />
                  <path d="M13.5 4v16" />
                  <path d="M14.2 9.5l3 2.5-3 2.5" />
                </svg>
              )}
            </button>

            <button
              onClick={toggleTheme}
              className="ui-icon-button"
              title={nextThemeLabel}
              aria-label={nextThemeLabel}
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                {theme === "light" ? (
                  <>
                    <path d="M12 3v2.2M12 18.8V21M4.6 4.6l1.6 1.6M17.8 17.8l1.6 1.6M3 12h2.2M18.8 12H21M4.6 19.4l1.6-1.6M17.8 6.2l1.6-1.6" />
                    <circle cx="12" cy="12" r="4.2" />
                  </>
                ) : (
                  <path d="M20.4 14.5A8.5 8.5 0 1 1 9.5 3.6a6.8 6.8 0 0 0 10.9 10.9z" />
                )}
              </svg>
            </button>

            <button
              onClick={openSettings}
              className="ui-icon-button"
              title="设置"
              aria-label="打开设置"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>

            <button
              onClick={() => {
                void api?.openFeedbackWindow?.();
              }}
              className="ui-icon-button"
              title="反馈"
              aria-label="打开反馈窗口"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M7 10h10M7 14h7" />
                <path d="M21 12a8.8 8.8 0 0 1-8.9 8.7h-3.8L3 23l1.8-4.4A8.8 8.8 0 1 1 21 12Z" />
              </svg>
            </button>

            <button
              onClick={() => {
                void logout();
              }}
              className="ui-icon-button"
              title="退出登录"
              aria-label="退出登录"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" />
                <polyline points="16 17 21 12 16 7" />
                <line x1="21" y1="12" x2="9" y2="12" />
              </svg>
            </button>

            {!isMac && (
              <div className="ml-1.5 flex h-full items-center">
                <button
                  onClick={() => api?.minimize()}
                  className="ui-window-button ui-window-traffic ui-window-minimize"
                  title="最小化"
                  aria-label="最小化"
                >
                  <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
                </button>
                <button
                  onClick={() => api?.maximize()}
                  className="ui-window-button ui-window-traffic ui-window-maximize"
                  title="最大化"
                  aria-label="最大化"
                >
                  <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
                </button>
                <button
                  onClick={() => api?.close()}
                  className="ui-window-button ui-window-traffic ui-window-close"
                  title="关闭"
                  aria-label="关闭"
                >
                  <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
                </button>
              </div>
            )}
          </div>
        </div>
      </header>
      <LeaderboardDialog
        isOpen={isLeaderboardOpen}
        onClose={() => setIsLeaderboardOpen(false)}
      />
    </>
  );
}
