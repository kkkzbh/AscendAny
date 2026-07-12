import { useCallback, useEffect, useState, type FormEvent } from "react";
import type { AccountSession } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadAccountSessions, revokeSession, saveDisplayName } from "../api/operations";
import { useSession } from "../session/SessionContext";

function formatTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function AccountView() {
  const { session, account, replaceAccount } = useSession();
  const [displayName, setDisplayName] = useState(account?.displayName ?? "");
  const [profileBusy, setProfileBusy] = useState(false);
  const [profileMessage, setProfileMessage] = useState<string | null>(null);
  const [profileError, setProfileError] = useState<string | null>(null);
  const [sessions, setSessions] = useState<AccountSession[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<string | null>(null);

  useEffect(() => {
    if (account !== null) setDisplayName(account.displayName);
  }, [account]);

  const loadSessions = useCallback(async () => {
    setSessionsLoading(true);
    setSessionsError(null);
    try {
      const result = await loadAccountSessions(session);
      setSessions(result.items);
    } catch (loadError) {
      setSessionsError(apiFailureMessage(loadError));
    } finally {
      setSessionsLoading(false);
    }
  }, [session]);

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  if (account === null) return null;

  const saveProfile = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const canonicalDisplayName = displayName.trim();
    if (canonicalDisplayName.length === 0) {
      setProfileError("显示名称不能为空");
      return;
    }
    setProfileBusy(true);
    setProfileError(null);
    setProfileMessage(null);
    try {
      const updated = await saveDisplayName(session, canonicalDisplayName);
      replaceAccount(updated);
      setProfileMessage("资料已更新");
    } catch (saveError) {
      setProfileError(apiFailureMessage(saveError));
    } finally {
      setProfileBusy(false);
    }
  };

  const revoke = async (item: AccountSession) => {
    const confirmed = window.confirm(item.current ? "确定退出当前设备吗？" : "确定撤销这个设备的会话吗？");
    if (!confirmed) return;
    setRevoking(item.id);
    setSessionsError(null);
    try {
      await revokeSession(session, item.id, item.current);
      if (!item.current) await loadSessions();
    } catch (revokeError) {
      setSessionsError(apiFailureMessage(revokeError));
    } finally {
      setRevoking(null);
    }
  };

  return (
    <div className="view-stack">
      <section className="account-identity">
        <div className="account-avatar" aria-hidden="true">{account.displayName.slice(0, 1).toUpperCase()}</div>
        <div><span className="role-badge">{account.role === "student" ? "学生" : "管理员"}</span><h2>{account.displayName}</h2><p>@{account.username}{account.studentNumber ? ` · ${account.studentNumber}` : ""}</p></div>
      </section>

      <section className="panel-card">
        <div className="section-heading"><div><span className="eyebrow">PROFILE</span><h2>个人资料</h2></div></div>
        <form className="profile-form" onSubmit={saveProfile}>
          <label><span>显示名称</span><input value={displayName} onChange={(event) => setDisplayName(event.target.value)} disabled={profileBusy} /></label>
          {profileError ? <div className="form-error" role="alert">{profileError}</div> : null}
          {profileMessage ? <div className="form-success" role="status">{profileMessage}</div> : null}
          <button className="primary-button compact" type="submit" disabled={profileBusy || displayName.trim() === account.displayName}>{profileBusy ? "保存中…" : "保存资料"}</button>
        </form>
      </section>

      <section className="panel-card">
        <div className="section-heading">
          <div><span className="eyebrow">SESSIONS</span><h2>登录设备</h2><p>最多显示最近 100 条会话记录。</p></div>
          <button className="text-button" type="button" disabled={sessionsLoading} onClick={() => void loadSessions()}>刷新</button>
        </div>
        {sessionsError ? <div className="form-error" role="alert">{sessionsError}</div> : null}
        {sessionsLoading ? <div className="inline-loading" role="status"><span className="loading-dot" />正在读取会话…</div> : (
          <div className="session-list">
            {sessions.map((item) => (
              <article className="session-item" key={item.id}>
                <div className="session-icon" aria-hidden="true">▣</div>
                <div className="session-copy">
                  <div><strong>{item.current ? "当前设备" : `会话 ${item.id.slice(0, 8)}`}</strong><span className={item.active ? "session-active" : "session-ended"}>{item.active ? "有效" : "已结束"}</span></div>
                  <span>最近使用 {formatTime(item.lastSeenAt)}</span>
                  <span>创建于 {formatTime(item.createdAt)} · 到期 {formatTime(item.expiresAt)}</span>
                  {item.revocationReason ? <span>结束原因：{item.revocationReason}</span> : null}
                </div>
                {item.active ? <button className="danger-button" type="button" disabled={revoking !== null} onClick={() => void revoke(item)}>{revoking === item.id ? "处理中…" : item.current ? "退出" : "撤销"}</button> : null}
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
