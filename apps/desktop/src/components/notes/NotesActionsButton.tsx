import { useEffect, useRef, useState } from "react";
import { useNotesStore, selectActiveNote } from "@/stores/notesStore";
import {
  formatExportFileName,
  plainTextFromMarkdown,
} from "./notesUtils";

interface NotesActionsButtonProps {
  previewRef: React.RefObject<HTMLDivElement | null>;
}

type ActionResultTone = "success" | "error";

interface ActionResult {
  tone: ActionResultTone;
  message: string;
}

async function copyToClipboard(value: string): Promise<boolean> {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // fallthrough
  }
  return false;
}

function downloadAsFile(filename: string, mime: string, body: BlobPart): void {
  const blob = new Blob([body], { type: mime });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

const PDF_PRINT_STYLE = `
:root { color-scheme: light; }
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", Roboto, Helvetica, Arial, sans-serif;
  font-size: 14px;
  line-height: 1.65;
  color: #111827;
  margin: 0;
  padding: 32px 36px;
}
h1, h2, h3, h4, h5, h6 { color: #0f172a; line-height: 1.3; margin: 1.6em 0 0.6em; }
p { margin: 0.5em 0; }
ul, ol { padding-left: 1.4em; }
code {
  background: #f1f5f9;
  padding: 0 4px;
  border-radius: 4px;
  font-family: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace;
  font-size: 0.9em;
}
pre {
  background: #f8fafc;
  padding: 12px 14px;
  border-radius: 6px;
  overflow-x: auto;
}
img { max-width: 100%; }
blockquote {
  border-left: 3px solid #cbd5f5;
  margin: 0.6em 0;
  padding: 0.2em 0.8em;
  color: #475569;
}
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #e2e8f0; padding: 6px 8px; text-align: left; }
`;

function buildPrintHtml(title: string, innerHtml: string): string {
  const safeTitle = title.replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<!doctype html><html><head><meta charset="utf-8"><title>${safeTitle}</title><style>${PDF_PRINT_STYLE}</style></head><body><h1>${safeTitle}</h1>${innerHtml}</body></html>`;
}

export function NotesActionsButton({ previewRef }: NotesActionsButtonProps) {
  const activeNote = useNotesStore(selectActiveNote);
  const clearActiveContent = useNotesStore((state) => state.clearActiveContent);
  const [open, setOpen] = useState(false);
  const [showClearConfirm, setShowClearConfirm] = useState(false);
  const [feedback, setFeedback] = useState<ActionResult | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const handleClick = (event: MouseEvent) => {
      const node = containerRef.current;
      if (node && !node.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => {
      document.removeEventListener("mousedown", handleClick);
    };
  }, [open]);

  useEffect(() => {
    if (!feedback) return;
    const timer = window.setTimeout(() => setFeedback(null), 2200);
    return () => window.clearTimeout(timer);
  }, [feedback]);

  const disabled = !activeNote;

  const reportSuccess = (message: string) => setFeedback({ tone: "success", message });
  const reportError = (message: string) => setFeedback({ tone: "error", message });

  const handleCopyMarkdown = async () => {
    if (!activeNote) return;
    setOpen(false);
    const ok = await copyToClipboard(activeNote.content);
    ok ? reportSuccess("已复制 Markdown 到剪贴板") : reportError("复制失败，请手动选中复制");
  };

  const handleExportMd = () => {
    if (!activeNote) return;
    setOpen(false);
    const filename = `${formatExportFileName(activeNote.title)}.md`;
    downloadAsFile(filename, "text/markdown;charset=utf-8", activeNote.content);
    reportSuccess(`已导出 ${filename}`);
  };

  const handleExportPdf = async () => {
    if (!activeNote) return;
    setOpen(false);
    const node = previewRef.current;
    if (!node) {
      reportError("无法获取预览内容");
      return;
    }
    const innerHtml = node.innerHTML;
    if (!innerHtml.trim()) {
      reportError("笔记为空，无法导出");
      return;
    }
    const html = buildPrintHtml(activeNote.title || "未命名笔记", innerHtml);
    const filename = `${formatExportFileName(activeNote.title)}.pdf`;
    const exporter = window.electronAPI?.notesExportPdf;
    if (!exporter) {
      reportError("当前环境不支持 PDF 导出");
      return;
    }
    try {
      const result = await exporter({ html, defaultFilename: filename });
      if (result.success) {
        reportSuccess(result.path ? `已导出到 ${result.path}` : "导出成功");
      } else if (result.canceled) {
        // silent — user cancelled
      } else {
        reportError(result.message ?? "导出失败");
      }
    } catch (error) {
      console.warn("[notes] export pdf failed", error);
      reportError("导出失败");
    }
  };

  const handleCopyAsContent = async () => {
    if (!activeNote) return;
    setOpen(false);
    const node = previewRef.current;
    const text = node?.innerText?.trim() || plainTextFromMarkdown(activeNote.content);
    const ok = await copyToClipboard(text);
    ok ? reportSuccess("已复制纯文本到剪贴板") : reportError("复制失败");
  };

  const handleClearRequest = () => {
    setOpen(false);
    setShowClearConfirm(true);
  };

  const handleConfirmClear = async () => {
    setShowClearConfirm(false);
    await clearActiveContent();
    reportSuccess("笔记内容已清空");
  };

  return (
    <div className="notes-actions" ref={containerRef}>
      <div className="notes-actions-split" role="group" aria-label="笔记操作">
        <button
          type="button"
          className="notes-actions-primary"
          onClick={handleCopyMarkdown}
          disabled={disabled}
          title="复制 Markdown"
          aria-label="复制 Markdown"
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
            <rect x="9" y="9" width="11" height="11" rx="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        </button>
        <button
          type="button"
          className="notes-actions-toggle"
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={() => setOpen((value) => !value)}
          disabled={disabled}
          title="更多操作"
        >
          <span aria-hidden>▾</span>
        </button>
      </div>
      {open ? (
        <div className="notes-actions-menu" role="menu">
          <button type="button" role="menuitem" onClick={handleExportMd}>
            导出为 md
          </button>
          <button type="button" role="menuitem" onClick={handleExportPdf}>
            导出为 pdf
          </button>
          <button type="button" role="menuitem" onClick={handleCopyAsContent}>
            Copy as content
          </button>
          <button
            type="button"
            role="menuitem"
            className="notes-actions-menu-danger"
            onClick={handleClearRequest}
          >
            清空笔记
          </button>
        </div>
      ) : null}
      {showClearConfirm ? (
        <div
          className="notes-confirm-backdrop"
          role="dialog"
          aria-modal="true"
          onClick={() => setShowClearConfirm(false)}
        >
          <div className="notes-confirm" onClick={(event) => event.stopPropagation()}>
            <p className="notes-confirm-title">确认清空当前笔记内容？</p>
            <p className="notes-confirm-desc">仅清空内容，笔记本身仍会保留。</p>
            <div className="notes-confirm-actions">
              <button type="button" onClick={() => setShowClearConfirm(false)}>
                取消
              </button>
              <button
                type="button"
                className="notes-confirm-danger"
                onClick={handleConfirmClear}
              >
                清空
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {feedback ? (
        <div className={`notes-actions-toast notes-actions-toast--${feedback.tone}`} role="status">
          {feedback.message}
        </div>
      ) : null}
    </div>
  );
}
