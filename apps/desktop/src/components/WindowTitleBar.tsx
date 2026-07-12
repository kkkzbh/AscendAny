export function WindowTitleBar() {
  const api = window.electronAPI;
  const controlsAPI = api !== undefined && api.platform !== "darwin" ? api : null;

  return (
    <header
      className="window-titlebar"
      onDoubleClick={() => api?.maximize()}
    >
      <span className="window-title">AscendAny</span>
      {controlsAPI !== null ? (
        <div className="window-controls">
          <button type="button" aria-label="最小化" onClick={() => controlsAPI.minimize()}>
            <span aria-hidden="true">—</span>
          </button>
          <button type="button" aria-label="最大化或还原" onClick={() => controlsAPI.maximize()}>
            <span aria-hidden="true">□</span>
          </button>
          <button className="window-close" type="button" aria-label="关闭" onClick={() => controlsAPI.close()}>
            <span aria-hidden="true">×</span>
          </button>
        </div>
      ) : null}
    </header>
  );
}
