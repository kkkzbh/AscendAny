import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useSession } from "../session/context";

function credentialLength(password: string): number {
  return new TextEncoder().encode(password).byteLength;
}

export function AuthPage({ mode }: { mode: "login" | "claim" }) {
  const {
    login,
    consumeEnrollment,
    error: sessionError,
    clearError,
  } = useSession();
  const [username, setUsername] = useState("");
  const [claimToken, setClaimToken] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const resetMessages = () => {
    setFormError(null);
    clearError();
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const canonicalUsername = username.trim();
    const canonicalClaimToken = claimToken.trim();
    const passwordBytes = credentialLength(password);

    if (mode === "login" && canonicalUsername.length === 0) {
      setFormError("请输入用户名");
      return;
    }
    if (mode === "claim" && canonicalClaimToken.length === 0) {
      setFormError("请输入一次性激活凭证");
      return;
    }
    if (passwordBytes < 12 || passwordBytes > 128) {
      setFormError("密码必须包含 12 至 128 个 UTF-8 字节");
      return;
    }

    setBusy(true);
    setFormError(null);
    try {
      if (mode === "login") {
        await login(canonicalUsername, password);
      } else {
        await consumeEnrollment(canonicalClaimToken, password);
      }
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "认证失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth-screen">
      <section className="auth-hero" aria-label="AscendAny 产品介绍">
        <div className="auth-brand">
          <span className="brand-mark" aria-hidden="true">A</span>
          <strong>AscendAny</strong>
        </div>
        <div>
          <span className="eyebrow">STUDENT CAPABILITY ANALYTICS</span>
          <h1>让每一次练习<br />汇成清晰的成长轨迹</h1>
          <p>基于已发布的考试快照，查看五维能力、Rating 历史和班级排行。</p>
        </div>
        <div className="auth-feature-list" aria-label="功能">
          <span><b>01</b> 五维能力画像</span>
          <span><b>02</b> Rating 成长轨迹</span>
          <span><b>03</b> 安全设备管理</span>
        </div>
      </section>

      <section className="auth-panel">
        <div className="auth-tabs" role="tablist" aria-label="认证方式">
          <Link
            to="/login"
            role="tab"
            aria-selected={mode === "login"}
            className={mode === "login" ? "active" : ""}
            onClick={resetMessages}
          >
            账号登录
          </Link>
          <Link
            to="/claim"
            role="tab"
            aria-selected={mode === "claim"}
            className={mode === "claim" ? "active" : ""}
            onClick={resetMessages}
          >
            首次激活
          </Link>
        </div>

        <div className="auth-copy">
          <span className="eyebrow">{mode === "login" ? "WELCOME BACK" : "ENROLLMENT CLAIM"}</span>
          <h2>{mode === "login" ? "欢迎回来" : "激活学生账号"}</h2>
          <p>
            {mode === "login"
              ? "使用已激活的 AscendAny 账号登录。"
              : "输入管理员单独发放的一次性凭证，并为账号设置密码。"}
          </p>
        </div>

        <form className="auth-form" onSubmit={submit}>
          {mode === "login" ? (
            <label>
              <span>用户名</span>
              <input
                autoComplete="username"
                autoFocus
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                disabled={busy}
                placeholder="输入用户名"
              />
            </label>
          ) : (
            <label>
              <span>一次性激活凭证</span>
              <input
                autoComplete="off"
                autoFocus
                value={claimToken}
                onChange={(event) => setClaimToken(event.target.value)}
                disabled={busy}
                placeholder="粘贴管理员发放的凭证"
              />
            </label>
          )}

          <label>
            <span>{mode === "login" ? "密码" : "设置密码"}</span>
            <input
              type="password"
              autoComplete={mode === "login" ? "current-password" : "new-password"}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              disabled={busy}
              placeholder="12 至 128 个 UTF-8 字节"
            />
          </label>

          {formError ?? sessionError ? (
            <div className="form-error" role="alert">{formError ?? sessionError}</div>
          ) : null}

          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "处理中…" : mode === "login" ? "登录" : "激活并登录"}
          </button>
        </form>

        <p className="auth-security">
          refresh credential 由 HttpOnly cookie 保存，短期 credential 只驻留当前运行内存。
        </p>
      </section>
    </main>
  );
}
