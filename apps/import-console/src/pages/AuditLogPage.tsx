import { useEffect, useState } from "react";
import { listAdminAuditLog, type AdminAuditLogItem } from "../api/admin";
import { EmptyState, PageHeader, StatusBadge } from "../components/ui";

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN");
}

export function AuditLogPage() {
  const [items, setItems] = useState<AdminAuditLogItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await listAdminAuditLog();
      setItems(response.items);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "审计日志加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  return (
    <div className="page">
      <PageHeader
        title="审计日志"
        description="查看导入任务、配置保存、模型连接测试和任务事件。"
        actions={<button className="button" type="button" onClick={() => void load()} disabled={loading}>{loading ? "刷新中" : "刷新"}</button>}
      />
      {error ? <div className="notice notice-error">{error}</div> : null}
      <section className="panel">
        <div className="panel-title">事件</div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>类型</th>
                <th>状态</th>
                <th>标题</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td>{formatDate(item.createdAt)}</td>
                  <td>{item.kind}</td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{item.title}</td>
                  <td className="audit-detail">{item.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!items.length && !loading ? <EmptyState>暂无审计事件。</EmptyState> : null}
        </div>
      </section>
    </div>
  );
}
