import type { RatingInfo } from "@/types/metrics";

interface RatingDisplayProps {
  rating: RatingInfo;
}

function getRatingColor(rating: number): string {
  if (rating >= 1200) return "#ef4444";
  if (rating >= 1000) return "#f59e0b";
  if (rating >= 800) return "#22c55e";
  return "#6b7280";
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
    <div className="relative overflow-hidden rounded-xl bg-white/40 px-5 py-4 shadow-sm ring-1 ring-black/[0.04]">
      <div
        className="absolute inset-0 opacity-[0.05]"
        style={{
          background: `radial-gradient(circle at 50% 30%, ${color}, transparent 70%)`,
        }}
      />
      <div className="relative flex flex-col items-center gap-1">
        <span className="text-[10px] font-semibold tracking-widest text-[var(--text-muted)] uppercase">
          综合 Rating
        </span>
        <span
          className="text-4xl font-bold tabular-nums tracking-tight"
          style={{ color }}
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
