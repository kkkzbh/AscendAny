import type { NoteStreamState } from "@/stores/notesStore";

interface NotesStreamingViewProps {
  streaming: NoteStreamState;
}

interface RenderedLine {
  key: string;
  kind: "unchanged" | "removed" | "added-partial" | "added-full" | "added-pending";
  text: string;
  showCaret: boolean;
}

function buildLines(streaming: NoteStreamState): RenderedLine[] {
  const lines: RenderedLine[] = [];
  let addedOffset = 0;
  const isCollapsing = streaming.phase !== "deleting";
  let caretAttached = false;
  streaming.segments.forEach((segment, segmentIndex) => {
    if (segment.kind === "unchanged") {
      segment.lines.forEach((line, lineIndex) => {
        lines.push({
          key: `u-${segmentIndex}-${lineIndex}`,
          kind: "unchanged",
          text: line,
          showCaret: false,
        });
      });
      return;
    }
    if (segment.kind === "removed") {
      if (isCollapsing) {
        return;
      }
      segment.lines.forEach((line, lineIndex) => {
        lines.push({
          key: `r-${segmentIndex}-${lineIndex}`,
          kind: "removed",
          text: line,
          showCaret: false,
        });
      });
      return;
    }
    const segmentChars = segment.lines.reduce(
      (acc, line) => acc + line.length,
      0,
    ) + Math.max(segment.lines.length - 1, 0);
    const localStart = addedOffset;
    const localEnd = addedOffset + segmentChars;
    addedOffset = localEnd;
    const fullyTyped = streaming.typedChars >= localEnd;
    const noneTyped = streaming.typedChars <= localStart;
    if (streaming.phase === "deleting" || noneTyped) {
      segment.lines.forEach((_, lineIndex) => {
        lines.push({
          key: `a-${segmentIndex}-${lineIndex}-pending`,
          kind: "added-pending",
          text: "",
          showCaret: false,
        });
      });
      return;
    }
    if (fullyTyped) {
      segment.lines.forEach((line, lineIndex) => {
        lines.push({
          key: `a-${segmentIndex}-${lineIndex}-full`,
          kind: "added-full",
          text: line,
          showCaret: false,
        });
      });
      return;
    }
    let remaining = streaming.typedChars - localStart;
    segment.lines.forEach((line, lineIndex) => {
      if (remaining <= 0) {
        lines.push({
          key: `a-${segmentIndex}-${lineIndex}-pending`,
          kind: "added-pending",
          text: "",
          showCaret: false,
        });
        return;
      }
      const take = Math.min(line.length, remaining);
      remaining -= take;
      const isPartial = take < line.length;
      const isLastConsumed = remaining <= 0 && !caretAttached;
      if (isLastConsumed) {
        caretAttached = true;
      }
      lines.push({
        key: `a-${segmentIndex}-${lineIndex}-partial`,
        kind: isPartial ? "added-partial" : "added-full",
        text: line.slice(0, take),
        showCaret: isLastConsumed,
      });
      if (remaining > 0) {
        remaining -= 1;
      }
    });
  });
  return lines;
}

export function NotesStreamingView({ streaming }: NotesStreamingViewProps) {
  const lines = buildLines(streaming);
  return (
    <div
      className={`notes-streaming notes-streaming--${streaming.phase}`}
      role="status"
      aria-live="polite"
    >
      <div className="notes-streaming-banner">
        <span className="notes-streaming-spark" aria-hidden>
          ✦
        </span>
        <span>笔记更新中…</span>
      </div>
      <pre className="notes-streaming-pre">
        {lines.map((line) => {
          if (line.kind === "added-pending") {
            return (
              <span key={line.key} className="notes-line notes-line--pending">
                {"​"}
                {"\n"}
              </span>
            );
          }
          const baseClass =
            line.kind === "removed"
              ? "notes-line notes-line--removed"
              : line.kind === "added-partial"
                ? "notes-line notes-line--added"
                : line.kind === "added-full"
                  ? "notes-line notes-line--added"
                  : "notes-line";
          return (
            <span key={line.key} className={baseClass}>
              {line.text || "​"}
              {line.showCaret ? <span className="notes-streaming-caret" aria-hidden /> : null}
              {"\n"}
            </span>
          );
        })}
      </pre>
    </div>
  );
}
