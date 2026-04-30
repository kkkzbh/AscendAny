import { useState, useEffect, useRef, useMemo, type ChangeEvent } from "react";
import {
  DEFAULT_ZOOM_PERCENT,
  ZOOM_PERCENT_MAX,
  ZOOM_PERCENT_MIN,
  ZOOM_PERCENT_STEP,
} from "@/types/settings";
import {
  DEFAULT_ROLE_ID,
  getAllRoles,
} from "@/types/role";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import { AvatarCropper } from "@/components/settings/AvatarCropper";
import { FeedbackSettingsPage } from "@/components/settings/FeedbackSettingsPage";

type SettingsPage = "general" | "role" | "feedback";

const NAV_ITEMS: { key: SettingsPage; label: string; icon: string }[] = [
  {
    key: "general",
    label: "通用",
    icon: "M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4",
  },
  {
    key: "role",
    label: "角色",
    icon: "M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 3a4 4 0 100 8 4 4 0 000-8z",
  },
  {
    key: "feedback",
    label: "反馈",
    icon: "M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z",
  },
];

const UPDATE_STATUS_LABEL: Record<UpdateStatus, string> = {
  idle: "待检查",
  checking: "检查中",
  available: "发现新版本",
  downloading: "下载中",
  downloaded: "下载完成",
  up_to_date: "已是最新",
  error: "检查失败",
  disabled: "不可用",
};

