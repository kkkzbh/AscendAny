import { useSettingsStore } from "@/stores/settingsStore";

export function TitleBar() {
  const openSettings = useSettingsStore((s) => s.openSettings);
  const api = window.electronAPI;
  const isMac = api?.platform === "darwin";

  return (
    <header className="drag-region flex h-11 shrink-0 items-center justify-between pr-4 pl-6">
      <div className="flex items-center gap-2">
        <span
          className={`text-sm font-semibold tracking-wide text-[var(--text-primary)] ${isMac ? "pl-16" : ""}`}
        >
          AscendAny
        </span>
        <span className="rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-[10px] font-medium text-[var(--accent)]">
          Beta
        </span>
      </div>

      <div className="no-drag flex items-center gap-0.5">
        <button
          onClick={openSettings}
          className="transition-all-smooth flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text-secondary)]"
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

        {!isMac && (
          <div className="ml-2 flex items-center">
            <button
              onClick={() => api?.minimize()}
              className="transition-all-smooth flex h-7 w-8 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text-secondary)]"
              title="最小化"
            >
              <svg width="10" height="10" viewBox="0 0 12 12">
                <rect x="2" y="5.5" width="8" height="1" rx="0.5" fill="currentColor" />
              </svg>
            </button>
            <button
              onClick={() => api?.maximize()}
              className="transition-all-smooth flex h-7 w-8 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text-secondary)]"
              title="最大化"
            >
              <svg width="10" height="10" viewBox="0 0 12 12">
                <rect x="2" y="2" width="8" height="8" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.2" />
              </svg>
            </button>
            <button
              onClick={() => api?.close()}
              className="transition-all-smooth flex h-7 w-8 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[#f43f5e]/10 hover:text-[#f43f5e]"
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
