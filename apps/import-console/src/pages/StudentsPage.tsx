import { type FormEvent, useCallback, useEffect, useState } from "react";
import type { IssuedEnrollment } from "@ascendany/sdk";
import {
  getManagedStudents,
  issueManagedEnrollmentClaim,
  revokeManagedEnrollmentClaim,
  type ManagedStudent,
} from "../api/administration";
import { EmptyState, PageHeader } from "../components/ui";

const DEFAULT_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS = 86_400;
const MIN_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS = 1;
const MAX_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS = 7 * 86_400;

type ClaimDraft = {
  studentNumber: string;
  username: string;
  displayName: string;
  expiresInSeconds: string;
};

type IssuedClaim = {
  grant: IssuedEnrollment["grant"];
  token: IssuedEnrollment["token"] | null;
};

function formatTime(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN");
}

export function StudentsPage() {
  const [items, setItems] = useState<ManagedStudent[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [draft, setDraft] = useState<ClaimDraft | null>(null);
  const [issuedClaim, setIssuedClaim] = useState<IssuedClaim | null>(null);
  const [issuing, setIssuing] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [claimError, setClaimError] = useState<string | null>(null);
  const [claimStatus, setClaimStatus] = useState<string | null>(null);

  const load = useCallback(async (cursor?: string) => {
    setLoading(true);
    setListError(null);
    try {
      const page = await getManagedStudents(30, cursor);
      setItems((current) => cursor ? [...current, ...page.items] : page.items);
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setListError(loadError instanceof Error ? loadError.message : "学生加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const selectedStudent = draft === null
    ? null
    : items.find((student) => student.studentNumber === draft.studentNumber) ?? null;

  const beginIssue = (student: ManagedStudent) => {
    if (student.account !== null || issuedClaim !== null) return;
    setClaimError(null);
    setClaimStatus(null);
    setDraft({
      studentNumber: student.studentNumber,
      username: "",
      displayName: student.sourceDisplayName?.trim() ?? "",
      expiresInSeconds: String(DEFAULT_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS),
    });
  };

  const submitIssue = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (draft === null || issuing) return;
    const currentStudent = items.find((student) => student.studentNumber === draft.studentNumber);
    if (currentStudent === undefined || currentStudent.account !== null) {
      setDraft(null);
      setClaimError("该学生已经绑定账号，不能签发 enrollment claim。请刷新学生列表确认当前状态。");
      return;
    }

    const expiresInSeconds = Number(draft.expiresInSeconds);
    if (
      !Number.isSafeInteger(expiresInSeconds)
      || expiresInSeconds < MIN_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS
      || expiresInSeconds > MAX_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS
    ) {
      setClaimError(
        `有效期必须是 ${MIN_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS} 到 ${MAX_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS} 秒之间的整数。`,
      );
      return;
    }

    setIssuing(true);
    setClaimError(null);
    setClaimStatus(null);
    try {
      const enrollment = await issueManagedEnrollmentClaim({
        username: draft.username.trim(),
        displayName: draft.displayName.trim(),
        studentNumber: draft.studentNumber,
        expiresInSeconds,
      });
      setIssuedClaim({ grant: enrollment.grant, token: enrollment.token });
      setDraft(null);
    } catch (issueError) {
      setClaimError(issueError instanceof Error ? issueError.message : "Enrollment claim 签发失败");
    } finally {
      setIssuing(false);
    }
  };

  const copyToken = async () => {
    if (issuedClaim === null || issuedClaim.token === null) return;
    setClaimError(null);
    setClaimStatus(null);
    if (navigator.clipboard === undefined) {
      setClaimError("当前浏览器未提供 Clipboard API，请手动选择并复制 token。");
      return;
    }
    try {
      await navigator.clipboard.writeText(issuedClaim.token);
      setClaimStatus("Token 已复制到剪贴板。此操作仅由本次按钮点击触发。");
    } catch (copyError) {
      setClaimError(copyError instanceof Error ? copyError.message : "Token 复制失败，请手动复制");
    }
  };

  const hideToken = () => {
    setIssuedClaim((current) => current === null ? null : { ...current, token: null });
    setClaimError(null);
    setClaimStatus("一次性 token 已从当前页面内存清除，无法再次显示。");
  };

  const closeIssuedClaim = () => {
    setIssuedClaim(null);
    setClaimError(null);
    setClaimStatus("当前 claim 结果及一次性 token 已从页面内存清除。");
  };

  const revokeClaim = async () => {
    if (issuedClaim === null || revoking) return;
    const grantId = issuedClaim.grant.id;
    setRevoking(true);
    setClaimError(null);
    setClaimStatus(null);
    try {
      await revokeManagedEnrollmentClaim(grantId);
      setIssuedClaim(null);
      setClaimStatus(`Grant ${grantId} 已撤销，一次性 token 已从页面内存清除。`);
    } catch (revokeError) {
      setClaimError(revokeError instanceof Error ? revokeError.message : "Enrollment claim 撤销失败");
    } finally {
      setRevoking(false);
    }
  };

  return (
    <div className="page">
      <PageHeader
        title="学生身份"
        description="展示 Pintia 导入身份、当前账号绑定与已发布 analytics rating。未绑定账号的学生可签发 enrollment claim。"
        actions={<button className="button" type="button" onClick={() => void load()} disabled={loading}>刷新</button>}
      />
      {listError ? <div className="notice notice-error" role="alert">{listError}</div> : null}
      {claimError ? <div className="notice notice-error" role="alert">{claimError}</div> : null}
      {claimStatus ? <div className="notice notice-success" role="status">{claimStatus}</div> : null}

      {issuedClaim === null ? null : (
        <section className="panel enrollment-result" aria-labelledby="enrollment-result-title">
          <div className="panel-title" id="enrollment-result-title">一次性 enrollment claim</div>
          <div className="enrollment-result-body">
            <div className="notice notice-warning">
              Token 只在当前页面内存中保留。刷新浏览器、离开页面、隐藏或关闭结果后均无法恢复。
            </div>
            <dl className="enrollment-grant-details">
              <div><dt>学生</dt><dd>{issuedClaim.grant.studentNumber} · {issuedClaim.grant.displayName}</dd></div>
              <div><dt>用户名</dt><dd>{issuedClaim.grant.username}</dd></div>
              <div><dt>Grant ID</dt><dd><code>{issuedClaim.grant.id}</code></dd></div>
              <div><dt>过期时间</dt><dd>{formatTime(issuedClaim.grant.expiresAt)}</dd></div>
            </dl>
            {issuedClaim.token === null ? (
              <div className="notice notice-warning">Token 已清除；Grant ID 仍保留在当前内存中，可继续执行撤销。</div>
            ) : (
              <div className="enrollment-token-block">
                <span>一次性 token</span>
                <code data-testid="enrollment-token">{issuedClaim.token}</code>
              </div>
            )}
            <div className="enrollment-result-actions">
              {issuedClaim.token === null ? null : (
                <>
                  <button className="button button-primary" type="button" onClick={() => void copyToken()}>复制 token</button>
                  <button className="button" type="button" onClick={hideToken}>已保存，隐藏 token</button>
                </>
              )}
              <button className="button button-danger" type="button" disabled={revoking} onClick={() => void revokeClaim()}>
                {revoking ? "撤销中" : "撤销并清除"}
              </button>
              <button className="button button-ghost" type="button" disabled={revoking} onClick={closeIssuedClaim}>
                关闭并清除页面结果
              </button>
            </div>
          </div>
        </section>
      )}

      {draft !== null && selectedStudent?.account === null ? (
        <section className="panel enrollment-editor" aria-labelledby="enrollment-editor-title">
          <div className="panel-title" id="enrollment-editor-title">为 {draft.studentNumber} 签发 claim</div>
          <form className="enrollment-form" onSubmit={(event) => void submitIssue(event)}>
            <div className="field">
              <label className="field-label" htmlFor="enrollment-username">用户名</label>
              <input
                aria-describedby="enrollment-username-hint"
                autoComplete="off"
                id="enrollment-username"
                maxLength={32}
                minLength={3}
                pattern="[a-z0-9_]{3,32}"
                required
                value={draft.username}
                onChange={(event) => setDraft({ ...draft, username: event.target.value })}
              />
              <span className="field-hint" id="enrollment-username-hint">3–32 位小写字母、数字或下划线。</span>
            </div>
            <div className="field">
              <label className="field-label" htmlFor="enrollment-display-name">显示名称</label>
              <input
                id="enrollment-display-name"
                maxLength={64}
                required
                value={draft.displayName}
                onChange={(event) => setDraft({ ...draft, displayName: event.target.value })}
              />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="enrollment-expires-in-seconds">有效期 expiresInSeconds</label>
              <input
                aria-describedby="enrollment-expires-hint"
                id="enrollment-expires-in-seconds"
                max={MAX_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS}
                min={MIN_ENROLLMENT_CLAIM_EXPIRES_IN_SECONDS}
                required
                step={1}
                type="number"
                value={draft.expiresInSeconds}
                onChange={(event) => setDraft({ ...draft, expiresInSeconds: event.target.value })}
              />
              <span className="field-hint" id="enrollment-expires-hint">默认 86400 秒；服务端最终校验绝对过期时间。</span>
            </div>
            <div className="enrollment-form-actions">
              <button className="button button-primary" type="submit" disabled={issuing}>
                {issuing ? "签发中" : "签发一次性 claim"}
              </button>
              <button className="button button-ghost" type="button" disabled={issuing} onClick={() => setDraft(null)}>取消</button>
            </div>
          </form>
        </section>
      ) : null}

      <section className="panel">
        <div className="table-wrap">
          <table>
            <thead><tr><th>学号</th><th>Pintia 身份</th><th>来源姓名</th><th>账号</th><th>Rating</th><th>Claim 操作</th></tr></thead>
            <tbody>
              {items.map((student) => (
                <tr key={student.studentNumber}>
                  <td><strong>{student.studentNumber}</strong></td>
                  <td>{student.pintiaUserId}</td>
                  <td>{student.sourceDisplayName ?? "-"}</td>
                  <td>
                    {student.account ? (
                      <><strong>{student.account.displayName}</strong><span className="muted-block">{student.account.username}{student.account.disabledAt ? " · 已禁用" : ""}</span></>
                    ) : "未领取"}
                  </td>
                  <td>{student.rating ?? "-"}</td>
                  <td>
                    <button
                      className="button"
                      type="button"
                      disabled={student.account !== null || issuedClaim !== null || issuing || revoking}
                      onClick={() => beginIssue(student)}
                    >
                      {student.account !== null ? "已绑定" : issuedClaim !== null ? "先处理当前 claim" : "签发 claim"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && items.length === 0 ? <EmptyState>尚未导入学生身份。</EmptyState> : null}
        </div>
      </section>
      {nextCursor ? <button className="button button-ghost load-more" type="button" disabled={loading} onClick={() => void load(nextCursor)}>加载更多</button> : null}
    </div>
  );
}