function formatUpdateTime(value: string | null): string {
  if (!value) {
    return "尚未检查";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "尚未检查";
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

export function SettingsSidebar({
  active,
  onSelect,
}: {
  active: SettingsPage;
  onSelect: (page: SettingsPage) => void;
}) {
  return (
    <nav className="settings-nav">
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

function SettingsWindowControls() {
  const api = window.electronAPI;

  return (
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
  );
}

function GeneralSettingsPage() {
  const account = useAuthStore((s) => s.account);
  const authError = useAuthStore((s) => s.error);
  const profileSaving = useAuthStore((s) => s.profileSaving);
  const bootstrapLocalPassword = useAuthStore((s) => s.bootstrapLocalPassword);
  const updateProfile = useAuthStore((s) => s.updateProfile);
  const clearAuthError = useAuthStore((s) => s.clearError);
  const logout = useAuthStore((s) => s.logout);
  const closeSettings = useSettingsStore((s) => s.closeSettings);
  const [loggingOut, setLoggingOut] = useState(false);

  async function onLogout() {
    if (loggingOut) {
      return;
    }
    setLoggingOut(true);
    try {
      closeSettings();
      await logout();
    } finally {
      setLoggingOut(false);
    }
  }

  const useOpaqueSidebarBackground = useSettingsStore((s) => s.useOpaqueSidebarBackground);
  const setOpaqueSidebarBackground = useSettingsStore((s) => s.setOpaqueSidebarBackground);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const saveAvatar = useAvatarStore((s) => s.saveAvatar);
  const deleteAvatar = useAvatarStore((s) => s.deleteAvatar);
  const zoomPercent = useSettingsStore((s) => s.zoomPercent);
  const setZoomPercent = useSettingsStore((s) => s.setZoomPercent);
  const [showCropper, setShowCropper] = useState(false);
  const [displayNameInput, setDisplayNameInput] = useState(account?.displayName ?? "");
  const [displayNameSaved, setDisplayNameSaved] = useState(false);
  const [localPasswordInput, setLocalPasswordInput] = useState("");
  const [localPasswordConfirmInput, setLocalPasswordConfirmInput] = useState("");
  const [localPasswordSaving, setLocalPasswordSaving] = useState(false);
  const [localPasswordSaved, setLocalPasswordSaved] = useState(false);
  const [updateState, setUpdateState] = useState<UpdateStateSnapshot | null>(null);
  const [updateActionMessage, setUpdateActionMessage] = useState("");

  useEffect(() => {
    setDisplayNameInput(account?.displayName ?? "");
    setDisplayNameSaved(false);
  }, [account?.accountId, account?.displayName]);

  useEffect(() => {
    setLocalPasswordInput("");
    setLocalPasswordConfirmInput("");
    setLocalPasswordSaved(false);
    setLocalPasswordSaving(false);
  }, [account?.accountId, account?.localPasswordEnabled]);

  async function onAvatarCropConfirm(dataUrl: string) {
    if (account?.accountId) {
      await saveAvatar(account.accountId, dataUrl);
    }
    setShowCropper(false);
  }

  async function onAvatarRemove() {
    if (account?.accountId) {
      await deleteAvatar(account.accountId);
    }
  }

  const normalizedDisplayName = displayNameInput.trim();
  const currentDisplayName = account?.displayName?.trim() ?? "";
  const canSaveDisplayName =
    Boolean(account?.accountId) &&
    Boolean(normalizedDisplayName) &&
    normalizedDisplayName !== currentDisplayName &&
    !profileSaving;

  async function onDisplayNameSave() {
    if (!canSaveDisplayName) {
      return;
    }
    clearAuthError();
    setDisplayNameSaved(false);
    try {
      await updateProfile({ displayName: normalizedDisplayName });
      setDisplayNameSaved(true);
    } catch {
      // Error text is from auth store.
    }
  }

  const shouldShowLocalPasswordBootstrap =
    account?.provisionSource === "external_sso" &&
    !account.localPasswordEnabled;
  const trimmedLocalPassword = localPasswordInput.trim();
  const canBootstrapLocalPassword =
    shouldShowLocalPasswordBootstrap &&
    trimmedLocalPassword.length >= 8 &&
    trimmedLocalPassword === localPasswordConfirmInput.trim() &&
    !localPasswordSaving;

  async function onBootstrapLocalPassword() {
    if (!canBootstrapLocalPassword) {
      return;
    }
    clearAuthError();
    setLocalPasswordSaved(false);
    setLocalPasswordSaving(true);
    try {
      await bootstrapLocalPassword(trimmedLocalPassword);
      setLocalPasswordSaved(true);
      setLocalPasswordInput("");
      setLocalPasswordConfirmInput("");
    } catch {
      // Error text comes from auth store.
    } finally {
      setLocalPasswordSaving(false);
    }
  }

  useEffect(() => {
    const api = window.electronAPI;
    if (!api?.updaterGetState) {
      return;
    }

    let active = true;
    void api.updaterGetState().then((state) => {
      if (!active) {
        return;
      }
      setUpdateState(state);
    }).catch(() => {
      if (!active) {
        return;
      }
      setUpdateActionMessage("当前环境暂不支持自动更新。");
    });

    const unlisten = api.updaterOnStateChanged?.((state) => {
      setUpdateState(state);
      setUpdateActionMessage("");
    });
    return () => {
      active = false;
      unlisten?.();
    };
  }, []);

  async function onCheckForUpdates() {
    const api = window.electronAPI;
    if (!api?.updaterCheckNow) {
      setUpdateActionMessage("当前环境暂不支持自动更新。");
      return;
    }
    const result = await api.updaterCheckNow();
    setUpdateActionMessage(result.message);
  }

  async function onQuitAndInstall() {
    const api = window.electronAPI;
    if (!api?.updaterQuitAndInstall) {
      setUpdateActionMessage("当前环境暂不支持自动更新。");
      return;
    }
    const result = await api.updaterQuitAndInstall();
    setUpdateActionMessage(result.message);
  }

  async function onStartDownload() {
    const api = window.electronAPI;
    if (!api?.updaterStartDownload) {
      setUpdateActionMessage("当前环境暂不支持自动更新。");
      return;
    }
    const result = await api.updaterStartDownload();
    setUpdateActionMessage(result.message);
  }

  const canCheckUpdates = updateState?.status !== "checking" && updateState?.status !== "downloading";
  const canStartDownload = updateState?.status === "available";
  const canInstallUpdates = updateState?.status === "downloaded";
  const updateProgress = updateState?.progressPercent;

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">通用设置</h2>

      {/* Avatar section */}
      <div className="settings-group">
        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            头像
          </label>
          <div className="flex w-full items-center gap-4">
            <div className="flex items-center gap-4">
              <div
                className="avatar-edit-wrapper group relative cursor-pointer"
                onClick={() => setShowCropper(true)}
              >
                <AvatarDisplay
                  size={72}
                  avatarUrl={avatarUrl}
                  username={account?.displayName ?? account?.username ?? ""}
                />
                <div className="absolute inset-0 flex items-center justify-center rounded-full bg-black/0 transition-all duration-200 group-hover:bg-black/40">
                  <svg
                    width="20"
                    height="20"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="white"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    className="opacity-0 transition-opacity duration-200 group-hover:opacity-100"
                  >
                    <path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" />
                    <circle cx="12" cy="13" r="4" />
                  </svg>
                </div>
              </div>
              <div className="flex flex-col gap-1.5">
                <button
                  className="text-left text-[13px] font-medium text-[var(--accent-600)] hover:underline"
                  onClick={() => setShowCropper(true)}
                >
                  更换头像
                </button>
                {avatarUrl && (
                  <button
                    className="text-left text-[12px] text-[var(--text-soft)] hover:text-[var(--rating-negative)] hover:underline"
                    onClick={() => void onAvatarRemove()}
                  >
                    移除头像
                  </button>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={() => void onLogout()}
              disabled={loggingOut}
              aria-label="退出登录"
              className={`settings-provider-pill ml-auto font-medium tracking-[0.04em] transition-colors ${
                loggingOut
                  ? "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
                  : "bg-[var(--rating-negative)] text-white shadow-[0_8px_16px_rgba(220,38,38,0.22)] hover:opacity-90"
              }`}
            >
              {loggingOut ? "退出中..." : "退出登录"}
            </button>
          </div>
        </div>
      </div>

      <div className="settings-group">
        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            左侧栏背景
          </label>
          <div className="flex items-center justify-between gap-4">
            <div className="grid gap-0.5">
              <p className="text-[13px] font-semibold leading-none text-[var(--text-strong)]">
                使用不透明左侧栏背景
              </p>
            </div>
            <button
              type="button"
              role="switch"
              aria-label="使用不透明左侧栏背景"
              aria-checked={useOpaqueSidebarBackground}
              onClick={() => {
                const next = !useOpaqueSidebarBackground;
                setOpaqueSidebarBackground(next);
                const api = window.electronAPI;
                if (api?.setOpaqueSidebarBackground) {
                  void api.setOpaqueSidebarBackground(next);
                }
              }}
              className={`relative h-6 w-11 shrink-0 rounded-full transition-colors ${
                useOpaqueSidebarBackground
                  ? "bg-[var(--accent-600)]"
                  : "bg-[var(--surface-soft)] ring-1 ring-[var(--border-subtle)]"
              }`}
              title={useOpaqueSidebarBackground ? "已开启不透明左侧栏背景" : "已关闭不透明左侧栏背景"}
            >
              <span
                className={`absolute left-[2px] top-[2px] h-5 w-5 rounded-full bg-white shadow transition-transform ${
                  useOpaqueSidebarBackground ? "translate-x-5" : "translate-x-0"
                }`}
              />
            </button>
          </div>
        </div>

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            界面缩放
          </label>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setZoomPercent(zoomPercent - ZOOM_PERCENT_STEP)}
              className="rounded-lg border border-[var(--border-subtle)] bg-[var(--surface-raised)] px-3 py-2 text-sm text-[var(--text-strong)] transition-colors hover:bg-[var(--surface-hover)]"
              aria-label="缩小界面"
            >
              -
            </button>
            <input
              type="range"
              min={ZOOM_PERCENT_MIN}
              max={ZOOM_PERCENT_MAX}
              step={ZOOM_PERCENT_STEP}
              value={zoomPercent}
              onChange={(event) => setZoomPercent(Number(event.target.value))}
              className="h-2 w-full cursor-pointer accent-[var(--accent-600)]"
            />
            <button
              type="button"
              onClick={() => setZoomPercent(zoomPercent + ZOOM_PERCENT_STEP)}
              className="rounded-lg border border-[var(--border-subtle)] bg-[var(--surface-raised)] px-3 py-2 text-sm text-[var(--text-strong)] transition-colors hover:bg-[var(--surface-hover)]"
              aria-label="放大界面"
            >
              +
            </button>
            <p className="w-14 text-right text-sm font-medium text-[var(--text-strong)]">
              {zoomPercent}%
            </p>
          </div>
          <div className="flex items-center justify-end">
            {zoomPercent !== DEFAULT_ZOOM_PERCENT && (
              <button
                type="button"
                onClick={() => setZoomPercent(DEFAULT_ZOOM_PERCENT)}
                className="text-[12px] font-medium text-[var(--accent-600)] hover:underline"
              >
                恢复 100%
              </button>
            )}
          </div>
        </div>

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            版本与更新
          </label>
          <div className="flex items-center gap-2">
            <div className="settings-input flex cursor-default items-center justify-between gap-3">
              <span className="text-[13px] font-medium text-[var(--text-strong)]">
                当前版本：{updateState?.currentVersion ?? "未知"}
              </span>
              <span className="text-[12px] text-[var(--text-soft)]">
                状态：{UPDATE_STATUS_LABEL[updateState?.status ?? "disabled"]}
              </span>
            </div>
            <button
              type="button"
              onClick={() => void onCheckForUpdates()}
              disabled={!canCheckUpdates}
              className={`settings-provider-pill px-4 ${
                canCheckUpdates
                  ? "bg-[var(--surface-raised)] text-[var(--text-strong)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
                  : "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
              }`}
            >
              检查更新
            </button>
            {canStartDownload && (
              <button
                type="button"
                onClick={() => void onStartDownload()}
                className="settings-provider-pill bg-[var(--surface-raised)] px-4 font-medium text-[var(--text-strong)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
              >
                立即更新
              </button>
            )}
            {canInstallUpdates && (
              <button
                type="button"
                onClick={() => void onQuitAndInstall()}
                className="settings-provider-pill bg-[var(--accent-600)] px-4 font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)] hover:opacity-90"
              >
                重启并更新
              </button>
            )}
          </div>
          {(() => {
            const parts = [
              `上次检查 ${formatUpdateTime(updateState?.lastCheckedAt ?? null)}`,
              updateState?.latestVersion ? `最新版本 ${updateState.latestVersion}` : null,
              updateActionMessage || updateState?.message || null,
            ].filter(Boolean) as string[];
            return (
              <p className="mt-1 text-[11px] leading-relaxed text-[var(--text-soft)]">
                {parts.join(" · ")}
              </p>
            );
          })()}
          {typeof updateProgress === "number" && (
            <div className="mt-1 flex items-center gap-2">
              <div className="h-1 flex-1 overflow-hidden rounded-full bg-[var(--surface-soft)]">
                <div
                  className="h-full rounded-full bg-[var(--accent-600)] transition-[width] duration-200"
                  style={{ width: `${Math.max(0, Math.min(100, updateProgress))}%` }}
                />
              </div>
              <span className="text-[11px] tabular-nums text-[var(--text-soft)]">
                {updateProgress.toFixed(1)}%
              </span>
            </div>
          )}
        </div>

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            用户名
          </label>
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={displayNameInput}
              onChange={(event) => {
                setDisplayNameInput(event.target.value);
                setDisplayNameSaved(false);
                clearAuthError();
              }}
              className="settings-input"
              placeholder="4-32 位字母 / 数字 / 下划线"
              maxLength={32}
            />
            <button
              type="button"
              onClick={() => void onDisplayNameSave()}
              disabled={!canSaveDisplayName}
              className={`settings-provider-pill px-4 ${
                canSaveDisplayName
                  ? "bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)] hover:opacity-90"
                  : "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
              }`}
            >
              {profileSaving ? "保存中..." : "保存"}
            </button>
          </div>
          {displayNameSaved && (
            <p className="mt-1 text-[11px] text-[var(--rating-positive)]">
              用户名已更新。
            </p>
          )}
          {authError && (
            <p className="mt-1 text-[11px] text-[var(--rating-negative)]">
              {authError}
            </p>
          )}
        </div>

        {shouldShowLocalPasswordBootstrap && (
          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              客户端登录密码
            </label>
            <div className="grid gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-soft)]/55 px-5 py-5 sm:px-6 sm:py-5">
              <p className="text-[13px] leading-6 text-[var(--text-soft)]">
                当前账号来自外部单点登录。设置本地密码后，可直接在桌面客户端继续登录该账号。
              </p>
              <input
                type="password"
                value={localPasswordInput}
                onChange={(event) => {
                  setLocalPasswordInput(event.target.value);
                  setLocalPasswordSaved(false);
                  clearAuthError();
                }}
                className="settings-input"
                placeholder="至少 8 位"
                autoComplete="new-password"
              />
              <input
                type="password"
                value={localPasswordConfirmInput}
                onChange={(event) => {
                  setLocalPasswordConfirmInput(event.target.value);
                  setLocalPasswordSaved(false);
                  clearAuthError();
                }}
                className="settings-input"
                placeholder="再次输入密码"
                autoComplete="new-password"
              />
              <div className="flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => void onBootstrapLocalPassword()}
                  disabled={!canBootstrapLocalPassword}
                  className={`settings-provider-pill px-4 ${
                    canBootstrapLocalPassword
                      ? "bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)] hover:opacity-90"
                      : "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
                  }`}
                >
                  {localPasswordSaving ? "启用中..." : "设置本地密码"}
                </button>
                {localPasswordInput.trim() !== localPasswordConfirmInput.trim() &&
                  localPasswordConfirmInput.trim() && (
                    <span className="text-[11px] text-[var(--rating-negative)]">
                      两次输入的密码不一致。
                    </span>
                  )}
              </div>
              {localPasswordSaved && (
                <p className="text-[11px] text-[var(--rating-positive)]">
                  已启用本地密码，可用于桌面客户端登录。
                </p>
              )}
              {authError && (
                <p className="text-[11px] text-[var(--rating-negative)]">
                  {authError}
                </p>
              )}
            </div>
          </div>
        )}

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            学号
          </label>
          <p className="settings-readonly-pill text-sm text-[var(--text-strong)]">
            {account?.studentId?.trim() || "未绑定"}
          </p>
        </div>

        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            PTA 账号昵称
          </label>
          <p className="settings-readonly-pill text-sm text-[var(--text-strong)]">
            {account?.ptaNickname?.trim() || "未绑定"}
          </p>
        </div>

      </div>

      {showCropper && (
        <AvatarCropper
          onConfirm={(dataUrl) => void onAvatarCropConfirm(dataUrl)}
          onCancel={() => setShowCropper(false)}
        />
      )}
    </div>
  );
}

function RoleSettingsPage({
  onCustomRoleDialogVisibilityChange,
}: {
  onCustomRoleDialogVisibilityChange: (visible: boolean) => void;
}) {
  const activeRole = useSettingsStore((s) => s.activeRole);
  const theme = useSettingsStore((s) => s.theme);
  const setActiveRole = useSettingsStore((s) => s.setActiveRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const saveCustomRole = useCustomRoleStore((s) => s.saveCustomRole);
  const removeCustomRole = useCustomRoleStore((s) => s.removeCustomRole);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [showCustomRoleDialog, setShowCustomRoleDialog] = useState(false);
  const [editingRoleId, setEditingRoleId] = useState<string | null>(null);
  const [customRoleName, setCustomRoleName] = useState("");
  const [customRoleAvatar, setCustomRoleAvatar] = useState("");
  const [customRolePrompt, setCustomRolePrompt] = useState("");

  const allRoles = useMemo(() => getAllRoles(customRoles), [customRoles]);
  const customRoleIdSet = useMemo(
    () => new Set(customRoles.map((role) => role.id)),
    [customRoles],
  );

  useEffect(() => {
    if (!allRoles.some((role) => role.id === activeRole)) {
      setActiveRole(DEFAULT_ROLE_ID);
    }
  }, [allRoles, activeRole, setActiveRole]);

  useEffect(() => {
    if (!showCustomRoleDialog) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        setShowCustomRoleDialog(false);
      }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => {
      window.removeEventListener("keydown", onKeyDown, true);
    };
  }, [showCustomRoleDialog]);

  useEffect(() => {
    onCustomRoleDialogVisibilityChange(showCustomRoleDialog);
    return () => {
      onCustomRoleDialogVisibilityChange(false);
    };
  }, [showCustomRoleDialog, onCustomRoleDialogVisibilityChange]);

  function resetCustomRoleForm() {
    setEditingRoleId(null);
    setCustomRoleName("");
    setCustomRoleAvatar("");
    setCustomRolePrompt("");
  }

  function onCloseCustomRoleDialog() {
    setShowCustomRoleDialog(false);
  }

  function onAvatarFileImport(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) {
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        setCustomRoleAvatar(reader.result);
      }
    };
    reader.readAsDataURL(file);
  }

  function onCreateCustomRole() {
    resetCustomRoleForm();
    setShowCustomRoleDialog(true);
  }

  function onEditCustomRole(roleId: string) {
    const role = customRoles.find((item) => item.id === roleId);
    if (!role) {
      return;
    }
    setEditingRoleId(role.id);
    setCustomRoleName(role.name);
    setCustomRoleAvatar(role.avatarUrl);
    setCustomRolePrompt(role.systemPromptExtra);
    setShowCustomRoleDialog(true);
  }

  function onSaveCustomRole() {
    const name = customRoleName.trim();
    const avatar = customRoleAvatar.trim();
    const prompt = customRolePrompt.trim();
    if (!name || !avatar || !prompt) {
      return;
    }

    const savedRoleId = saveCustomRole({
      id: editingRoleId ?? undefined,
      name,
      avatarUrl: avatar,
      systemPromptExtra: prompt,
    });
    setActiveRole(savedRoleId);
    setEditingRoleId(savedRoleId);
    setShowCustomRoleDialog(false);
  }

  function onDeleteCustomRole(roleId: string) {
    removeCustomRole(roleId);
    if (activeRole === roleId) {
      setActiveRole(DEFAULT_ROLE_ID);
    }
    if (editingRoleId === roleId) {
      resetCustomRoleForm();
      setShowCustomRoleDialog(false);
    }
  }

  const canSaveCustomRole = Boolean(
    customRoleName.trim() && customRoleAvatar.trim() && customRolePrompt.trim(),
  );

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">角色设置</h2>

      <div className="settings-group">
        <p className="text-[12px] leading-relaxed text-[var(--text-muted)]">
          选择 AI 助教角色；你也可以导入头像、昵称和系统提示词，创建本地自定义角色。
        </p>

        <div className="mt-3 flex flex-col gap-2.5">
          {allRoles.map((role) => {
            const isActive = role.id === activeRole;
            const isCustomRole = customRoleIdSet.has(role.id);
            return (
              <div
                key={role.id}
                role="button"
                tabIndex={0}
                onClick={() => setActiveRole(role.id)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    setActiveRole(role.id);
                  }
                }}
                className={`flex items-center gap-3.5 rounded-xl px-4 py-3 text-left transition-all duration-200 ${
                  isActive
                    ? "bg-[var(--accent-600)]/8 ring-2 ring-[var(--accent-600)] shadow-[0_4px_12px_rgba(3,105,161,0.12)]"
                    : "bg-[var(--surface-soft)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
                }`}
              >
                <img
                  src={role.avatarUrl}
                  alt={role.name}
                  className="h-10 w-10 shrink-0 rounded-full object-cover ring-1 ring-[var(--border-subtle)]"
                  style={
                    isActive && role.id === "xiaoD" && theme === "dark"
                      ? { filter: "brightness(0) invert(1)" }
                      : undefined
                  }
                />

                <div className="flex min-w-0 flex-col gap-0.5">
                  <div className="flex items-center gap-2">
                    <span
                      className={`truncate text-[13px] font-medium ${
                        isActive ? "text-[var(--accent-700)]" : "text-[var(--text-strong)]"
                      }`}
                    >
                      {role.name}
                    </span>
                    {role.id === "xiaoD" && (
                      <span className="rounded-md bg-[var(--accent-600)]/10 px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-600)]">
                        默认
                      </span>
                    )}
                    {isCustomRole && (
                      <span className="rounded-md bg-[var(--surface-soft)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]">
                        本地
                      </span>
                    )}
                  </div>
                  <span className="truncate text-[11px] text-[var(--text-soft)]">
                    {role.description}
                  </span>
                </div>

                <div className="ml-auto flex items-center gap-1.5">
                  {isCustomRole && (
                    <>
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation();
                          onEditCustomRole(role.id);
                        }}
                        className="rounded-md px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-600)] hover:bg-[var(--accent-600)]/8"
                      >
                        编辑
                      </button>
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation();
                          onDeleteCustomRole(role.id);
                        }}
                        className="rounded-md px-1.5 py-0.5 text-[10px] font-medium text-[var(--rating-negative)] hover:bg-[var(--rating-negative)]/10"
                      >
                        删除
                      </button>
                    </>
                  )}
                  {isActive && (
                    <svg
                      width="16"
                      height="16"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="var(--accent-600)"
                      strokeWidth="2.5"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    >
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  )}
                </div>
              </div>
            );
          })}

          <button
            type="button"
            onClick={onCreateCustomRole}
            className="flex items-center gap-3.5 rounded-xl bg-[var(--surface-soft)] px-4 py-3 text-left ring-1 ring-[var(--border-subtle)] transition-all duration-200 hover:bg-[var(--surface-hover)]"
          >
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-[var(--accent-600)]/10 text-[var(--accent-600)] ring-1 ring-[var(--accent-600)]/20">
              <svg
                width="18"
                height="18"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M12 5v14M5 12h14" />
              </svg>
            </div>
            <div className="min-w-0">
              <div className="text-[13px] font-medium text-[var(--text-strong)]">自定义</div>
              <p className="text-[11px] text-[var(--text-soft)]">
                导入头像、昵称和系统提示词，创建本地角色
              </p>
            </div>
          </button>
        </div>
        <p className="text-[11px] text-[var(--text-soft)]">
          自定义角色仅存储在本地设备，不按账号隔离，也不会上传头像文件。
        </p>
      </div>

      {showCustomRoleDialog && (
        <div className="fixed inset-0 z-[70] flex items-center justify-center p-5">
          <div
            className="absolute inset-0 bg-black/40 backdrop-blur-sm"
            onClick={onCloseCustomRoleDialog}
          />
          <div className="custom-role-dialog relative z-10 flex w-[620px] max-w-[92vw] flex-col overflow-hidden max-[720px]:max-h-[90vh]">
            <div className="custom-role-dialog-header flex items-center justify-between gap-3">
              <h3 className="settings-page-title custom-role-dialog-title mb-0 text-lg font-semibold text-[var(--text-strong)]">
                {editingRoleId ? "编辑自定义角色" : "创建自定义角色"}
              </h3>
              <button
                onClick={onCloseCustomRoleDialog}
                className="ui-window-button ui-window-traffic ui-window-close dialog-close-traffic shrink-0"
                aria-label="关闭自定义角色弹窗"
              >
                <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
              </button>
            </div>

            <div className="custom-role-dialog-body grid min-h-0 flex-1 gap-4 overflow-y-auto">
              <div className="settings-field rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-soft)]/55 p-4">
                <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
                  导入头像
                </label>
                <div className="mt-2 flex items-center gap-3">
                  <div className="h-[50px] w-[50px] shrink-0 overflow-hidden rounded-full border border-[var(--border-subtle)]/75 bg-[var(--surface-raised)]">
                    {customRoleAvatar ? (
                      <img
                        src={customRoleAvatar}
                        alt="自定义角色头像预览"
                        className="h-full w-full object-cover"
                      />
                    ) : (
                      <div className="flex h-full w-full items-center justify-center text-[11px] text-[var(--text-soft)]">
                        未导入
                      </div>
                    )}
                  </div>
                  <button
                    type="button"
                    onClick={() => fileInputRef.current?.click()}
                    className="h-9 rounded-full border border-[var(--border-subtle)] bg-[var(--surface-raised)] px-5 text-[12px] font-medium text-[var(--text-strong)] shadow-[0_2px_8px_rgba(15,23,42,0.06)] transition-colors hover:bg-[var(--surface-hover)]"
                  >
                    选择本地图片
                  </button>
                </div>
              </div>

              <div className="settings-field rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-soft)]/55 p-4">
                <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
                  角色昵称
                </label>
                <input
                  type="text"
                  value={customRoleName}
                  onChange={(event) => setCustomRoleName(event.target.value)}
                  placeholder="例如：竞赛教练"
                  className="settings-input mt-1 rounded-xl"
                />
              </div>

              <div className="settings-field rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-soft)]/55 p-4">
                <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
                  系统提示词
                </label>
                <textarea
                  value={customRolePrompt}
                  onChange={(event) => setCustomRolePrompt(event.target.value)}
                  placeholder="输入角色的系统提示词，描述语气、分析风格和回答要求"
                  rows={5}
                  className="settings-input mt-1 min-h-[150px] resize-y rounded-xl"
                />
              </div>
            </div>

            <div className="custom-role-dialog-footer border-t border-[var(--border-subtle)]/75">
              <div className="flex items-center justify-end">
                <button
                  type="button"
                  disabled={!canSaveCustomRole}
                  onClick={onSaveCustomRole}
                  className="h-10 min-w-[86px] rounded-full border border-[var(--accent-600)]/20 bg-[var(--accent-600)] px-6 text-[13px] font-semibold text-white shadow-[0_8px_18px_rgba(2,132,199,0.28)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-45"
                >
                  保存
                </button>
              </div>
            </div>

            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={onAvatarFileImport}
            />
          </div>
        </div>
      )}
    </div>
  );
}

