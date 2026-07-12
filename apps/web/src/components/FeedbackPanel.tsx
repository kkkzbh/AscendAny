import { useState, type FormEvent } from "react";
import { apiFailureMessage } from "../api/client";
import { sendAuthenticatedFeedback } from "../api/operations";
import { useSession } from "../session/context";

const encoder = new TextEncoder();

export function FeedbackPanel() {
  const { session } = useSession();
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const canonicalTitle = title.trim();
    const canonicalContent = content.trim();
    if (canonicalTitle.length === 0 || encoder.encode(canonicalTitle).byteLength > 800) {
      setError("反馈标题必须包含 1 至 800 个 UTF-8 字节。");
      return;
    }
    if (canonicalContent.length === 0 || encoder.encode(canonicalContent).byteLength > 40000) {
      setError("反馈内容必须包含 1 至 40000 个 UTF-8 字节。");
      return;
    }
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const result = await sendAuthenticatedFeedback(session, {
        title: canonicalTitle,
        content: canonicalContent,
        platform: "web",
      });
      setTitle("");
      setContent("");
      setMessage(result.created ? `反馈已提交（${result.submission.id.slice(0, 8)}）。` : "该反馈已经提交。");
    } catch (submitError) {
      setError(apiFailureMessage(submitError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel-card">
      <header className="section-heading">
        <div>
          <span className="eyebrow">FEEDBACK</span>
          <h2>提交反馈</h2>
          <p>反馈会进入持久化 delivery job，并保留投递审计。</p>
        </div>
      </header>
      <form className="feedback-form" onSubmit={(event) => void submit(event)}>
        <label>
          <span>标题</span>
          <input value={title} disabled={busy} onChange={(event) => setTitle(event.target.value)} />
        </label>
        <label>
          <span>详细内容</span>
          <textarea value={content} disabled={busy} rows={7} onChange={(event) => setContent(event.target.value)} />
        </label>
        {error !== null ? <div className="form-error" role="alert">{error}</div> : null}
        {message !== null ? <div className="form-success" role="status">{message}</div> : null}
        <button className="primary-button compact" type="submit" disabled={busy || title.trim().length === 0 || content.trim().length === 0}>
          {busy ? "提交中…" : "提交反馈"}
        </button>
      </form>
    </section>
  );
}
