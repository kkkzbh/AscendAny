import { useLayoutStore } from "@/stores/layoutStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import { useSettingsStore } from "@/stores/settingsStore";

export function TitleBar() {
  const openLeaderboard = useLeaderboardStore((s) => s.openLeaderboard);
  const theme = useSettingsStore((s) => s.theme);
  const toggleTheme = useSettingsStore((s) => s.toggleTheme);
  const isLeftSidebarCollapsed = useLayoutStore((s) => s.isLeftSidebarCollapsed);
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  const setLeftSidebarCollapsed = useLayoutStore((s) => s.setLeftSidebarCollapsed);
  const toggleMetricsPanel = useLayoutStore((s) => s.toggleMetricsPanel);
  const setActiveFullscreenView = useLayoutStore((s) => s.setActiveFullscreenView);
  const api = window.electronAPI;
  const rightPanelLabel = isMetricsPanelVisible ? "折叠右侧栏" : "展开右侧栏";
  const nextThemeLabel = theme === "light" ? "切换到暗色主题" : "切换到亮色主题";

  return (
    <header className="student-titlebar drag-region">
        {isLeftSidebarCollapsed ? (
          <button
            type="button"
            className="student-titlebar-button student-titlebar-left-button no-drag"
            onClick={() => setLeftSidebarCollapsed(false)}
            title="展开左侧栏"
            aria-label="展开左侧栏"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <rect x="3.5" y="4" width="17" height="16" rx="2.4" />
              <path d="M9 4v16" />
              <path d="M14 9l3 3-3 3" />
            </svg>
          </button>
        ) : null}
        <div className="student-titlebar-spacer" />
        <div className="student-titlebar-actions no-drag">
          <button
            type="button"
            className="student-titlebar-button"
            onClick={openLeaderboard}
            title="排行榜"
            aria-label="打开排行榜"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
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
            type="button"
            className="student-titlebar-button"
            onClick={() => setActiveFullscreenView("achievements")}
            title="成就图鉴"
            aria-label="打开成就页面"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
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
            type="button"
            className="student-titlebar-button ui-theme-toggle"
            data-theme={theme}
            onClick={toggleTheme}
            title={nextThemeLabel}
            aria-label={nextThemeLabel}
          >
            <svg
              className="ui-theme-toggle-icon ui-theme-toggle-icon--sun"
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M12 3v2.2M12 18.8V21M4.6 4.6l1.6 1.6M17.8 17.8l1.6 1.6M3 12h2.2M18.8 12H21M4.6 19.4l1.6-1.6M17.8 6.2l1.6-1.6" />
              <circle cx="12" cy="12" r="4.2" />
            </svg>
            <svg
              className="ui-theme-toggle-icon ui-theme-toggle-icon--moon"
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M20.4 14.5A8.5 8.5 0 1 1 9.5 3.6a6.8 6.8 0 0 0 10.9 10.9z" />
            </svg>
          </button>

          <button
            type="button"
            className="student-titlebar-button"
            onClick={toggleMetricsPanel}
            title={rightPanelLabel}
            aria-label={rightPanelLabel}
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <rect x="3.5" y="4" width="17" height="16" rx="2.4" />
              <path d="M14.5 4v16" />
              <path d={isMetricsPanelVisible ? "M17.5 9l-3 3 3 3" : "M14.5 9l3 3-3 3"} />
            </svg>
          </button>

          <div className="student-window-controls" aria-label="窗口控制">
            <button
              type="button"
              onClick={() => api?.minimize()}
              className="ui-window-button ui-window-traffic ui-window-minimize student-titlebar-traffic"
              title="最小化"
              aria-label="最小化"
            >
              <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
            </button>
            <button
              type="button"
              onClick={() => api?.maximize()}
              className="ui-window-button ui-window-traffic ui-window-maximize student-titlebar-traffic"
              title="最大化"
              aria-label="最大化"
            >
              <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
            </button>
            <button
              type="button"
              onClick={() => api?.close()}
              className="ui-window-button ui-window-traffic ui-window-close student-titlebar-traffic"
              title="关闭"
              aria-label="关闭"
            >
              <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
            </button>
          </div>
        </div>
    </header>
  );
}
