import { useCallback, useEffect, useState } from "react";

const statusLabels: Record<UpdateStatus, string> = {
  idle: "等待检查",
  checking: "正在检查",
  available: "发现新版本",
  downloading: "正在下载",
  downloaded: "等待安装",
  up_to_date: "已是最新版本",
  error: "更新失败",
  disabled: "自动更新已禁用",
};

function formatTime(value: string | null): string {
  if (value === null) return "尚未检查";
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function UpdaterView() {
  const api = window.electronAPI;
  const updaterAvailable =
    api?.updaterGetState !== undefined
    && api.updaterCheckNow !== undefined
    && api.updaterOnStateChanged !== undefined;
  const [state, setState] = useState<UpdateStateSnapshot | null>(null);
  const [loading, setLoading] = useState(updaterAvailable);
  const [actionBusy, setActionBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!updaterAvailable || api?.updaterGetState === undefined) return;
    let active = true;
    void api.updaterGetState()
      .then((snapshot) => {
        if (active) setState(snapshot);
      })
      .catch((loadError: unknown) => {
        if (active) {
          setError(loadError instanceof Error ? loadError.message : "更新状态读取失败");
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    const unsubscribe = api.updaterOnStateChanged?.((snapshot) => {
      setState(snapshot);
      setActionBusy(false);
    });
    return () => {
      active = false;
      unsubscribe?.();
    };
  }, [api, updaterAvailable]);

  const runAction = useCallback(async (
    action: (() => Promise<UpdateActionResult>) | undefined,
  ) => {
    if (action === undefined) {
      setError("当前 Desktop build 未提供该更新能力");
      return;
    }
    setActionBusy(true);
    setError(null);
    setMessage(null);
    try {
      const result = await action();
      if (result.success) {
        setMessage(result.message);
      } else {
        setError(result.message);
      }
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "更新操作失败");
    } finally {
      setActionBusy(false);
    }
  }, []);

  if (!updaterAvailable) {
    return (
      <section className="state-panel">
        <div className="state-symbol" aria-hidden="true">↥</div>
        <h2>当前环境没有客户端更新能力</h2>
        <p>浏览器预览不会连接 Electron updater。</p>
      </section>
    );
  }
  if (loading) {
    return (
      <section className="state-panel" role="status">
        <span className="loading-dot" />
        <p>正在读取更新状态…</p>
      </section>
    );
  }
  if (state === null) {
    return (
      <section className="state-panel error-state">
        <h2>更新状态不可用</h2>
        <p>{error ?? "Electron updater 未返回状态"}</p>
      </section>
    );
  }

  const progress = Math.min(100, Math.max(0, state.progressPercent ?? 0));

  return (
    <div className="view-stack">
      <section className="update-hero">
        <span className="update-symbol" aria-hidden="true">↥</span>
        <div>
          <span className="eyebrow">ASCENDANY DESKTOP</span>
          <h2>{statusLabels[state.status]}</h2>
          <p>
            当前版本 {state.currentVersion}
            {state.latestVersion !== null ? " · 最新版本 " + state.latestVersion : ""}
          </p>
        </div>
      </section>

      <section className="panel-card">
        <header className="section-heading">
          <div>
            <span className="eyebrow">UPDATE STATUS</span>
            <h2>更新详情</h2>
            <p>上次检查：{formatTime(state.lastCheckedAt)}</p>
          </div>
          <button
            className="secondary-button"
            type="button"
            disabled={actionBusy || state.status === "checking" || state.status === "downloading"}
            onClick={() => void runAction(api?.updaterCheckNow)}
          >
            {state.status === "checking" ? "检查中…" : "检查更新"}
          </button>
        </header>

        {state.status === "downloading" || state.status === "downloaded" ? (
          <div className="update-progress">
            <div>
              <strong>{state.status === "downloaded" ? "下载完成" : "下载进度"}</strong>
              <span>{progress.toFixed(2)}%</span>
            </div>
            <div className="progress-track">
              <span style={{ width: String(progress) + "%" }} />
            </div>
          </div>
        ) : null}

        {state.message !== null ? <p className="update-message">{state.message}</p> : null}
        {message !== null ? <div className="form-success" role="status">{message}</div> : null}
        {error !== null ? <div className="form-error" role="alert">{error}</div> : null}

        <div className="update-actions">
          {state.status === "available" ? (
            <button
              className="primary-button compact"
              type="button"
              disabled={actionBusy}
              onClick={() => void runAction(api?.updaterStartDownload)}
            >
              {actionBusy ? "处理中…" : "下载更新"}
            </button>
          ) : null}
          {state.status === "downloaded" ? (
            <button
              className="primary-button compact"
              type="button"
              disabled={actionBusy}
              onClick={() => void runAction(api?.updaterQuitAndInstall)}
            >
              {actionBusy ? "正在重启…" : "重启并安装"}
            </button>
          ) : null}
        </div>
      </section>
    </div>
  );
}
