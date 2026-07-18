import { useState, useRef, useCallback, useEffect } from "react";
import ReactCrop, { type Crop, centerCrop, makeAspectCrop } from "react-image-crop";
import "react-image-crop/dist/ReactCrop.css";

interface AvatarCropperProps {
  /** Called with the final cropped image as a data-URL (image/png). */
  onConfirm: (dataUrl: string) => void;
  onCancel: () => void;
}

/** Output size in px for the cropped avatar. */
const OUTPUT_SIZE = 256;

function centerAspectCrop(mediaWidth: number, mediaHeight: number): Crop {
  return centerCrop(
    makeAspectCrop(
      { unit: "%", width: 80 },
      1,
      mediaWidth,
      mediaHeight,
    ),
    mediaWidth,
    mediaHeight,
  );
}

export function AvatarCropper({ onConfirm, onCancel }: AvatarCropperProps) {
  const [imgSrc, setImgSrc] = useState<string | null>(null);
  const [crop, setCrop] = useState<Crop>();
  const [completedCrop, setCompletedCrop] = useState<Crop>();
  const imgRef = useRef<HTMLImageElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // Open file picker on mount
  useEffect(() => {
    fileInputRef.current?.click();
  }, []);

  const onFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = () => {
      setImgSrc(reader.result as string);
    };
    reader.readAsDataURL(file);
    // Reset so re-selecting the same file works
    e.target.value = "";
  }, []);

  const onImageLoad = useCallback((e: React.SyntheticEvent<HTMLImageElement>) => {
    const { naturalWidth, naturalHeight } = e.currentTarget;
    const initial = centerAspectCrop(naturalWidth, naturalHeight);
    setCrop(initial);
    setCompletedCrop(initial);
  }, []);

  const handleConfirm = useCallback(() => {
    const image = imgRef.current;
    if (!image || !completedCrop) return;

    const canvas = document.createElement("canvas");
    canvas.width = OUTPUT_SIZE;
    canvas.height = OUTPUT_SIZE;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const scaleX = image.naturalWidth / image.width;
    const scaleY = image.naturalHeight / image.height;

    const cropX = (completedCrop.x ?? 0) * scaleX;
    const cropY = (completedCrop.y ?? 0) * scaleY;
    const cropW = (completedCrop.width ?? 0) * scaleX;
    const cropH = (completedCrop.height ?? 0) * scaleY;

    ctx.drawImage(image, cropX, cropY, cropW, cropH, 0, 0, OUTPUT_SIZE, OUTPUT_SIZE);

    const dataUrl = canvas.toDataURL("image/png");
    onConfirm(dataUrl);
  }, [completedCrop, onConfirm]);

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-3">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/40 backdrop-blur-sm"
        onClick={onCancel}
      />

      {/* Dialog */}
      <div className="avatar-cropper-dialog relative z-10 flex w-[420px] max-w-[92vw] flex-col overflow-hidden rounded-2xl">
        {/* Header */}
        <div className="flex items-center justify-between px-5 pt-4 pb-2">
          <h3 className="text-[15px] font-semibold text-[var(--text-strong)]">
            裁剪头像
          </h3>
          <button onClick={onCancel} className="ui-icon-button">
            <svg width="14" height="14" viewBox="0 0 14 14">
              <path
                d="M1 1l12 12M13 1L1 13"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>

        {/* Content */}
        <div className="flex flex-col items-center gap-4 px-5 pb-5">
          {!imgSrc ? (
            <div className="flex h-52 w-full items-center justify-center rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--surface-soft)]">
              <button
                className="text-sm font-medium text-[var(--accent-600)] hover:underline"
                onClick={() => fileInputRef.current?.click()}
              >
                选择图片
              </button>
            </div>
          ) : (
            <div className="avatar-crop-area flex items-center justify-center overflow-hidden rounded-xl">
              <ReactCrop
                crop={crop}
                onChange={(c) => setCrop(c)}
                onComplete={(c) => setCompletedCrop(c)}
                aspect={1}
                circularCrop
                minWidth={48}
                minHeight={48}
              >
                <img
                  ref={imgRef}
                  src={imgSrc}
                  alt="待裁剪图片"
                  onLoad={onImageLoad}
                  style={{ maxHeight: "50vh", maxWidth: "100%" }}
                  draggable={false}
                />
              </ReactCrop>
            </div>
          )}

          {/* Actions */}
          {imgSrc && (
            <div className="flex w-full items-center gap-3">
              <button
                className="flex-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-raised)] py-2.5 text-sm font-medium text-[var(--text-muted)] transition-all hover:bg-[var(--surface-hover)]"
                onClick={() => fileInputRef.current?.click()}
              >
                重新选择
              </button>
              <button
                className="auth-submit flex-1"
                onClick={handleConfirm}
                disabled={!completedCrop}
              >
                确认裁剪
              </button>
            </div>
          )}
        </div>

        {/* Hidden file input */}
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={onFileChange}
        />
      </div>
    </div>
  );
}
