import type { ReactNode } from "react";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-header">
      <div>
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </div>
  );
}

export function StatusBadge({ status }: { status: string }) {
  const normalized = status.toLowerCase();
  const className = normalized.includes("success") || normalized === "done"
    ? "status status-success"
    : normalized.includes("fail") || normalized.includes("error")
      ? "status status-error"
      : normalized.includes("missing") || normalized.includes("pending")
        ? "status status-warning"
        : "status status-neutral";
  const labelMap: Record<string, string> = {
    success: "成功",
    failed: "失败",
    missing: "缺失",
    done: "完成",
    running: "运行中",
    pending: "等待",
    error: "错误",
  };
  return <span className={className}>{labelMap[normalized] ?? status}</span>;
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="empty-state">{children}</div>;
}

export function Alert({
  tone = "error",
  children,
}: {
  tone?: "error" | "success" | "warning";
  children: ReactNode;
}) {
  return <div className={`notice notice-${tone}`}>{children}</div>;
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint ? <span className="field-hint">{hint}</span> : null}
    </label>
  );
}
