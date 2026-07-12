import { useCallback, useEffect, useState } from "react";
import { getAuditEvents, type AuditEvent } from "../api/administration";
import { EmptyState, PageHeader } from "../components/ui";

function formatTime(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN");
}

export function AuditPage() {
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (cursor?: string) => {
    setLoading(true);
    setError(null);
    try {
      const page = await getAuditEvents(30, cursor);
      setItems((current) => cursor ? [...current, ...page.items] : page.items);
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "审计日志加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="page">
      <PageHeader
        title="审计日志"
        description="按 durable sequence 展示 immutable 安全与管理事件，响应不会包含 credential。"
        actions={<button className="button" type="button" onClick={() => void load()} disabled={loading}>刷新</button>}
      />
      {error ? <div className="notice notice-error" role="alert">{error}</div> : null}
      <section className="panel">
        <div className="table-wrap">
          <table>
            <thead><tr><th>ID</th><th>时间</th><th>事件</th><th>操作者</th><th>Payload</th></tr></thead>
            <tbody>
              {items.map((event) => (
                <tr key={event.id}>
                  <td>{event.id}</td>
                  <td>{formatTime(event.occurredAt)}</td>
                  <td><strong>{event.type}</strong></td>
                  <td title={event.actorAccountId ?? ""}>{event.actorAccountId?.slice(0, 8) ?? "system"}</td>
                  <td><code className="audit-payload">{JSON.stringify(event.payload)}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && items.length === 0 ? <EmptyState>当前没有审计事件。</EmptyState> : null}
        </div>
      </section>
      {nextCursor ? <button className="button button-ghost load-more" type="button" disabled={loading} onClick={() => void load(nextCursor)}>加载更多</button> : null}
    </div>
  );
}
