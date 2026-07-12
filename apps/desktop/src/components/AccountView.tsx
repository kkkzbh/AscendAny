import { useCallback, useEffect, useState, type FormEvent } from "react";
import type { AccountSession } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import {
  loadAccountSessions,
  revokeSession,
  saveDisplayName,
} from "../api/operations";
import { useSession } from "../session/context";

function formatTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).byteLength;
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
    const displayNameBytes = utf8Length(canonicalDisplayName);
    if (displayNameBytes < 1 || displayNameBytes > 64) {
      setProfileError("显示名称必须包含 1 至 64 个 UTF-8 字节");
      return;
    }

    setProfileBusy(true);
    setProfileError(null);
    setProfileMessage(null);
    try {
      const updated = await saveDisplayName(session, canonicalDisplayName);
      replaceAccount(updated);
      setProfileMessage("个人资料已更新");
    } catch (saveError) {
      setProfileError(apiFailureMessage(saveError));
    } finally {
      setProfileBusy(false);
    }
  };

  const revoke = async (item: AccountSession) => {
    const prompt = item.current
      ? "确定退出当前设备吗？"
      : "确定撤销这个设备的会话吗？";
    if (!window.confirm(prompt)) return;

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
        <span className="account-avatar" aria-hidden="true">
          {account.displayName.slice(0, 1).toUpperCase()}
        </span>
        <div>
          <span className="role-badge">{account.role === "student" ? "学生" : "管理员"}</span>
          <h2>{account.displayName}</h2>
          <p>
            @{account.username}
            {account.studentNumber !== null ? " · " + account.studentNumber : ""}
          </p>
        </div>
      </section>

      <section className="panel-card">
        <header className="section-heading">
          <div>
            <span className="eyebrow">PROFILE</span>
            <h2>个人资料</h2>
            <p>用户名、角色与学号由管理员维护。</p>
          </div>
        </header>
        <form className="profile-form" onSubmit={saveProfile}>
          <label>
            <span>显示名称</span>
            <input
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              disabled={profileBusy}
            />
          </label>
          <label>
            <span>用户名</span>
            <input value={account.username} readOnly />
          </label>
          {account.studentNumber !== null ? (
            <label>
              <span>学号</span>
              <input value={account.studentNumber} readOnly />
            </label>
          ) : null}
          {profileError !== null ? (
            <div className="form-error full-row" role="alert">{profileError}</div>
          ) : null}
          {profileMessage !== null ? (
            <div className="form-success full-row" role="status">{profileMessage}</div>
          ) : null}
          <div className="form-actions full-row">
            <button
              className="primary-button compact"
              type="submit"
              disabled={profileBusy || displayName.trim() === account.displayName}
            >
              {profileBusy ? "保存中…" : "保存资料"}
            </button>
          </div>
        </form>
      </section>

      <section className="panel-card">
        <header className="section-heading">
          <div>
            <span className="eyebrow">SESSIONS</span>
            <h2>登录设备</h2>
            <p>最多显示最近 100 条会话记录。</p>
          </div>
          <button
            className="text-button"
            type="button"
            disabled={sessionsLoading}
            onClick={() => void loadSessions()}
          >
            刷新列表
          </button>
        </header>

        {sessionsError !== null ? (
          <div className="form-error" role="alert">{sessionsError}</div>
        ) : null}

        {sessionsLoading ? (
          <div className="inline-loading" role="status">
            <span className="loading-dot" />
            正在读取会话…
          </div>
        ) : sessions.length === 0 ? (
          <p className="empty-copy">没有会话记录。</p>
        ) : (
          <div className="session-list">
            {sessions.map((item) => (
              <article className="session-item" key={item.id}>
                <span className="session-icon" aria-hidden="true">▣</span>
                <div className="session-copy">
                  <div>
                    <strong>
                      {item.current ? "当前设备" : "会话 " + item.id.slice(0, 8)}
                    </strong>
                    <span className={item.active ? "session-active" : "session-ended"}>
                      {item.active ? "有效" : "已结束"}
                    </span>
                  </div>
                  <span>最近使用 {formatTime(item.lastSeenAt)}</span>
                  <span>
                    创建于 {formatTime(item.createdAt)}
                    <span aria-hidden="true"> · </span>
                    到期 {formatTime(item.expiresAt)}
                  </span>
                  {item.revocationReason !== null ? (
                    <span>结束原因：{item.revocationReason}</span>
                  ) : null}
                </div>
                {item.active ? (
                  <button
                    className="danger-button"
                    type="button"
                    disabled={revoking !== null}
                    onClick={() => void revoke(item)}
                  >
                    {revoking === item.id
                      ? "处理中…"
                      : item.current
                        ? "退出"
                        : "撤销"}
                  </button>
                ) : null}
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
