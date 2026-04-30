import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
} from "react";
import { useAuthStore } from "@/stores/authStore";

interface FeedbackImageItem {
  id: string;
  name: string;
  dataUrl: string;
}

const MAX_IMAGES = 8;
const MAX_IMAGE_SIZE_BYTES = 8 * 1024 * 1024;
const NOTICE_AUTO_CLEAR_MS = 5000;

function readImageAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        resolve(reader.result);
      } else {
        reject(new Error("invalid-file"));
      }
    };
    reader.onerror = () => reject(new Error("file-read-failed"));
    reader.readAsDataURL(file);
  });
}

function buildImageId() {
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function FeedbackSettingsPage() {
  const account = useAuthStore((s) => s.account);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [images, setImages] = useState<FeedbackImageItem[]>([]);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isDragging, setIsDragging] = useState(false);

  useEffect(() => {
    if (!notice) return;
    const handle = window.setTimeout(() => setNotice(null), NOTICE_AUTO_CLEAR_MS);
    return () => window.clearTimeout(handle);
  }, [notice]);

  const remainingSlots = MAX_IMAGES - images.length;
  const canSubmit = title.trim().length > 0 && content.trim().length > 0 && !isSubmitting;
  const hasContent = title.length > 0 || content.length > 0 || images.length > 0;
  const dropzoneDisabled = isSubmitting || remainingSlots <= 0;

  async function appendFiles(fileList: File[]) {
    if (fileList.length === 0) {
      return;
    }

    if (remainingSlots <= 0) {
      setError(`最多上传 ${MAX_IMAGES} 张图片。`);
      return;
    }

    const nextFiles = fileList.slice(0, remainingSlots);
    const newImages: FeedbackImageItem[] = [];
    let localError: string | null = null;

    for (const file of nextFiles) {
      if (!file.type.startsWith("image/")) {
        localError = "仅支持上传图片文件。";
        continue;
      }
      if (file.size > MAX_IMAGE_SIZE_BYTES) {
        localError = "单张图片不能超过 8MB。";
        continue;
      }
      try {
        const dataUrl = await readImageAsDataUrl(file);
        newImages.push({
          id: buildImageId(),
          name: file.name || "screenshot.png",
          dataUrl,
        });
      } catch {
        localError = "读取图片失败，请重试。";
      }
    }

    if (newImages.length > 0) {
      setImages((prev) => [...prev, ...newImages]);
      setNotice(`已添加 ${newImages.length} 张图片。`);
      setError(null);
    }
    if (localError) {
      setError(localError);
    }
  }

  function onSelectImage(event: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(event.target.files ?? []);
    if (files.length === 0) {
      return;
    }
    setError(null);
    void appendFiles(files);
    event.target.value = "";
  }

  function onPasteImage(event: ClipboardEvent<HTMLElement>) {
    const items = Array.from(event.clipboardData.items);
    const imageFiles = items
      .filter((item) => item.type.startsWith("image/"))
      .map((item) => item.getAsFile())
      .filter((file): file is File => Boolean(file));

    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    setError(null);
    void appendFiles(imageFiles);
  }

  function onDragOver(event: DragEvent<HTMLDivElement>) {
    if (dropzoneDisabled) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setIsDragging(true);
  }

  function onDragLeave(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setIsDragging(false);
  }

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setIsDragging(false);
    if (dropzoneDisabled) return;
    const files = Array.from(event.dataTransfer.files ?? []);
    if (files.length === 0) return;
    setError(null);
    void appendFiles(files);
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedContent = content.trim();
    if (!trimmedTitle || !trimmedContent) {
      setError("请先填写标题和反馈内容。");
      setNotice(null);
      return;
    }

    const api = window.electronAPI;
    if (!api?.submitFeedback) {
      setError("当前环境不支持发送反馈。");
      setNotice(null);
      return;
    }

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      const result = await api.submitFeedback({
        title: trimmedTitle,
        content: trimmedContent,
        images: images.map((item) => ({
          name: item.name,
          dataUrl: item.dataUrl,
        })),
      });
      if (!result.success) {
        setError(result.message);
        return;
      }

      setNotice(result.message);
      setTitle("");
      setContent("");
      setImages([]);
    } catch {
      setError("发送失败，请稍后重试。");
    } finally {
      setIsSubmitting(false);
    }
  }

  function removeImage(id: string) {
    setImages((prev) => prev.filter((item) => item.id !== id));
  }

  function onReset() {
    setTitle("");
    setContent("");
    setImages([]);
    setError(null);
    setNotice(null);
  }

  const senderLabel =
    account?.displayName?.trim() ||
    account?.username?.trim() ||
    account?.studentId?.trim() ||
    "当前账号";

  return (
    <div className="settings-page animate-fade-in">
      <h2 className="settings-page-title text-lg font-semibold text-[var(--text-strong)]">反馈</h2>
      <p className="text-[13px] leading-relaxed text-[var(--text-muted)]">
        反馈将以邮件形式发送到开发者邮箱，并附带 {senderLabel} 与系统平台等信息以便定位问题。
      </p>

      <form className="settings-feedback-form" onSubmit={onSubmit} onPaste={onPasteImage}>
        <div className="settings-group">
          <div className="settings-field">
            <label
              htmlFor="settings-feedback-title"
              className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase"
            >
              标题
            </label>
            <input
              id="settings-feedback-title"
              type="text"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              maxLength={120}
              disabled={isSubmitting}
              placeholder="请简要描述问题或建议"
              className="settings-input"
            />
          </div>

          <div className="settings-field">
            <label
              htmlFor="settings-feedback-content"
              className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase"
            >
              详细描述
            </label>
            <textarea
              id="settings-feedback-content"
              value={content}
              onChange={(event) => setContent(event.target.value)}
              maxLength={4000}
              rows={8}
              disabled={isSubmitting}
              placeholder="请提供尽量详细的描述，支持在此直接粘贴截图（Ctrl+V）。"
              className="settings-input settings-feedback-textarea"
            />
            <p className="text-[11px] text-[var(--text-soft)]">
              支持直接粘贴截图（Ctrl+V）。
            </p>
          </div>

          <div className="settings-field">
            <label className="block text-xs font-semibold tracking-[0.08em] text-[var(--text-soft)] uppercase">
              附件截图
            </label>
            <div
              role="button"
              tabIndex={dropzoneDisabled ? -1 : 0}
              onClick={() => {
                if (dropzoneDisabled) return;
                fileInputRef.current?.click();
              }}
              onKeyDown={(event) => {
                if (dropzoneDisabled) return;
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  fileInputRef.current?.click();
                }
              }}
              onDragOver={onDragOver}
              onDragLeave={onDragLeave}
              onDrop={onDrop}
              className={`settings-feedback-dropzone${isDragging ? " is-dragging" : ""}${
                dropzoneDisabled ? " is-disabled" : ""
              }`}
              aria-label="上传反馈截图"
            >
              <svg
                width="22"
                height="22"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M12 16V4" />
                <path d="m7 9 5-5 5 5" />
                <path d="M5 16v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3" />
              </svg>
              <p className="settings-feedback-dropzone-title">
                {remainingSlots <= 0
                  ? `已达上限：最多 ${MAX_IMAGES} 张`
                  : "点击选择 / 拖拽图片到此处 / 直接粘贴截图"}
              </p>
              <p className="settings-feedback-dropzone-hint">
                最多 {MAX_IMAGES} 张，单张不超过 8MB · 当前 {images.length}/{MAX_IMAGES}
              </p>
            </div>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              multiple
              hidden
              onChange={onSelectImage}
            />
            {images.length > 0 && (
              <div className="settings-feedback-image-grid">
                {images.map((item) => (
                  <div key={item.id} className="settings-feedback-image-card">
                    <img src={item.dataUrl} alt={item.name} />
                    <button
                      type="button"
                      onClick={() => removeImage(item.id)}
                      disabled={isSubmitting}
                      className="settings-feedback-image-remove"
                      aria-label={`移除图片 ${item.name}`}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="settings-feedback-footer">
          <div className="settings-feedback-status" aria-live="polite">
            {error && (
              <span className="settings-feedback-status-chip is-error">{error}</span>
            )}
            {!error && notice && (
              <span className="settings-feedback-status-chip is-success">{notice}</span>
            )}
          </div>
          <div className="settings-feedback-actions">
            <button
              type="button"
              onClick={onReset}
              disabled={isSubmitting || !hasContent}
              className={`settings-provider-pill px-4 ${
                isSubmitting || !hasContent
                  ? "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
                  : "bg-[var(--surface-raised)] text-[var(--text-strong)] ring-1 ring-[var(--border-subtle)] hover:bg-[var(--surface-hover)]"
              }`}
            >
              重置
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className={`settings-provider-pill px-5 ${
                canSubmit
                  ? "bg-[var(--accent-600)] font-medium text-white shadow-[0_8px_16px_rgba(3,105,161,0.25)] hover:opacity-90"
                  : "cursor-not-allowed bg-[var(--surface-soft)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]"
              }`}
            >
              {isSubmitting ? "发送中..." : "发送反馈"}
            </button>
          </div>
        </div>
      </form>
    </div>
  );
}
