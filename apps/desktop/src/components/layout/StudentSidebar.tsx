import {
  useCallback,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";

import { useChatStore } from "@/stores/chatStore";
import {
  MIN_LEFT_SIDEBAR_RATIO,
  MAX_LEFT_SIDEBAR_RATIO,
  useLayoutStore,
} from "@/stores/layoutStore";
import { useSettingsStore } from "@/stores/settingsStore";

function formatRelativeTime(timestamp: number): string {
  if (!Number.isFinite(timestamp)) {
    return "";
  }
  const diffMs = Math.max(0, Date.now() - timestamp);
  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  return `${days} 天前`;
}

export function StudentSidebar() {
  const [query, setQuery] = useState("");
  const draggingRef = useRef(false);
  const sessions = useChatStore((s) => s.sessions);
  const activeSessionId = useChatStore((s) => s.activeSessionId);
  const startNewSessionDraft = useChatStore((s) => s.startNewSessionDraft);
  const selectSession = useChatStore((s) => s.selectSession);
  const deleteSession = useChatStore((s) => s.deleteSession);
  const isCollapsed = useLayoutStore((s) => s.isLeftSidebarCollapsed);
  const leftSidebarRatio = useLayoutStore((s) => s.leftSidebarRatio);
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar);
  const setLeftSidebarCollapsed = useLayoutStore((s) => s.setLeftSidebarCollapsed);
  const setLeftSidebarRatio = useLayoutStore((s) => s.setLeftSidebarRatio);
  const openSettings = useSettingsStore((s) => s.openSettings);

  const filteredSessions = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    if (!normalizedQuery) {
      return sessions;
    }
    return sessions.filter((session) => {
      const haystack = [
        session.title,
        ...session.messages.map((message) => message.content),
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(normalizedQuery);
    });
  }, [query, sessions]);

  const applyRatioFromClientX = useCallback(
    (clientX: number) => {
      const width = window.innerWidth || 1;
      setLeftSidebarRatio(clientX / width);
    },
    [setLeftSidebarRatio],
  );

  const onResizePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      event.preventDefault();
      draggingRef.current = true;
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      applyRatioFromClientX(event.clientX);

      const onPointerMove = (moveEvent: PointerEvent) => {
        if (!draggingRef.current) return;
        applyRatioFromClientX(moveEvent.clientX);
      };

      const onPointerUp = () => {
        draggingRef.current = false;
        window.removeEventListener("pointermove", onPointerMove);
        window.removeEventListener("pointerup", onPointerUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      window.addEventListener("pointermove", onPointerMove);
      window.addEventListener("pointerup", onPointerUp);
    },
    [applyRatioFromClientX],
  );

  const onResizeKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLDivElement>) => {
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
      event.preventDefault();
      const step = event.shiftKey ? 0.03 : 0.01;
      const delta = event.key === "ArrowLeft" ? -step : step;
      setLeftSidebarRatio(useLayoutStore.getState().leftSidebarRatio + delta);
    },
    [setLeftSidebarRatio],
  );

  if (isCollapsed) {
    return <aside className="student-sidebar is-collapsed" aria-hidden="true" />;
  }

  const sidebarStyle = {
    "--student-left-sidebar-ratio": String(leftSidebarRatio),
  } as CSSProperties;

  return (
    <aside className="student-sidebar" style={sidebarStyle}>
      <div className="student-sidebar-top">
        <button
          type="button"
          className="student-titlebar-button student-sidebar-titlebar-button no-drag"
          onClick={toggleLeftSidebar}
          title="折叠左侧栏"
          aria-label="折叠左侧栏"
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
            <path d="M17 9l-3 3 3 3" />
          </svg>
        </button>
      </div>

      <div className="student-sidebar-actions">
        <button
          type="button"
          className="student-sidebar-action no-drag"
          onClick={() => {
            startNewSessionDraft();
            setLeftSidebarCollapsed(false);
          }}
          title="新对话"
          aria-label="新对话"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
          {!isCollapsed && <span>新对话</span>}
        </button>

        {isCollapsed ? (
          <button
            type="button"
            className="student-sidebar-action no-drag"
            onClick={() => setLeftSidebarCollapsed(false)}
            title="搜索"
            aria-label="搜索"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.9"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.2-3.2" />
            </svg>
          </button>
        ) : (
          <label className="student-sidebar-search no-drag">
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.9"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.2-3.2" />
            </svg>
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索"
            />
          </label>
        )}
      </div>

      <nav className="student-session-list no-drag" aria-label="对话列表">
        {filteredSessions.map((session) => {
          const isActive = session.id === activeSessionId;
          return (
            <div
              key={session.id}
              className={`student-session-item ${isActive ? "is-active" : ""}`}
            >
              <button
                type="button"
                className="student-session-select"
                onClick={() => selectSession(session.id)}
                title={session.title}
              >
                <span className="student-session-title">{session.title}</span>
                <span className="student-session-time">
                  {formatRelativeTime(session.updatedAt)}
                </span>
              </button>
              {!isCollapsed && (
                <button
                  type="button"
                  className="student-session-delete"
                  aria-label={`删除对话 ${session.title}`}
                  onClick={() => deleteSession(session.id)}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.8"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M3 6h18" />
                    <path d="M8 6V4h8v2" />
                    <path d="M19 6l-1 14H6L5 6" />
                  </svg>
                </button>
              )}
            </div>
          );
        })}
      </nav>

      <div className="student-sidebar-footer">
        <button
          type="button"
          className="student-sidebar-action no-drag"
          onClick={openSettings}
          title="设置"
          aria-label="设置"
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
          >
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
          </svg>
          {!isCollapsed && <span>设置</span>}
        </button>
      </div>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="调整左侧栏宽度"
        aria-valuemin={MIN_LEFT_SIDEBAR_RATIO}
        aria-valuemax={MAX_LEFT_SIDEBAR_RATIO}
        aria-valuenow={Number(leftSidebarRatio.toFixed(2))}
        tabIndex={0}
        className="student-sidebar-resizer no-drag"
        onPointerDown={onResizePointerDown}
        onKeyDown={onResizeKeyDown}
      >
        <div className="student-sidebar-resizer-bar" />
      </div>
    </aside>
  );
}