export function SettingsWorkspace() {
  const isOpen = useSettingsStore((s) => s.isOpen);
  const closeSettings = useSettingsStore((s) => s.closeSettings);
  const [activePage, setActivePage] = useState<SettingsPage>("general");
  const [isCustomRoleDialogOpen, setIsCustomRoleDialogOpen] = useState(false);

  if (!isOpen) return null;

  return (
    <div className="settings-workspace">
      <aside className="settings-sidebar">
        <div className="settings-sidebar-top drag-region">
          <button
            type="button"
            onClick={closeSettings}
            className="settings-return-button no-drag"
            aria-label="返回应用"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="m15 18-6-6 6-6" />
              <path d="M21 12H9" />
            </svg>
            <span>返回应用</span>
          </button>
        </div>
        <SettingsSidebar active={activePage} onSelect={setActivePage} />
      </aside>

      <main className="settings-main">
        <header className="settings-titlebar drag-region">
          <div className="settings-titlebar-spacer" />
          {!isCustomRoleDialogOpen && (
            <div className="settings-titlebar-actions no-drag">
              <SettingsWindowControls />
            </div>
          )}
        </header>

        <div className="settings-content">
          <div className="settings-content-inner">
            {activePage === "general" && <GeneralSettingsPage />}
            {activePage === "role" && (
              <RoleSettingsPage onCustomRoleDialogVisibilityChange={setIsCustomRoleDialogOpen} />
            )}
            {activePage === "feedback" && <FeedbackSettingsPage />}
          </div>
        </div>
      </main>
    </div>
  );
}

export const SettingsDialog = SettingsWorkspace;
