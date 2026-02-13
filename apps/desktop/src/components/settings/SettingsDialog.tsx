import { useState, useEffect, useRef } from "react";
import {
  PROVIDER_ORDER,
} from "@/types/settings";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";

type SettingsPage = "general" | "model";

const NAV_ITEMS: { key: SettingsPage; label: string; icon: string }[] = [
  {
    key: "general",
    label: "通用",
    icon: "M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4",
  },
  {
    key: "model",
    label: "模型",
    icon: "M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z",
  },
];

export function SettingsSidebar({
  active,
  onSelect,
}: {
  active: SettingsPage;
  onSelect: (page: SettingsPage) => void;
}) {
  return (
    <nav className="settings-nav flex w-44 shrink-0 flex-col gap-1 max-[720px]:w-full max-[720px]:border-b max-[720px]:border-r-0">
      <span className="settings-nav-label text-[10px] font-semibold tracking-[0.12em] text-[var(--text-soft)] uppercase">
        设置
      </span>
      {NAV_ITEMS.map((item) => (
        <button
          key={item.key}
          onClick={() => onSelect(item.key)}
          className={`settings-nav-item flex items-center gap-2.5 rounded-lg text-left text-sm transition-all duration-200 ${
            active === item.key
              ? "is-active font-medium"
              : "text-[var(--text-muted)]"
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
  const account = useAuthStore((s) => s.account);
  const updateProfile = useAuthStore((s) => s.updateProfile);
  const profileSaving = useAuthStore((s) => s.profileSaving);
  const [studentId, setStudentId] = useState(account?.studentId ?? "");
  const [ptaNickname, setPtaNickname] = useState(account?.ptaNickname ?? "");
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    setStudentId(account?.studentId ?? "");
    setPtaNickname(account?.ptaNickname ?? "");
  }, [account?.studentId, account?.ptaNickname]);

  async function onSaveProfile() {
    setSaveError(null);
    try {
      await updateProfile({
        studentId: studentId.trim() || null,
        ptaNickname: ptaNickname.trim() || null,
      });
    } catch (error) {
      const message =
        error instanceof Error && error.message.trim()
          ? error.message
          : "保存失败，请稍后重试。";
      setSaveError(message);
    }
  }

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">通用设置</h2>

      <div className="settings-group">
        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            学号
          </label>
          <input
            type="text"
            value={studentId}
            onChange={(e) => setStudentId(e.target.value)}
            placeholder="输入你的学号"
            className="settings-input"
          />
        </div>

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            PTA 账号昵称
          </label>
          <input
            type="text"
            value={ptaNickname}
            onChange={(e) => setPtaNickname(e.target.value)}
            placeholder="输入你的 PTA 昵称"
            className="settings-input"
          />
        </div>

        <div className="settings-field">
          <button
            type="button"
            onClick={() => {
              void onSaveProfile();
            }}
            disabled={profileSaving}
            className="auth-submit w-[128px] disabled:opacity-50"
          >
            {profileSaving ? "保存中..." : "保存资料"}
          </button>
          <p className="text-[11px] text-[var(--text-soft)]">
            学号和 PTA 昵称保存在账号云端，登录后自动同步。
          </p>
          {saveError && (
            <p className="text-[11px] text-[var(--rating-negative)]">{saveError}</p>
          )}
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
  const isServerDefault = current?.usesServerConfig;

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">模型配置</h2>

      <div className="settings-field">
        <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
          模型提供商
        </label>
        <div className="flex flex-wrap gap-2">
          {PROVIDER_ORDER.map((providerType) => {
            const provider = providers[providerType];
            const isDisabled = !provider.enabled;
            return (
              <button
                key={providerType}
                onClick={() => setActiveProvider(providerType)}
                disabled={isDisabled}
                className={`rounded-lg px-4 py-2 text-sm transition-all duration-200 ${
                  activeProvider === providerType
                    ? "bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)]"
                    : "bg-[var(--surface-soft)] text-[var(--text-muted)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
                } ${isDisabled ? "cursor-not-allowed opacity-45 hover:bg-[var(--surface-soft)]" : ""}`}
              >
                {provider.label}
              </button>
            );
          })}
        </div>
      </div>

      {current && !isServerDefault && (
        <div className="settings-group animate-fade-in">
          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              Base URL
            </label>
            <input
              type="text"
              value={current.baseUrl}
              onChange={(e) =>
                updateProvider(activeProvider, { baseUrl: e.target.value })
              }
              className="settings-input"
            />
          </div>

          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              模型名称
            </label>
            <input
              type="text"
              value={current.model}
              onChange={(e) =>
                updateProvider(activeProvider, { model: e.target.value })
              }
              className="settings-input"
            />
          </div>

          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              API Key
            </label>
            <input
              type="password"
              value={current.apiKey}
              onChange={(e) =>
                updateProvider(activeProvider, { apiKey: e.target.value })
              }
              placeholder="sk-..."
              className="settings-input"
            />
            <p className="text-[11px] text-[var(--text-soft)]">
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
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }

    if (isOpen) {
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        setMounted(true);
      });
    } else {
      setMounted(false);
    }

    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeSettings();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [isOpen, closeSettings]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3">
      <div
        className={`absolute inset-0 bg-black/30 backdrop-blur-sm transition-opacity duration-300 ${mounted ? "opacity-100" : "opacity-0"}`}
        onClick={closeSettings}
      />

      <div
        className={`settings-dialog relative z-10 flex h-[500px] w-[680px] overflow-hidden rounded-2xl transition-all duration-300 max-[720px]:h-[92vh] max-[720px]:w-full max-[720px]:flex-col ${
          mounted ? "scale-100 opacity-100" : "scale-95 opacity-0"
        }`}
        style={{ transitionTimingFunction: "var(--ease-spring)" }}
      >
        <SettingsSidebar active={activePage} onSelect={setActivePage} />

        <div className="settings-content flex-1 overflow-y-auto">
          {activePage === "general" && <GeneralSettingsPage />}
          {activePage === "model" && <ModelSettingsPage />}
        </div>

        <button
          onClick={closeSettings}
          className="ui-icon-button absolute right-5 top-5"
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
