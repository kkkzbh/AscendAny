import { useState, useEffect } from "react";
import type { ProviderType } from "@/types/settings";
import { useSettingsStore } from "@/stores/settingsStore";

type SettingsPage = "general" | "model";

const NAV_ITEMS: { key: SettingsPage; label: string; icon: string }[] = [
  { key: "general", label: "通用", icon: "M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4" },
  { key: "model", label: "模型", icon: "M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" },
];

export function SettingsSidebar({
  active,
  onSelect,
}: {
  active: SettingsPage;
  onSelect: (page: SettingsPage) => void;
}) {
  return (
    <nav className="flex w-44 shrink-0 flex-col gap-1 border-r border-[var(--border)] p-3">
      <span className="mb-2 px-3 text-[10px] font-semibold tracking-widest text-[var(--text-muted)] uppercase">
        设置
      </span>
      {NAV_ITEMS.map((item) => (
        <button
          key={item.key}
          onClick={() => onSelect(item.key)}
          className={`transition-all-smooth flex items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm ${
            active === item.key
              ? "bg-[var(--accent-soft)] font-medium text-[var(--accent)] shadow-sm"
              : "text-[var(--text-secondary)] hover:bg-[var(--surface-hover)]"
          }`}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d={item.icon} />
          </svg>
          {item.label}
        </button>
      ))}
    </nav>
  );
}

function GeneralSettingsPage() {
  const studentId = useSettingsStore((s) => s.studentId);
  const apiBaseUrl = useSettingsStore((s) => s.apiBaseUrl);
  const setStudentId = useSettingsStore((s) => s.setStudentId);
  const setApiBaseUrl = useSettingsStore((s) => s.setApiBaseUrl);

  return (
    <div className="animate-fade-in space-y-6">
      <h2 className="text-lg font-semibold text-[var(--text-primary)]">通用设置</h2>

      <div className="space-y-5">
        <div className="space-y-1.5">
          <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
            学号
          </label>
          <input
            type="text"
            value={studentId}
            onChange={(e) => setStudentId(e.target.value)}
            placeholder="输入你的学号"
            className="focus-ring w-full rounded-lg border border-[var(--glass-border)] bg-[var(--glass-bg)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none backdrop-blur-sm placeholder:text-[var(--text-muted)] transition-all-smooth focus:border-[var(--accent-soft)]"
          />
        </div>

        <div className="space-y-1.5">
          <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
            API 服务地址
          </label>
          <input
            type="text"
            value={apiBaseUrl}
            onChange={(e) => setApiBaseUrl(e.target.value)}
            placeholder="http://127.0.0.1:8000"
            className="focus-ring w-full rounded-lg border border-[var(--glass-border)] bg-[var(--glass-bg)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none backdrop-blur-sm placeholder:text-[var(--text-muted)] transition-all-smooth focus:border-[var(--accent-soft)]"
          />
          <p className="text-[11px] text-[var(--text-muted)]">FastAPI 后端地址</p>
        </div>
      </div>
    </div>
  );
}

