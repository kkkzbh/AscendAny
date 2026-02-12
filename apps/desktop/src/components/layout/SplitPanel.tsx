import React, { useCallback, useRef, useState } from "react";

interface SplitPanelProps {
  left: React.ReactNode;
  right: React.ReactNode;
  defaultRatio?: number;
  minRatio?: number;
}

export function SplitPanel({
  left,
  right,
  defaultRatio = 0.6,
  minRatio = 0.3,
}: SplitPanelProps) {
  const [ratio, setRatio] = useState(defaultRatio);
  const containerRef = useRef<HTMLDivElement>(null);
  const dragging = useRef(false);

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      dragging.current = true;

      const onMouseMove = (ev: MouseEvent) => {
        if (!dragging.current || !containerRef.current) return;
        const rect = containerRef.current.getBoundingClientRect();
        let newRatio = (ev.clientX - rect.left) / rect.width;
        newRatio = Math.max(minRatio, Math.min(1 - minRatio, newRatio));
        setRatio(newRatio);
      };

      const onMouseUp = () => {
        dragging.current = false;
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [minRatio],
  );

  return (
    <div ref={containerRef} className="flex h-full w-full gap-2 overflow-hidden">
      {/* Left panel */}
      <div
        className="flex h-full overflow-hidden rounded-2xl bg-white/60 shadow-sm ring-1 ring-black/[0.04]"
        style={{ width: `calc(${ratio * 100}% - 5px)` }}
      >
        {left}
      </div>

      {/* Drag handle */}
      <div
        className="relative flex h-full w-1 shrink-0 cursor-col-resize items-center justify-center"
        onMouseDown={onMouseDown}
      >
        <div className="h-6 w-0.5 rounded-full bg-[var(--text-muted)]/20 transition-all duration-150 hover:h-10 hover:bg-[var(--text-muted)]/40" />
        <div className="absolute inset-y-0 -left-2 -right-2" />
      </div>

      {/* Right panel */}
      <div className="flex h-full flex-1 overflow-hidden rounded-2xl bg-white/60 shadow-sm ring-1 ring-black/[0.04]">
        {right}
      </div>
    </div>
  );
}
