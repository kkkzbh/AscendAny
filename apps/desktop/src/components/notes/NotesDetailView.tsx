import { useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  selectActiveNote,
  useNotesStore,
  NOTES_LIMITS,
  deriveAutoNoteTitle,
} from "@/stores/notesStore";
import { NotesActionsButton } from "./NotesActionsButton";
import { safeMarkdownUrl } from "./notesUtils";

const PLACEHOLDER_HEADING = "未命名笔记";

export function NotesDetailView() {
  const activeNote = useNotesStore(selectActiveNote);
  const setView = useNotesStore((state) => state.setView);
  const createNote = useNotesStore((state) => state.createNote);
  const setTitle = useNotesStore((state) => state.setTitle);
  const setContent = useNotesStore((state) => state.setContent);
  const setIsEditingContent = useNotesStore((state) => state.setIsEditingContent);
  const isEditingContent = useNotesStore((state) => state.isEditingContent);
  const pendingRemoteUpdate = useNotesStore((state) => state.pendingRemoteUpdate);
  const acceptPendingRemoteUpdate = useNotesStore((state) => state.acceptPendingRemoteUpdate);
  const dismissPendingRemoteUpdate = useNotesStore((state) => state.dismissPendingRemoteUpdate);

  const [creating, setCreating] = useState(false);
  const previewRef = useRef<HTMLDivElement | null>(null);

  const handleNewNote = async () => {
    if (creating) return;
    setCreating(true);
    try {
      await createNote();
    } finally {
      setCreating(false);
    }
  };

  if (!activeNote) {
    return (
      <div className="notes-detail notes-detail--empty">
        <p className="notes-empty-hint">还没有笔记。新建一份开始记录吧。</p>
        <button type="button" className="notes-empty-cta" onClick={handleNewNote}>
          + 新建笔记
        </button>
      </div>
    );
  }

  const titleValue = activeNote.title;
  const titlePlaceholder = activeNote.titleIsAuto
    ? deriveAutoNoteTitle(activeNote.content) || PLACEHOLDER_HEADING
    : PLACEHOLDER_HEADING;
  const charCount = activeNote.content.length;
  const charLimit = NOTES_LIMITS.CONTENT_MAX_LENGTH;
  const charNearLimit = charCount > charLimit * 0.85;

  return (
    <div className="notes-detail">
      <div className="notes-detail-header">
        <input
          type="text"
          className="notes-title-input"
          value={titleValue}
          placeholder={titlePlaceholder}
          aria-label="笔记标题"
          maxLength={NOTES_LIMITS.TITLE_MAX_LENGTH}
          onChange={(event) => setTitle(event.target.value)}
        />
        <div className="notes-detail-toolbar">
          <button
            type="button"
            className={`notes-icon-btn${isEditingContent ? " is-active" : ""}`}
            onClick={() => setIsEditingContent(!isEditingContent)}
            title={isEditingContent ? "完成编辑" : "编辑笔记"}
            aria-label={isEditingContent ? "完成编辑" : "编辑笔记"}
            aria-pressed={isEditingContent}
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
              aria-hidden
            >
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4Z" />
            </svg>
          </button>
          <button
            type="button"
            className="notes-icon-btn"
            onClick={handleNewNote}
            disabled={creating}
            title="新建笔记"
            aria-label="新建笔记"
          >
            <span aria-hidden>＋</span>
          </button>
          <button
            type="button"
            className="notes-icon-btn"
            onClick={() => setView("list")}
            title="加载笔记"
            aria-label="加载笔记"
          >
            <span aria-hidden>≡</span>
          </button>
          <NotesActionsButton previewRef={previewRef} />
        </div>
      </div>

      {pendingRemoteUpdate !== null ? (
        <div className="notes-pending-banner" role="status">
          <span>模型已更新笔记，是否替换当前编辑？</span>
          <div className="notes-pending-actions">
            <button type="button" onClick={dismissPendingRemoteUpdate}>
              忽略
            </button>
            <button type="button" className="notes-pending-accept" onClick={acceptPendingRemoteUpdate}>
              查看
            </button>
          </div>
        </div>
      ) : null}

      <div className="notes-detail-body">
        {isEditingContent ? (
          <textarea
            className="notes-editor"
            value={activeNote.content}
            maxLength={NOTES_LIMITS.CONTENT_MAX_LENGTH}
            onChange={(event) => setContent(event.target.value)}
          />
        ) : (
          <div ref={previewRef} className="notes-preview chat-markdown">
            {activeNote.content.trim() ? (
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                urlTransform={(value) => safeMarkdownUrl(value)}
              >
                {activeNote.content}
              </ReactMarkdown>
            ) : null}
          </div>
        )}
        <NotesUsageRing
          count={charCount}
          limit={charLimit}
          warn={charNearLimit}
        />
      </div>
    </div>
  );
}

interface NotesUsageRingProps {
  count: number;
  limit: number;
  warn: boolean;
}

function NotesUsageRing({ count, limit, warn }: NotesUsageRingProps) {
  const ratio = limit > 0 ? Math.min(1, count / limit) : 0;
  const radius = 8;
  const circumference = 2 * Math.PI * radius;
  const dashOffset = circumference * (1 - ratio);
  return (
    <div
      className={`notes-usage-ring${warn ? " is-warn" : ""}`}
      role="status"
      aria-label={`已使用 ${count.toLocaleString()} / ${limit.toLocaleString()} 字符`}
    >
      <svg width="20" height="20" viewBox="0 0 20 20" aria-hidden>
        <circle
          cx="10"
          cy="10"
          r={radius}
          className="notes-usage-ring-track"
          fill="none"
          strokeWidth="2.4"
        />
        <circle
          cx="10"
          cy="10"
          r={radius}
          className="notes-usage-ring-arc"
          fill="none"
          strokeWidth="2.4"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={dashOffset}
          transform="rotate(-90 10 10)"
        />
      </svg>
      <div className="notes-usage-tooltip" role="tooltip">
        {count.toLocaleString()} / {limit.toLocaleString()} 字符
      </div>
    </div>
  );
}