function ModelSettingsPage() {
  const activeProvider = useSettingsStore((s) => s.activeProvider);
  const providers = useSettingsStore((s) => s.providers);
  const setActiveProvider = useSettingsStore((s) => s.setActiveProvider);
  const updateProvider = useSettingsStore((s) => s.updateProvider);

  const current = providers[activeProvider];

  return (
    <div className="animate-fade-in space-y-6">
      <h2 className="text-lg font-semibold text-[var(--text-primary)]">模型配置</h2>

      {/* Provider selector */}
      <div className="space-y-2">
        <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
          模型提供商
        </label>
        <div className="flex gap-2">
          {(Object.keys(providers) as ProviderType[]).map((key) => (
            <button
              key={key}
              onClick={() => setActiveProvider(key)}
              className={`transition-all-smooth rounded-lg px-4 py-2 text-sm ${
                activeProvider === key
                  ? "bg-[var(--accent)] font-medium text-white shadow-md shadow-[var(--accent-glow)]"
                  : "border border-[var(--glass-border)] bg-[var(--glass-bg)] text-[var(--text-secondary)] backdrop-blur-sm hover:bg-[var(--surface-hover)]"
              }`}
            >
              {providers[key].label}
            </button>
          ))}
        </div>
      </div>

      {/* Config for active provider */}
      {current && (
        <div className="animate-fade-in space-y-5">
          <div className="space-y-1.5">
            <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
              Base URL
            </label>
            <input
              type="text"
              value={current.baseUrl}
              onChange={(e) =>
                updateProvider(activeProvider, { baseUrl: e.target.value })
              }
              className="focus-ring w-full rounded-lg border border-[var(--glass-border)] bg-[var(--glass-bg)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none backdrop-blur-sm placeholder:text-[var(--text-muted)] transition-all-smooth focus:border-[var(--accent-soft)]"
            />
          </div>

          <div className="space-y-1.5">
            <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
              模型名称
            </label>
            <input
              type="text"
              value={current.model}
              onChange={(e) =>
                updateProvider(activeProvider, { model: e.target.value })
              }
              className="focus-ring w-full rounded-lg border border-[var(--glass-border)] bg-[var(--glass-bg)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none backdrop-blur-sm placeholder:text-[var(--text-muted)] transition-all-smooth focus:border-[var(--accent-soft)]"
            />
          </div>

          <div className="space-y-1.5">
            <label className="block text-xs font-semibold tracking-wide text-[var(--text-muted)] uppercase">
              API Key
            </label>
            <input
              type="password"
              value={current.apiKey}
              onChange={(e) =>
                updateProvider(activeProvider, { apiKey: e.target.value })
              }
              placeholder="sk-..."
              className="focus-ring w-full rounded-lg border border-[var(--glass-border)] bg-[var(--glass-bg)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none backdrop-blur-sm placeholder:text-[var(--text-muted)] transition-all-smooth focus:border-[var(--accent-soft)]"
            />
            <p className="text-[11px] text-[var(--text-muted)]">
              密钥仅存储在本地，不会上传
            </p>
          </div>
        </div>
      )}
    </div>
  );
}

export function SettingsDialog() {
  const isOpen = useSettingsStore((s) => s.isOpen);
  const closeSettings = useSettingsStore((s) => s.closeSettings);
  const [activePage, setActivePage] = useState<SettingsPage>("general");
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    if (isOpen) {
      // Trigger entrance animation on next frame
      requestAnimationFrame(() => setMounted(true));
    } else {
      setMounted(false);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className={`absolute inset-0 bg-black/30 backdrop-blur-sm transition-opacity duration-300 ${mounted ? "opacity-100" : "opacity-0"}`}
        onClick={closeSettings}
      />

      {/* Dialog */}
      <div
        className={`relative z-10 flex h-[480px] w-[640px] overflow-hidden rounded-2xl border border-[var(--glass-border)] bg-[var(--glass-bg-strong)] shadow-2xl backdrop-blur-2xl transition-all duration-300 ${
          mounted
            ? "scale-100 opacity-100"
            : "scale-95 opacity-0"
        }`}
        style={{ transitionTimingFunction: "var(--ease-spring)" }}
      >
        <SettingsSidebar active={activePage} onSelect={setActivePage} />

        <div className="flex-1 overflow-y-auto p-6">
          {activePage === "general" && <GeneralSettingsPage />}
          {activePage === "model" && <ModelSettingsPage />}
        </div>

        {/* Close button */}
        <button
          onClick={closeSettings}
          className="transition-all-smooth absolute right-3 top-3 flex h-7 w-7 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text-primary)]"
        >
          <svg width="14" height="14" viewBox="0 0 14 14">
            <path
              d="M1 1l12 12M13 1L1 13"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
    </div>
  );
}
