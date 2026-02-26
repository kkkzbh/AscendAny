import { useRef, useState, type ChangeEvent, type ClipboardEvent, type FormEvent } from "react";

interface FeedbackImageItem {
  id: string;
  name: string;
  dataUrl: string;
}

const MAX_IMAGES = 8;
const MAX_IMAGE_SIZE_BYTES = 8 * 1024 * 1024;

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

export function FeedbackWindow() {
  const api = window.electronAPI;
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [images, setImages] = useState<FeedbackImageItem[]>([]);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function appendFiles(fileList: File[]) {
    if (fileList.length === 0) {
      return;
    }

    const availableSlots = MAX_IMAGES - images.length;
    if (availableSlots <= 0) {
      setError(`最多上传 ${MAX_IMAGES} 张图片。`);
      return;
    }

    const nextFiles = fileList.slice(0, availableSlots);
    const newImages: FeedbackImageItem[] = [];

    for (const file of nextFiles) {
      if (!file.type.startsWith("image/")) {
        setError("仅支持上传图片文件。");
        continue;
      }
      if (file.size > MAX_IMAGE_SIZE_BYTES) {
        setError("单张图片不能超过 8MB。");
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
        setError("读取图片失败，请重试。");
      }
    }

    if (newImages.length > 0) {
      setImages((prev) => [...prev, ...newImages]);
      setNotice(`已添加 ${newImages.length} 张图片。`);
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
    const clipboardItems = Array.from(event.clipboardData.items);
    const imageFiles = clipboardItems
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

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedContent = content.trim();
    if (!trimmedTitle || !trimmedContent) {
      setError("请先填写标题和反馈内容。");
      setNotice(null);
      return;
    }

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

  return (
    <div className="feedback-window-shell flex h-screen w-screen flex-col overflow-hidden">
      <header className="drag-region titlebar titlebar-pad feedback-window-header relative flex h-14 shrink-0 items-center">
        <div className="feedback-window-brand flex min-w-0 items-center">
          <p className="feedback-window-title">用户反馈</p>
        </div>
        <div className="feedback-window-controls no-drag absolute right-3 top-1/2 flex -translate-y-1/2 items-center">
          <button
            onClick={() => api?.minimize()}
            className="ui-window-button ui-window-traffic ui-window-minimize"
            title="最小化"
            aria-label="最小化"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
          </button>
          <button
            onClick={() => api?.maximize()}
            className="ui-window-button ui-window-traffic ui-window-maximize"
            title="最大化"
            aria-label="最大化"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
          </button>
          <button
            onClick={() => api?.close()}
            className="ui-window-button ui-window-traffic ui-window-close"
            title="关闭"
            aria-label="关闭"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
          </button>
        </div>
      </header>

      <main className="feedback-window-main no-drag flex-1 overflow-hidden">
        <section className="feedback-card mx-auto flex h-full w-full max-w-[900px] flex-col rounded-3xl p-6 sm:p-7">
          <form className="feedback-form flex h-full flex-col gap-5" onSubmit={onSubmit} onPaste={onPasteImage}>
            <label className="feedback-field">
              <span className="feedback-label">标题</span>
              <div className="input-shell feedback-input-shell rounded-[18px] px-4 py-3">
                <input
                  value={title}
                  onChange={(event) => setTitle(event.target.value)}
                  type="text"
                  maxLength={120}
                  className="feedback-input"
                  placeholder="请简要描述问题或建议"
                />
              </div>
            </label>

            <label className="feedback-field feedback-field-grow">
              <span className="feedback-label">内容</span>
              <div className="input-shell feedback-input-shell feedback-textarea-shell rounded-[20px] px-4 py-3">
                <textarea
                  value={content}
                  onChange={(event) => setContent(event.target.value)}
                  rows={10}
                  maxLength={4000}
                  className="feedback-textarea"
                  placeholder="请提供尽量详细的描述，支持在此直接粘贴截图（Ctrl+V）。"
                />
              </div>
            </label>

            <div className="feedback-field feedback-upload-section">
              <div className="flex items-center justify-between gap-3">
                <span className="feedback-label">截图 / 图片</span>
                <button
                  type="button"
                  onClick={() => fileInputRef.current?.click()}
                  className="settings-provider-pill feedback-upload-btn"
                >
                  上传图片
                </button>
              </div>
              <p className="feedback-hint">支持粘贴截图，最多 {MAX_IMAGES} 张，单张不超过 8MB。</p>
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                multiple
                hidden
                onChange={onSelectImage}
              />
              {images.length > 0 && (
                <div className="feedback-image-grid mt-3">
                  {images.map((item) => (
                    <div key={item.id} className="feedback-image-item">
                      <img src={item.dataUrl} alt={item.name} className="feedback-image-preview" />
                      <button
                        type="button"
                        onClick={() => removeImage(item.id)}
                        className="feedback-remove-image"
                        aria-label={`移除图片 ${item.name}`}
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {error && <p className="feedback-error">{error}</p>}
            {notice && <p className="feedback-notice">{notice}</p>}

            <div className="mt-auto flex items-center justify-end gap-3 pt-2">
              <button type="submit" className="send-button feedback-submit-btn" disabled={isSubmitting}>
                {isSubmitting ? "发送中..." : "发送反馈"}
              </button>
            </div>
          </form>
        </section>
      </main>
    </div>
  );
}
