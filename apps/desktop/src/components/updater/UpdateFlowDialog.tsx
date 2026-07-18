import { useEffect, useMemo, useRef, useState } from "react";

function formatPercent(value: number | null): string {
  if (typeof value !== "number" || Number.isNaN(value)) {
    return "0.00";
  }
  return value.toFixed(2);
}

type DialogMode = "confirm" | "progress" | null;

export function UpdateFlowDialog() {
  const [updateState, setUpdateState] = useState<UpdateStateSnapshot | null>(null);
  const [actionMessage, setActionMessage] = useState("");
  const [dismissedAvailableVersion, setDismissedAvailableVersion] = useState<string | null>(null);
  const [dismissedDownloadedVersion, setDismissedDownloadedVersion] = useState<string | null>(null);
  const [isStartingDownload, setIsStartingDownload] = useState(false);
  const [isInstalling, setIsInstalling] = useState(false);
  const [mounted, setMounted] = useState(false);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    const api = window.electronAPI;
    if (!api?.updaterGetState) {
      return;
    }

    let active = true;
    void api.updaterGetState().then((state) => {
      if (active) {
        setUpdateState(state);
      }
    }).catch(() => {
      if (active) {
        setActionMessage("当前环境暂不支持自动更新。");
      }
    });

    const unlisten = api.updaterOnStateChanged?.((state) => {
      setUpdateState(state);
      setActionMessage("");
      if (state.status !== "downloading") {
        setIsStartingDownload(false);
      }
      if (state.status !== "downloaded") {
        setIsInstalling(false);
      }
    });
    return () => {
      active = false;
      unlisten?.();
    };
  }, []);

  const dialogMode = useMemo<DialogMode>(() => {
    if (!updateState) {
      return null;
    }
    const versionKey = updateState.latestVersion ?? "__unknown__";
    if (updateState.status === "downloading") {
      return "progress";
    }
    if (updateState.status === "downloaded") {
      if (dismissedDownloadedVersion !== versionKey) {
        return "progress";
      }
      return null;
    }
    if (updateState.status === "available") {
      if (dismissedAvailableVersion !== versionKey) {
        return "confirm";
      }
    }
    return null;
  }, [dismissedAvailableVersion, dismissedDownloadedVersion, updateState]);

  useEffect(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
    if (!dialogMode) {
      setMounted(false);
      return;
    }
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      setMounted(true);
    });
    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [dialogMode]);

  useEffect(() => {
    if (!dialogMode) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) {
        return;
      }
      if (event.key !== "Escape") {
        return;
      }
      if (dialogMode === "confirm") {
        const versionKey = updateState?.latestVersion ?? "__unknown__";
        setDismissedAvailableVersion(versionKey);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [dialogMode, updateState?.latestVersion]);

  async function onStartDownload() {
    const api = window.electronAPI;
    if (!api?.updaterStartDownload) {
      setActionMessage("当前环境暂不支持自动更新。");
      return;
    }
    setIsStartingDownload(true);
    const versionKey = updateState?.latestVersion ?? "__unknown__";
    setDismissedDownloadedVersion((prev) => (prev === versionKey ? null : prev));
    const result = await api.updaterStartDownload();
    setActionMessage(result.message);
    if (!result.success) {
      setIsStartingDownload(false);
    }
  }

  async function onQuitAndInstall() {
    const api = window.electronAPI;
    if (!api?.updaterQuitAndInstall) {
      setActionMessage("当前环境暂不支持自动更新。");
      return;
    }
    setIsInstalling(true);
    const result = await api.updaterQuitAndInstall();
    setActionMessage(result.message);
    if (!result.success) {
      setIsInstalling(false);
    }
  }

  if (!dialogMode || !updateState) {
    return null;
  }

  const progressPercent = updateState.progressPercent ?? 0;
  const latestVersionLabel = updateState.latestVersion ?? "未知";
  const isDownloading = updateState.status === "downloading";
  const isDownloaded = updateState.status === "downloaded";

  return (
    <div className="fixed inset-0 z-[80] flex items-center justify-center p-4">
      <div className={`update-dialog-backdrop absolute inset-0 transition-opacity duration-300 ${mounted ? "opacity-100" : "opacity-0"}`} />
      <section
        className={`update-dialog relative z-10 w-[500px] max-w-[94vw] transition-all duration-300 ${
          mounted ? "scale-100 opacity-100" : "scale-95 opacity-0"
        }`}
        style={{ transitionTimingFunction: "var(--ease-spring)" }}
      >
        <div className="update-dialog-topline" />
        <header className="update-dialog-header">
          <div className="update-dialog-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 3v10" />
              <path d="m8.5 9.5 3.5 3.5 3.5-3.5" />
              <path d="M4 15.5v1A2.5 2.5 0 0 0 6.5 19h11a2.5 2.5 0 0 0 2.5-2.5v-1" />
            </svg>
          </div>
          <div className="min-w-0">
            <h3 className="text-[20px] font-semibold leading-tight text-[var(--text-strong)]">
              {dialogMode === "confirm" ? "发现新版本" : "正在更新客户端"}
            </h3>
            <p className="mt-1 text-[12px] text-[var(--text-soft)]">
              当前版本 {updateState.currentVersion}，最新版本 {latestVersionLabel}
            </p>
          </div>
        </header>

        <div className="update-dialog-body">
          {dialogMode === "confirm" && (
            <>
              <p className="text-[13px] leading-6 text-[var(--text-muted)]">
                检测到新版本，是否现在开始更新？确认后将进入下载阶段并显示实时进度。
              </p>
              {(actionMessage || updateState.message) && (
                <p className="update-dialog-hint">{actionMessage || updateState.message}</p>
              )}
              <div className="update-dialog-actions">
                <button
                  type="button"
                  className="update-dialog-btn update-dialog-btn-secondary"
                  onClick={() => {
                    const versionKey = updateState.latestVersion ?? "__unknown__";
                    setDismissedAvailableVersion(versionKey);
                  }}
                >
                  稍后更新
                </button>
                <button
                  type="button"
                  className="update-dialog-btn update-dialog-btn-primary"
                  onClick={() => void onStartDownload()}
                  disabled={isStartingDownload}
                >
                  {isStartingDownload ? "准备下载..." : "立即更新"}
                </button>
              </div>
            </>
          )}

          {dialogMode === "progress" && (
            <>
              <div className="update-progress-card">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-[13px] font-medium text-[var(--text-strong)]">
                    {isDownloaded ? "下载完成" : "下载进度"}
                  </span>
                  <span className="text-[12px] text-[var(--text-soft)]">
                    {isDownloaded ? "100.00%" : `${formatPercent(progressPercent)}%`}
                  </span>
                </div>
                <div className="update-progress-track mt-3">
                  <div
                    className="update-progress-fill"
                    style={{
                      width: `${isDownloaded ? 100 : Math.max(0, Math.min(100, progressPercent))}%`,
                    }}
                  />
                </div>
              </div>
              {(actionMessage || updateState.message) && (
                <p className="update-dialog-hint">{actionMessage || updateState.message}</p>
              )}
              <div className="update-dialog-actions">
                {isDownloaded ? (
                  <>
                    <button
                      type="button"
                      className="update-dialog-btn update-dialog-btn-secondary"
                      onClick={() => {
                        const versionKey = updateState.latestVersion ?? "__unknown__";
                        setDismissedDownloadedVersion(versionKey);
                      }}
                    >
                      稍后重启
                    </button>
                    <button
                      type="button"
                      className="update-dialog-btn update-dialog-btn-primary"
                      onClick={() => void onQuitAndInstall()}
                      disabled={isInstalling}
                    >
                      {isInstalling ? "重启中..." : "重启并更新"}
                    </button>
                  </>
                ) : (
                  <button
                    type="button"
                    className="update-dialog-btn update-dialog-btn-primary w-full"
                    disabled
                  >
                    {isDownloading ? "正在下载，请稍候..." : "准备下载..."}
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      </section>
    </div>
  );
}
