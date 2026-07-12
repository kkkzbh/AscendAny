import { useCallback, useEffect, useState } from "react";
import {
  changeManagedAccountState,
  getManagedAccounts,
  type ManagedAccount,
} from "../api/administration";
import { EmptyState, PageHeader } from "../components/ui";

function formatTime(value: string | null): string {
  if (value === null) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN");
}

export function AccountsPage() {
  const [items, setItems] = useState<ManagedAccount[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [changing, setChanging] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (cursor?: string) => {
    setLoading(true);
    setError(null);
    try {
      const page = await getManagedAccounts(30, cursor);
      setItems((current) => cursor ? [...current, ...page.items] : page.items);
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "账户加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const changeState = async (account: ManagedAccount) => {
    const disabled = account.disabledAt === null;
    setChanging(account.id);
    setError(null);
    try {
      const updated = await changeManagedAccountState(account.id, disabled);
      setItems((current) => current.map((item) => item.id === updated.id ? updated : item));
    } catch (mutationError) {
      setError(mutationError instanceof Error ? mutationError.message : "账户状态修改失败");
    } finally {
      setChanging(null);
    }
  };

  return (
    <div className="page">
      <PageHeader
        title="账户管理"
        description="禁用会立即撤销该账户的全部会话与 refresh credential；恢复后需要重新登录。"
        actions={<button className="button" type="button" onClick={() => void load()} disabled={loading}>刷新</button>}
      />
      {error ? <div className="notice notice-error" role="alert">{error}</div> : null}
      <section className="panel">
        <div className="table-wrap">
          <table>
            <thead>
              <tr><th>账户</th><th>角色</th><th>学号</th><th>状态</th><th>活动会话</th><th>更新时间</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((account) => (
                <tr key={account.id}>
                  <td><strong>{account.displayName}</strong><span className="muted-block">{account.username}</span></td>
                  <td>{account.role}</td>
                  <td>{account.studentNumber ?? "-"}</td>
                  <td>{account.disabledAt === null ? "正常" : `已禁用 · ${formatTime(account.disabledAt)}`}</td>
                  <td>{account.activeSessionCount}</td>
                  <td>{formatTime(account.updatedAt)}</td>
                  <td>
                    <button
                      className={`button ${account.disabledAt === null ? "button-danger" : "button-primary"}`}
                      type="button"
                      disabled={changing !== null}
                      onClick={() => void changeState(account)}
                    >
                      {changing === account.id ? "处理中" : account.disabledAt === null ? "禁用" : "恢复"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && items.length === 0 ? <EmptyState>当前没有账户。</EmptyState> : null}
        </div>
      </section>
      {nextCursor ? <button className="button button-ghost load-more" type="button" disabled={loading} onClick={() => void load(nextCursor)}>加载更多</button> : null}
    </div>
  );
}
