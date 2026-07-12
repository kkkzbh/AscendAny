import { useCallback, useEffect, useMemo, useState } from "react";
import type { AgentNote, AgentNoteSummary } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import {
  changeStudentAgentNoteState,
  createStudentAgentNote,
  loadAgentNote,
  loadAgentNotes,
  replaceStudentAgentNote,
} from "../api/operations";
import { useSession } from "../session/context";

const encoder = new TextEncoder();

function canonicalDocument(title: string, content: string): { title: string; content: string } {
  return {
    title: title.normalize("NFC").trim(),
    content: content.replace(/\r\n?/g, "\n").normalize("NFC"),
  };
}

function validateDocument(title: string, content: string): string | null {
  if (title.length === 0 || title.includes("\n") || title.includes("\r") || title.includes("\0")) {
    return "标题必须是非空单行文本。";
  }
  if (encoder.encode(title).byteLength > 512) return "标题不能超过 512 个 UTF-8 字节。";
  if (content.includes("\0")) return "正文不能包含 NUL 字符。";
  if (encoder.encode(content).byteLength > 131072) return "正文不能超过 131072 个 UTF-8 字节。";
  return null;
}

function summaryOf(note: AgentNote): AgentNoteSummary {
  const { content, ...summary } = note;
  void content;
  return summary;
}

function replaceSummary(items: AgentNoteSummary[], note: AgentNote): AgentNoteSummary[] {
  const summary = summaryOf(note);
  return [summary, ...items.filter((item) => item.id !== note.id)];
}

function formatTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function NotesPage() {
  const { session } = useSession();
  const [summaries, setSummaries] = useState<AgentNoteSummary[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [note, setNote] = useState<AgentNote | null>(null);
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [loadingList, setLoadingList] = useState(true);
  const [loadingNote, setLoadingNote] = useState(false);
  const [mutating, setMutating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const selectedSummary = useMemo(
    () => summaries.find((item) => item.id === selectedId) ?? null,
    [selectedId, summaries],
  );

  const refreshList = useCallback(async () => {
    setLoadingList(true);
    setError(null);
    try {
      const page = await loadAgentNotes(session);
      setSummaries(page.items);
      setNextCursor(page.nextCursor);
      setSelectedId((current) => current ?? page.items[0]?.id ?? null);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoadingList(false);
    }
  }, [session]);

  useEffect(() => {
    void refreshList();
  }, [refreshList]);

  useEffect(() => {
    if (selectedId === null) {
      setNote(null);
      setTitle("");
      setContent("");
      return;
    }
    let active = true;
    setLoadingNote(true);
    setError(null);
    setMessage(null);
    void loadAgentNote(session, selectedId)
      .then((loaded) => {
        if (!active) return;
        setNote(loaded);
        setTitle(loaded.title);
        setContent(loaded.content);
      })
      .catch((loadError: unknown) => {
        if (active) setError(apiFailureMessage(loadError));
      })
      .finally(() => {
        if (active) setLoadingNote(false);
      });
    return () => {
      active = false;
    };
  }, [selectedId, session]);

  const createNote = async () => {
    setMutating(true);
    setError(null);
    setMessage(null);
    try {
      const result = await createStudentAgentNote(session, "新笔记", "");
      setSummaries((current) => replaceSummary(current, result.note));
      setSelectedId(result.note.id);
      setNote(result.note);
      setTitle(result.note.title);
      setContent(result.note.content);
      setMessage(result.idempotent ? "笔记已存在。" : "笔记已创建。");
    } catch (createError) {
      setError(apiFailureMessage(createError));
    } finally {
      setMutating(false);
    }
  };

  const loadMore = async () => {
    if (nextCursor === null) return;
    setLoadingList(true);
    setError(null);
    try {
      const page = await loadAgentNotes(session, 50, nextCursor);
      setSummaries((current) => {
        const known = new Set(current.map((item) => item.id));
        return [...current, ...page.items.filter((item) => !known.has(item.id))];
      });
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoadingList(false);
    }
  };

  const save = async () => {
    if (note === null || note.state !== "active") return;
    const canonical = canonicalDocument(title, content);
    const validation = validateDocument(canonical.title, canonical.content);
    if (validation !== null) {
      setError(validation);
      return;
    }
    setMutating(true);
    setError(null);
    setMessage(null);
    try {
      const result = await replaceStudentAgentNote(
        session,
        note,
        canonical.title,
        canonical.content,
      );
      setNote(result.note);
      setTitle(result.note.title);
      setContent(result.note.content);
      setSummaries((current) => replaceSummary(current, result.note));
      setMessage(result.idempotent ? "当前内容已保存。" : "笔记已保存。");
    } catch (saveError) {
      setError(apiFailureMessage(saveError));
    } finally {
      setMutating(false);
    }
  };

  const changeState = async () => {
    if (note === null) return;
    const target = note.state === "active" ? "archived" : "active";
    setMutating(true);
    setError(null);
    setMessage(null);
    try {
      const result = await changeStudentAgentNoteState(session, note, target);
      setNote(result.note);
      setSummaries((current) => replaceSummary(current, result.note));
      setMessage(target === "archived" ? "笔记已归档。" : "笔记已恢复。");
    } catch (stateError) {
      setError(apiFailureMessage(stateError));
    } finally {
      setMutating(false);
    }
  };

  const dirty = note !== null && (title !== note.title || content !== note.content);

  return (
    <div className="notes-workspace">
      <aside className="notes-sidebar">
        <button className="primary-button compact" type="button" disabled={mutating} onClick={() => void createNote()}>
          新建笔记
        </button>
        <div className="notes-list" aria-label="Agent 笔记列表">
          {loadingList && summaries.length === 0 ? <p className="empty-copy">正在读取笔记…</p> : null}
          {!loadingList && summaries.length === 0 ? <p className="empty-copy">还没有笔记。</p> : null}
          {summaries.map((summary) => (
            <button
              type="button"
              className={summary.id === selectedId ? "active" : ""}
              key={summary.id}
              onClick={() => setSelectedId(summary.id)}
            >
              <strong>{summary.title}</strong>
              <span>{summary.state === "active" ? "使用中" : "已归档"} · {formatTime(summary.updatedAt)}</span>
            </button>
          ))}
        </div>
        {nextCursor !== null ? <button className="text-button" type="button" onClick={() => void loadMore()}>加载更多笔记</button> : null}
      </aside>

      <section className="notes-editor">
        {error !== null ? <div className="global-error" role="alert">{error}</div> : null}
        {message !== null ? <div className="form-success" role="status">{message}</div> : null}
        {selectedSummary === null ? (
          <div className="notes-empty"><span>□</span><h2>选择或新建笔记</h2><p>Agent 对笔记的读取会被学生身份和 active state 限定。</p></div>
        ) : loadingNote || note === null ? (
          <div className="notes-empty" role="status"><span>□</span><p>正在读取笔记…</p></div>
        ) : (
          <>
            <header className="notes-editor-header">
              <div><strong>Revision {note.headRevision}</strong><span>{note.state === "active" ? "使用中" : "已归档"}</span></div>
              <div>
                <button className="secondary-button" type="button" disabled={mutating} onClick={() => void changeState()}>
                  {note.state === "active" ? "归档" : "恢复"}
                </button>
                <button className="primary-button compact" type="button" disabled={mutating || !dirty || note.state !== "active"} onClick={() => void save()}>
                  {mutating ? "提交中…" : "保存"}
                </button>
              </div>
            </header>
            <label className="notes-title-field">
              <span>标题</span>
              <input value={title} disabled={note.state !== "active"} maxLength={512} onChange={(event) => setTitle(event.target.value)} />
            </label>
            <label className="notes-content-field">
              <span>Markdown 正文</span>
              <textarea value={content} disabled={note.state !== "active"} rows={18} onChange={(event) => setContent(event.target.value)} />
            </label>
          </>
        )}
      </section>
    </div>
  );
}
