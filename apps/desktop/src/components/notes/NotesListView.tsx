import { useMemo, useState } from "react";
import { useNotesStore, deriveAutoNoteTitle } from "@/stores/notesStore";
import { formatRelativeTime, summarizeNoteContent } from "./notesUtils";

const PLACEHOLDER_TITLE = "未命名笔记";

export function NotesListView() {
  const order = useNotesStore((state) => state.order);
  const items = useNotesStore((state) => state.items);
  const notes = useMemo(
    () =>
      order
        .map((id) => items[id])
        .filter((note): note is NonNullable<typeof note> => Boolean(note)),
    [order, items],
  );
  const activeId = useNotesStore((state) => state.activeId);
  const selectNote = useNotesStore((state) => state.selectNote);
  const createNote = useNotesStore((state) => state.createNote);
  const deleteNote = useNotesStore((state) => state.deleteNote);
  const setView = useNotesStore((state) => state.setView);

  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const handleNewNote = async () => {
    if (creating) return;
    setCreating(true);
    try {
      await createNote();
    } finally {
      setCreating(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!pendingDeleteId) return;
    const id = pendingDeleteId;
    setPendingDeleteId(null);
    await deleteNote(id);
  };

  return (
    <div className="notes-list">
      <div className="notes-list-header">
        <button
          type="button"
          className="notes-icon-btn"
          onClick={() => setView("detail")}
          title="返回详情"
          aria-label="返回详情"
        >
          <span aria-hidden>←</span>
        </button>
        <span className="notes-list-title">所有笔记</span>
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
      </div>

      <div className="notes-list-body">
        {notes.length === 0 ? (
          <div className="notes-empty-hint">
            还没有笔记，<button type="button" className="notes-inline-link" onClick={handleNewNote}>新建一份</button>
            开始记录。
          </div>
        ) : (
          notes.map((note) => {
            const title =
              (note.title?.trim() ? note.title : "")
              || deriveAutoNoteTitle(note.content)
              || PLACEHOLDER_TITLE;
            const summary = summarizeNoteContent(note.content);
            const isActive = note.id === activeId;
            return (
              <div
                key={note.id}
                role="button"
                tabIndex={0}
                className={`notes-list-row${isActive ? " is-active" : ""}`}
                onClick={() => {
                  void selectNote(note.id);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    void selectNote(note.id);
                  }
                }}
              >
                <div className="notes-list-row-main">
                  <div className="notes-list-row-title">{title}</div>
                  <div className="notes-list-row-summary">
                    {summary || "（暂无内容）"}
                  </div>
                </div>
                <div className="notes-list-row-meta">
                  <time
                    dateTime={new Date(note.updatedAt).toISOString()}
                    title={new Date(note.updatedAt).toLocaleString()}
                  >
                    {formatRelativeTime(note.updatedAt)}
                  </time>
                  <button
                    type="button"
                    className="notes-list-row-delete"
                    aria-label={`删除“${title}”`}
                    onClick={(event) => {
                      event.stopPropagation();
                      setPendingDeleteId(note.id);
                    }}
                  >
                    <span aria-hidden>🗑</span>
                  </button>
                </div>
              </div>
            );
          })
        )}
      </div>

      {pendingDeleteId ? (
        <div
          className="notes-confirm-backdrop"
          role="dialog"
          aria-modal="true"
          onClick={() => setPendingDeleteId(null)}
        >
          <div className="notes-confirm" onClick={(event) => event.stopPropagation()}>
            <p className="notes-confirm-title">删除该笔记？</p>
            <p className="notes-confirm-desc">删除后无法恢复。如果是当前激活的笔记，软件会自动切到最近更新的另一份；若库内已无笔记，会自动新建一份空白笔记。</p>
            <div className="notes-confirm-actions">
              <button type="button" onClick={() => setPendingDeleteId(null)}>
                取消
              </button>
              <button type="button" className="notes-confirm-danger" onClick={handleConfirmDelete}>
                删除
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
