import React, { useCallback, useEffect, useRef, useState } from "react";

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
  const [isStacked, setIsStacked] = useState(() =>
    typeof window !== "undefined" ? window.innerWidth < 960 : false,
  );
  const containerRef = useRef<HTMLDivElement>(null);
  const dragging = useRef(false);

  useEffect(() => {
    const media = window.matchMedia("(max-width: 959px)");
    const sync = () => setIsStacked(media.matches);
    sync();

    media.addEventListener("change", sync);
    return () => {
      media.removeEventListener("change", sync);
    };
  }, []);

  const updateRatio = useCallback(
    (clientX: number) => {
      if (!containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      let newRatio = (clientX - rect.left) / rect.width;
      newRatio = Math.max(minRatio, Math.min(1 - minRatio, newRatio));
      setRatio(newRatio);
    },
    [minRatio],
  );

  useEffect(() => {
    if (isStacked) {
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
  }, [isStacked]);

  const onMouseDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (isStacked) return;
      e.preventDefault();
      dragging.current = true;

      const onPointerMove = (ev: PointerEvent) => {
        if (!dragging.current) return;
        updateRatio(ev.clientX);
      };

      const onPointerUp = () => {
        dragging.current = false;
        window.removeEventListener("pointermove", onPointerMove);
        window.removeEventListener("pointerup", onPointerUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      window.addEventListener("pointermove", onPointerMove);
      window.addEventListener("pointerup", onPointerUp);
    },
    [isStacked, updateRatio],
  );

  const onHandleKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      if (isStacked) return;
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
      event.preventDefault();
      const step = event.shiftKey ? 0.05 : 0.02;
      const delta = event.key === "ArrowLeft" ? -step : step;
      setRatio((current) =>
        Math.max(minRatio, Math.min(1 - minRatio, current + delta)),
      );
    },
    [isStacked, minRatio],
  );

  return (
    <div
      ref={containerRef}
      className="split-panel flex h-full w-full gap-0 overflow-hidden max-[959px]:flex-col"
    >
      <div
        className="panel-shell panel-chat flex h-full min-w-0 overflow-hidden"
        style={
          isStacked
            ? undefined
            : {
                flexBasis: `${ratio * 100}%`,
                flexGrow: 0,
                flexShrink: 0,
              }
        }
      >
        {left}
      </div>

      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="调整面板宽度"
        tabIndex={isStacked ? -1 : 0}
        className="split-handle relative flex h-full w-1 shrink-0 cursor-col-resize items-center justify-center max-[959px]:hidden"
        onPointerDown={onMouseDown}
        onKeyDown={onHandleKeyDown}
      >
        <div className="split-handle-bar h-10 w-px rounded-full" />
      </div>

      <div className="panel-shell panel-metrics flex h-full min-w-0 flex-1 overflow-hidden">
        {right}
      </div>
    </div>
  );
}
