import { useState, useEffect, useRef, useMemo, type ChangeEvent } from "react";
import {
  DEFAULT_ZOOM_PERCENT,
  PROVIDER_ORDER,
  type ProviderType,
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

type SettingsPage = "general" | "model" | "role";

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
  {
    key: "role",
    label: "角色",
    icon: "M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 3a4 4 0 100 8 4 4 0 000-8z",
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
  const authError = useAuthStore((s) => s.error);
  const profileSaving = useAuthStore((s) => s.profileSaving);
  const updateProfile = useAuthStore((s) => s.updateProfile);
  const clearAuthError = useAuthStore((s) => s.clearError);
  const useOpaqueWindowBackground = useSettingsStore((s) => s.useOpaqueWindowBackground);
  const setOpaqueWindowBackground = useSettingsStore((s) => s.setOpaqueWindowBackground);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const saveAvatar = useAvatarStore((s) => s.saveAvatar);
  const deleteAvatar = useAvatarStore((s) => s.deleteAvatar);
  const zoomPercent = useSettingsStore((s) => s.zoomPercent);
  const setZoomPercent = useSettingsStore((s) => s.setZoomPercent);
  const [showCropper, setShowCropper] = useState(false);
  const [displayNameInput, setDisplayNameInput] = useState(account?.displayName ?? "");
  const [displayNameSaved, setDisplayNameSaved] = useState(false);
  const [updateState, setUpdateState] = useState<UpdateStateSnapshot | null>(null);
  const [updateActionMessage, setUpdateActionMessage] = useState("");

  useEffect(() => {
    setDisplayNameInput(account?.displayName ?? "");
    setDisplayNameSaved(false);
  }, [account?.accountId, account?.displayName]);

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

  const canCheckUpdates = updateState?.status !== "checking" && updateState?.status !== "downloading";
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
              <p className="text-[11px] text-[var(--text-soft)]">
                头像仅存储在本地，不会上传至服务器。
              </p>
            </div>
          </div>
        </div>
      </div>

      <div className="settings-group">
        <div className="settings-field">
          <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
            窗口背景
          </label>
          <div className="flex items-center justify-between gap-4">
            <div className="grid gap-0.5">
              <p className="text-[13px] font-semibold leading-none text-[var(--text-strong)]">
                使用不透明窗口背景
              </p>
            </div>
            <button
              type="button"
              role="switch"
              aria-checked={useOpaqueWindowBackground}
              onClick={() => {
                const next = !useOpaqueWindowBackground;
                setOpaqueWindowBackground(next);
                const api = window.electronAPI;
                if (api?.setOpaqueWindowBackground) {
                  void api.setOpaqueWindowBackground(next);
                }
              }}
              className={`relative h-6 w-11 shrink-0 rounded-full transition-colors ${
                useOpaqueWindowBackground
                  ? "bg-[var(--accent-600)]"
                  : "bg-[var(--surface-soft)] ring-1 ring-[var(--border-subtle)]"
              }`}
              title={useOpaqueWindowBackground ? "已开启不透明背景" : "已关闭不透明背景"}
            >
              <span
                className={`absolute top-[2px] h-5 w-5 rounded-full bg-white shadow transition-transform ${
                  useOpaqueWindowBackground ? "translate-x-[22px]" : "translate-x-[2px]"
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
          <div className="flex items-center justify-between">
            <p className="text-[11px] text-[var(--text-soft)]">
              调整范围 {ZOOM_PERCENT_MIN}% - {ZOOM_PERCENT_MAX}%。
            </p>
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
          <div className="grid gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-soft)]/55 px-5 py-5 sm:px-6 sm:py-5">
            <div className="flex items-center justify-between gap-3 text-[12px] leading-5">
              <span className="font-medium text-[var(--text-strong)]">
                当前版本：{updateState?.currentVersion ?? "未知"}
              </span>
              <span className="text-[var(--text-soft)]">
                状态：{UPDATE_STATUS_LABEL[updateState?.status ?? "disabled"]}
              </span>
            </div>
            <p className="text-[11px] leading-relaxed text-[var(--text-soft)]">
              上次检查：{formatUpdateTime(updateState?.lastCheckedAt ?? null)}
            </p>
            {typeof updateProgress === "number" && (
              <p className="text-[11px] leading-relaxed text-[var(--text-soft)]">
                下载进度：{updateProgress.toFixed(2)}%
              </p>
            )}
            {updateState?.latestVersion && (
              <p className="text-[11px] leading-relaxed text-[var(--text-soft)]">
                最新版本：{updateState.latestVersion}
              </p>
            )}
            {(updateState?.message || updateActionMessage) && (
              <p className="text-[11px] leading-relaxed text-[var(--text-soft)]">
                {updateActionMessage || updateState?.message}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2 pt-1">
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
          </div>
          <p className="mt-1 text-[11px] text-[var(--text-soft)]">
            客户端每次启动后会自动检查更新。
          </p>
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

        <div className="settings-field">
          <p className="text-[11px] text-[var(--text-soft)]">
            用户名可随时修改；学号在注册后不可修改；PTA 昵称可由服务端按规则更新并回填历史提交。
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

function ModelSettingsPage() {
  const activeProvider = useSettingsStore((s) => s.activeProvider);
  const providers = useSettingsStore((s) => s.providers);
  const setActiveProvider = useSettingsStore((s) => s.setActiveProvider);
  const updateProvider = useSettingsStore((s) => s.updateProvider);
  const [pendingProvider, setPendingProvider] = useState<ProviderType | null>(null);
  const selectedProvider: ProviderType = pendingProvider ?? activeProvider;
  const selectedProviderConfig = providers[selectedProvider];
  const isSelectedServerDefault = selectedProviderConfig?.usesServerConfig;
  const canApplyPendingProvider = Boolean(
    pendingProvider &&
      pendingProvider !== activeProvider &&
      !providers[pendingProvider].usesServerConfig,
  );

  function onProviderSelect(providerType: ProviderType) {
    const provider = providers[providerType];
    const isTemporarilyUnlocked =
      providerType === "anthropic" || providerType === "deepseek" || providerType === "gemini";
    if (!provider?.enabled && !isTemporarilyUnlocked) {
      return;
    }
    if (provider.usesServerConfig) {
      setActiveProvider(providerType);
      setPendingProvider(null);
      return;
    }
    if (providerType === activeProvider) {
      setPendingProvider(null);
      return;
    }
    setPendingProvider(providerType);
  }

  function applyPendingProvider() {
    if (!pendingProvider || !canApplyPendingProvider) {
      return;
    }
    setActiveProvider(pendingProvider);
    setPendingProvider(null);
  }

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">模型配置</h2>

      <div className="settings-field">
        <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
          模型提供商
        </label>
        <div className="settings-provider-list">
          {PROVIDER_ORDER.map((providerType) => {
            const provider = providers[providerType];
            const isTemporarilyUnlocked =
              providerType === "anthropic" || providerType === "deepseek" || providerType === "gemini";
            const isDisabled = !provider.enabled && !isTemporarilyUnlocked;
            const isSelected = selectedProvider === providerType;
            return (
              <button
                key={providerType}
                onClick={() => onProviderSelect(providerType)}
                disabled={isDisabled}
                className={`settings-provider-pill transition-all duration-200 ${
                  isSelected
                    ? "bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)]"
                    : "bg-[var(--surface-soft)] text-[var(--text-muted)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
                } ${isDisabled ? "cursor-not-allowed opacity-45 hover:bg-[var(--surface-soft)]" : ""}`}
              >
                {provider.label}
              </button>
            );
          })}
        </div>
        <p className="mt-2 text-[11px] text-[var(--text-soft)]">
          如果对默认模型不满意，可切换至自定义模型。
        </p>
      </div>

      {selectedProviderConfig && !isSelectedServerDefault && (
        <div className="settings-group animate-fade-in">
          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              Base URL
            </label>
            <input
              type="text"
              value={selectedProviderConfig.baseUrl}
              onChange={(e) =>
                updateProvider(selectedProvider, { baseUrl: e.target.value })
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
              value={selectedProviderConfig.model}
              onChange={(e) =>
                updateProvider(selectedProvider, { model: e.target.value })
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
              value={selectedProviderConfig.apiKey}
              onChange={(e) =>
                updateProvider(selectedProvider, { apiKey: e.target.value })
              }
              placeholder="sk-..."
              className="settings-input"
            />
            <p className="text-[11px] text-[var(--text-soft)]">
              密钥仅存储在本地，不会上传
            </p>
          </div>

          {canApplyPendingProvider && (
            <div className="mt-1">
              <button
                type="button"
                onClick={applyPendingProvider}
                className="settings-provider-pill bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)] transition-opacity hover:opacity-90"
              >
                确认切换到 {selectedProviderConfig.label}
              </button>
            </div>
          )}
        </div>
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

export function SettingsDialog() {
  const isOpen = useSettingsStore((s) => s.isOpen);
  const closeSettings = useSettingsStore((s) => s.closeSettings);
  const [activePage, setActivePage] = useState<SettingsPage>("general");
  const [isCustomRoleDialogOpen, setIsCustomRoleDialogOpen] = useState(false);
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
      if (event.defaultPrevented) {
        return;
      }
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
          {activePage === "role" && (
            <RoleSettingsPage onCustomRoleDialogVisibilityChange={setIsCustomRoleDialogOpen} />
          )}
        </div>

        {!isCustomRoleDialogOpen && (
          <button
            onClick={closeSettings}
            className="ui-window-button ui-window-traffic ui-window-close dialog-close-traffic absolute right-5 top-5"
            aria-label="关闭设置"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
          </button>
        )}
      </div>
    </div>
  );
}
