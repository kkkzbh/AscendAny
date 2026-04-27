import { useEffect, useState } from "react";
import { listAdminAccounts, type AdminAccountSummary } from "../api/admin";
import { EmptyState, PageHeader, StatusBadge } from "../components/ui";
import type { AccountInfo } from "../hooks/useAuth";

function formatDate(value: string | null): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN");
}

export function UsersPage({ account }: { account: AccountInfo | null }) {
  const [items, setItems] = useState<AdminAccountSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await listAdminAccounts();
      setItems(response.items);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "账户列表加载失败");
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
        title="用户与权限"
        description="查看账户、管理员状态和学生身份绑定。第一版不在控制台写入权限。"
        actions={<button className="button" type="button" onClick={() => void load()} disabled={loading}>{loading ? "刷新中" : "刷新"}</button>}
      />
      {error ? <div className="notice notice-error">{error}</div> : null}
      <section className="panel">
        <div className="panel-title">当前账号</div>
        <div className="summary-grid">
          <div className="metric-tile"><span>用户名</span><strong>{account?.username ?? "-"}</strong></div>
          <div className="metric-tile"><span>管理员</span><strong>{account?.isAdmin ? "是" : "否"}</strong></div>
          <div className="metric-tile"><span>绑定学号</span><strong>{account?.studentId ?? "-"}</strong></div>
        </div>
      </section>
      <section className="panel">
        <div className="panel-title">账户列表</div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账户</th>
                <th>显示名</th>
                <th>状态</th>
                <th>角色</th>
                <th>学生身份</th>
                <th>最近登录</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.accountId}>
                  <td>{item.username}</td>
                  <td>{item.displayName}</td>
                  <td><StatusBadge status={item.isActive ? "success" : "failed"} /></td>
                  <td>{item.isAdmin ? "管理员" : "普通用户"}</td>
                  <td>{item.studentId || item.ptaNickname || "-"}</td>
                  <td>{formatDate(item.lastLoginAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!items.length && !loading ? <EmptyState>暂无账户数据。</EmptyState> : null}
        </div>
      </section>
    </div>
  );
}
