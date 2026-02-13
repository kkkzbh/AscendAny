import type { RatingInfo } from "@/types/metrics";

interface RatingDisplayProps {
  rating: RatingInfo;
}

function getRatingColor(rating: number): string {
  if (rating >= 1200) return "#d97706";
  if (rating >= 1000) return "#0f766e";
  if (rating >= 800) return "#0369a1";
  return "#64748b";
}

function getRatingLabel(rating: number): string {
  if (rating >= 1200) return "Expert";
  if (rating >= 1000) return "Advanced";
  if (rating >= 800) return "Intermediate";
  return "Beginner";
}

export function RatingDisplay({ rating }: RatingDisplayProps) {
  const color = getRatingColor(rating.current);
  const label = getRatingLabel(rating.current);
  const delta = rating.lastDelta;

  return (
    <div className="relative rounded-xl bg-[var(--surface-raised)] px-5 py-4 ring-1 ring-[var(--border-subtle)]">
      <div
        className="absolute inset-0 rounded-xl opacity-[0.08]"
        style={{
          background: `radial-gradient(circle at 50% 30%, ${color}, transparent 70%)`,
        }}
      />
      <div className="relative flex flex-col items-center gap-1">
        <span className="text-[10px] font-semibold tracking-[0.12em] text-[var(--text-soft)] uppercase">
          综合 Rating
        </span>
        <span
          className="text-4xl font-bold tabular-nums tracking-tight"
          style={{ color, lineHeight: 1.08 }}
        >
          {rating.current}
        </span>
        <div className="flex items-center gap-1.5">
          <span
              className="rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide"
            style={{ color, backgroundColor: `${color}12` }}
          >
            {label}
          </span>
          {delta !== null && (
            <span
              className="rounded px-1.5 py-0.5 text-[11px] font-semibold tabular-nums"
              style={{
                color:
                  delta >= 0
                    ? "var(--rating-positive)"
                    : "var(--rating-negative)",
                backgroundColor:
                  delta >= 0
                    ? "rgba(16, 185, 129, 0.08)"
                    : "rgba(244, 63, 94, 0.08)",
              }}
            >
              {delta >= 0 ? "+" : ""}
              {delta}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
