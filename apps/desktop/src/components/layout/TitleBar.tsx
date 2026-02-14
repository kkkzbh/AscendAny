import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";

export function TitleBar() {
  const openSettings = useSettingsStore((s) => s.openSettings);
  const theme = useSettingsStore((s) => s.theme);
  const toggleTheme = useSettingsStore((s) => s.toggleTheme);
  const account = useAuthStore((s) => s.account);
  const logout = useAuthStore((s) => s.logout);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const api = window.electronAPI;
  const isMac = api?.platform === "darwin";
  const nextThemeLabel = theme === "light" ? "切换到暗色主题" : "切换到亮色主题";

  return (
    <header
      className="drag-region titlebar titlebar-pad relative flex h-12 w-full shrink-0 items-center"
    >
      <div
        className={`titlebar-brand flex items-center gap-3 ${isMac ? "pl-20" : "pr-28"}`}
      >
        <div className="flex h-7 w-7 items-center justify-center rounded-xl bg-gradient-to-br from-[var(--accent-600)] to-[var(--accent-400)] shadow-[0_8px_20px_rgba(3,105,161,0.28)]">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M4 14l5-5 4 4 7-7" />
            <path d="M16 6h4v4" />
          </svg>
        </div>
        <div className="flex flex-col leading-none">
          <span className="text-[13px] font-semibold tracking-[0.02em] text-[var(--text-strong)]">AscendAny</span>
          <span className="text-[10px] text-[var(--text-muted)]">Student Insight Studio</span>
        </div>
      </div>

      <div
        className={`titlebar-actions no-drag flex h-full items-center gap-0.5 ${
          isMac ? "ml-auto" : "absolute right-2 top-0"
        }`}
      >
        {account && (
          <div className="mr-2 hidden items-center gap-1.5 rounded-md px-2 py-1 text-[11px] text-[var(--text-soft)] sm:flex">
            <AvatarDisplay
              size={20}
              avatarUrl={avatarUrl}
              username={account.username}
            />
            @{account.username}
          </div>
        )}
        <button
          onClick={toggleTheme}
          className="ui-icon-button"
          title={nextThemeLabel}
          aria-label={nextThemeLabel}
        >
          {theme === "light" ? (
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
              <path d="M12 3v2.2M12 18.8V21M4.6 4.6l1.6 1.6M17.8 17.8l1.6 1.6M3 12h2.2M18.8 12H21M4.6 19.4l1.6-1.6M17.8 6.2l1.6-1.6" />
              <circle cx="12" cy="12" r="4.2" />
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
              <path d="M20.4 14.5A8.5 8.5 0 1 1 9.5 3.6a6.8 6.8 0 0 0 10.9 10.9z" />
            </svg>
          )}
        </button>

        <button
          onClick={openSettings}
          className="ui-icon-button"
          title="设置"
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
              className="ui-window-button"
              title="最小化"
            >
              <svg width="10" height="10" viewBox="0 0 12 12">
                <rect x="2" y="5.5" width="8" height="1" rx="0.5" fill="currentColor" />
              </svg>
            </button>
            <button
              onClick={() => api?.maximize()}
              className="ui-window-button"
              title="最大化"
            >
              <svg width="10" height="10" viewBox="0 0 12 12">
                <rect x="2" y="2" width="8" height="8" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.2" />
              </svg>
            </button>
            <button
              onClick={() => api?.close()}
              className="ui-window-button ui-window-close"
              title="关闭"
            >
              <svg width="10" height="10" viewBox="0 0 12 12">
                <path d="M3 3l6 6M9 3l-6 6" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
              </svg>
            </button>
          </div>
        )}
      </div>
    </header>
  );
}
