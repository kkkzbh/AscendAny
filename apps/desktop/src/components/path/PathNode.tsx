import type { CSSProperties } from "react";
import type { NodeStatus, NodeViewModel } from "@/types/path";

interface PathNodeProps {
  vm: NodeViewModel;
  cx: number;
  cy: number;
  size: number;
  isFocused: boolean;
  isGhosted: boolean;
  isAdded: boolean;
  isLeaving: boolean;
  iconSrc: string | null;
  labelSide: "left" | "right";
  onSelect: (point: string) => void;
}

const STATUS_LABELS: Record<NodeStatus, string> = {
  locked: "未解锁",
  current: "进行中",
  mastered: "已掌握",
};

export function PathNode({
  vm,
  cx,
  cy,
  size,
  isFocused,
  isGhosted,
  isAdded,
  isLeaving,
  iconSrc,
  labelSide,
  onSelect,
}: PathNodeProps) {
  const radius = size / 2;
  const ringRadius = radius - 3;
  const ringCircumference = 2 * Math.PI * ringRadius;
  const masteryFraction = Math.max(0, Math.min(1, vm.mastery));
  const dashOffset = ringCircumference * (1 - masteryFraction);
  const stateClassNames = [
    "path-node",
    `path-node--${vm.status}`,
    isFocused ? "is-focused" : "",
    isGhosted ? "is-ghosted" : "",
    isAdded ? "is-entering" : "",
    isLeaving ? "is-leaving" : "",
    vm.isTarget ? "is-target" : "",
    labelSide === "left" ? "is-label-left" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const style: CSSProperties = {
    transform: `translate(${cx}px, ${cy}px)`,
    width: size,
    height: size,
  };

  const accessibleLabel = `${vm.point}，${STATUS_LABELS[vm.status]}，掌握度 ${(
    masteryFraction * 100
  ).toFixed(0)}%`;

  return (
    <button
      type="button"
      className={stateClassNames}
      style={style}
      onClick={() => onSelect(vm.point)}
      aria-label={accessibleLabel}
    >
      <span className="path-node__halo" aria-hidden />
      <svg
        className="path-node__ring"
        viewBox={`0 0 ${size} ${size}`}
        width={size}
        height={size}
        aria-hidden
      >
        <circle
          className="path-node__ring-track"
          cx={radius}
          cy={radius}
          r={ringRadius}
          fill="none"
        />
        <circle
          className="path-node__ring-fill"
          cx={radius}
          cy={radius}
          r={ringRadius}
          fill="none"
          strokeDasharray={ringCircumference}
          strokeDashoffset={dashOffset}
          transform={`rotate(-90 ${radius} ${radius})`}
        />
      </svg>
      <span className="path-node__core" aria-hidden>
        {iconSrc ? (
          <img className="path-node__icon" src={iconSrc} alt="" draggable={false} />
        ) : (
          <span className="path-node__spark" />
        )}
      </span>
      <span className="path-node__label" aria-hidden>
        {vm.point}
      </span>
      {vm.status === "mastered" ? (
        <span className="path-node__badge" aria-hidden />
      ) : null}
    </button>
  );
}
