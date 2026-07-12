import { useState, type FormEvent } from "react";
import { useSession } from "../session/SessionContext";

type AuthMode = "login" | "enrollment";

export function AuthScreen() {
  const { login, consumeEnrollment, error: sessionError, clearError } = useSession();
  const [mode, setMode] = useState<AuthMode>("login");
  const [username, setUsername] = useState("");
  const [credential, setCredential] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const selectMode = (nextMode: AuthMode) => {
    setMode(nextMode);
    setFormError(null);
    clearError();
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const canonicalUsername = username.trim();
    const canonicalCredential = credential.trim();
    if (password.length === 0 || (mode === "login" ? canonicalUsername.length === 0 : canonicalCredential.length === 0)) {
      setFormError("请填写完整的认证信息");
      return;
    }

    setBusy(true);
    setFormError(null);
    try {
      if (mode === "login") {
        await login(canonicalUsername, password);
      } else {
        await consumeEnrollment(canonicalCredential, password);
      }
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "认证失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth-screen">
      <section className="auth-hero" aria-label="AscendAny 介绍">
        <span className="eyebrow">AscendAny Mobile</span>
        <h1>看见每一次练习<br />沉淀出的能力</h1>
        <p>使用同一份已发布分析数据查看五维画像、Rating 轨迹和班级排行。</p>
        <div className="auth-feature-list" aria-label="功能">
          <span>五维能力画像</span>
          <span>Rating 历史</span>
          <span>安全会话管理</span>
        </div>
      </section>

      <section className="auth-card">
        <div className="auth-tabs" role="tablist" aria-label="认证方式">
          <button type="button" role="tab" aria-selected={mode === "login"} className={mode === "login" ? "active" : ""} onClick={() => selectMode("login")}>账号登录</button>
          <button type="button" role="tab" aria-selected={mode === "enrollment"} className={mode === "enrollment" ? "active" : ""} onClick={() => selectMode("enrollment")}>首次激活</button>
        </div>

        <div className="auth-copy">
          <h2>{mode === "login" ? "欢迎回来" : "激活学生账号"}</h2>
          <p>{mode === "login" ? "使用已激活的本地账号继续。" : "输入管理员发放的一次性激活凭证并设置密码。"}</p>
        </div>

        <form className="auth-form" onSubmit={submit}>
          {mode === "login" ? (
            <label>
              <span>用户名</span>
              <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} disabled={busy} />
            </label>
          ) : (
            <label>
              <span>一次性激活凭证</span>
              <input autoComplete="off" value={credential} onChange={(event) => setCredential(event.target.value)} disabled={busy} />
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
            />
          </label>

          {(formError ?? sessionError) ? <div className="form-error" role="alert">{formError ?? sessionError}</div> : null}
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "处理中…" : mode === "login" ? "登录" : "激活并登录"}
          </button>
        </form>

        <p className="auth-security">会话使用 HttpOnly refresh cookie；access token 仅驻留当前运行内存。</p>
      </section>
    </main>
  );
}
