import { useEffect, useMemo, useRef, useState } from "react";
import {
  extractDirectLoginParamsFromUrl,
  isDirectLoginEnabled,
  scrubDirectLoginParams,
} from "@/lib/directLogin";
import { useAuthStore } from "@/stores/authStore";

function trimOrUndefined(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

export function AuthScreen() {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [studentId, setStudentId] = useState("");
  const [ptaNickname, setPtaNickname] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [autoLogin, setAutoLogin] = useState(true);
  const [rememberPassword, setRememberPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const [credentialAvailable, setCredentialAvailable] = useState(false);

  const policy = useAuthStore((s) => s.policy);
  const error = useAuthStore((s) => s.error);
  const lastUsername = useAuthStore((s) => s.lastUsername);
  const savedRememberPassword = useAuthStore((s) => s.rememberPassword);
  const savedAutoLogin = useAuthStore((s) => s.autoLogin);
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const refreshPolicy = useAuthStore((s) => s.refreshPolicy);
  const clearError = useAuthStore((s) => s.clearError);
  const api = window.electronAPI;
  const isMac = api?.platform === "darwin";
  const directLoginEnabled = isDirectLoginEnabled(
    import.meta.env.VITE_DIRECT_LOGIN_ENABLED,
  );
  const directLoginAttemptedRef = useRef(false);

  const isContactPhoneRequired = policy?.requirePhone ?? false;
  const isContactEmailRequired = policy?.requireEmail ?? false;
  const effectiveRememberPassword = credentialAvailable ? rememberPassword : false;
  const requiresAnyContact = useMemo(
    () => policy?.signupPolicy === "require_phone_or_email",
    [policy],
  );

  useEffect(() => {
    void refreshPolicy();

    const api = window.electronAPI;
    if (!api?.credentialAvailable) {
      setCredentialAvailable(false);
      return;
    }

    void api.credentialAvailable().then((available) => {
      setCredentialAvailable(Boolean(available));
      if (!available) {
        setRememberPassword(false);
      }
    });
  }, [refreshPolicy]);

  useEffect(() => {
    setUsername(lastUsername);
    setRememberPassword(savedRememberPassword);
    setAutoLogin(savedAutoLogin);
  }, [lastUsername, savedRememberPassword, savedAutoLogin]);

  useEffect(() => {
    const api = window.electronAPI;
    if (!api?.credentialRead || !credentialAvailable) {
      return;
    }
    if (!savedRememberPassword || !lastUsername.trim()) {
      return;
    }

    void api.credentialRead(lastUsername.trim()).then((value) => {
      if (typeof value === "string" && value.trim()) {
        setPassword(value);
      }
    });
  }, [credentialAvailable, savedRememberPassword, lastUsername]);

  useEffect(() => {
    if (!directLoginEnabled || directLoginAttemptedRef.current) {
      return;
    }

    const params = extractDirectLoginParamsFromUrl(new URL(window.location.href));
    if (!params) {
      return;
    }

    directLoginAttemptedRef.current = true;
    setMode("login");
    setUsername(params.username);
    setPassword(params.password);
    setAutoLogin(params.autoLogin);
    setRememberPassword(params.rememberPassword);
    setSubmitting(true);
    setLocalError(null);
    clearError();

    void login({
      username: params.username,
      password: params.password,
      passwordMode: params.passwordMode,
      autoLogin: params.autoLogin,
      rememberPassword: credentialAvailable ? params.rememberPassword : false,
      deviceId: params.deviceId ?? "desktop-web-direct-login",
    })
      .catch(() => {
        setLocalError("直登失败，请检查账号或密码。");
      })
      .finally(() => {
        const cleanedPath = scrubDirectLoginParams(new URL(window.location.href));
        window.history.replaceState(null, "", cleanedPath);
        setSubmitting(false);
      });
  }, [clearError, credentialAvailable, directLoginEnabled, login]);

  async function saveCredentialIfNeeded(nextUsername: string, nextPassword: string) {
    const api = window.electronAPI;
    if (!credentialAvailable || !api?.credentialSave || !api.credentialDelete) {
      return;
    }

    if (effectiveRememberPassword) {
      await api.credentialSave(nextUsername, nextPassword);
    } else {
      await api.credentialDelete(nextUsername);
    }
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    if (submitting) {
      return;
    }

    const nextUsername = username.trim();
    if (!nextUsername) {
      setLocalError("请输入账号。");
      return;
    }
    if (!password.trim()) {
      setLocalError("请输入密码。");
      return;
    }

    if (mode === "register") {
      const nextStudentId = studentId.trim();
      const nextPtaNickname = ptaNickname.trim();
      if (password !== confirmPassword) {
        setLocalError("两次输入的密码不一致。");
        return;
      }
      if (password.trim().length < 8) {
        setLocalError("密码长度至少为 8 位。");
        return;
      }
      if (!nextStudentId) {
        setLocalError("注册时必须填写学号。");
        return;
      }
      if (!nextPtaNickname) {
        setLocalError("注册时必须填写 PTA 账号昵称。");
        return;
      }
      if (isContactPhoneRequired && !phone.trim()) {
        setLocalError("当前注册策略要求填写手机号。");
        return;
      }
      if (isContactEmailRequired && !email.trim()) {
        setLocalError("当前注册策略要求填写邮箱。");
        return;
      }
      if (requiresAnyContact && !phone.trim() && !email.trim()) {
        setLocalError("当前注册策略要求至少填写手机号或邮箱之一。");
        return;
      }
    }

    setSubmitting(true);
    setLocalError(null);
    clearError();

    try {
      if (mode === "login") {
        await login({
          username: nextUsername,
          password,
          passwordMode: "plain",
          autoLogin,
          rememberPassword: effectiveRememberPassword,
          deviceId: "desktop",
        });
      } else {
        await register({
          username: nextUsername,
          password,
          studentId: studentId.trim(),
          ptaNickname: ptaNickname.trim(),
          phone: trimOrUndefined(phone),
          email: trimOrUndefined(email),
          autoLogin,
          rememberPassword: effectiveRememberPassword,
          deviceId: "desktop",
        });
      }
      await saveCredentialIfNeeded(nextUsername, password);
    } catch {
      // Error message is managed by store error state.
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="auth-root">
      {!isMac && api && (
        <div className="auth-window-controls no-drag" aria-label="窗口控制">
          <button
            type="button"
            onClick={() => api.minimize()}
            className="ui-window-button ui-window-traffic ui-window-minimize"
            title="最小化"
            aria-label="最小化"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
          </button>
          <button
            type="button"
            onClick={() => api.maximize()}
            className="ui-window-button ui-window-traffic ui-window-maximize"
            title="最大化"
            aria-label="最大化"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
          </button>
          <button
            type="button"
            onClick={() => api.close()}
            className="ui-window-button ui-window-traffic ui-window-close"
            title="关闭"
            aria-label="关闭"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
          </button>
        </div>
      )}
      <div className="auth-workbench">
        <section className="auth-intro">
          <div className="auth-intro-header">
            <span className="auth-intro-kicker">AscendAny</span>
            <h1 className="auth-intro-title">学生能力分析平台</h1>
            <p className="auth-intro-subtitle">
              登录后可直接进入你的学习工作台。注册时请一次性完成学号与 PTA 昵称绑定。
            </p>
          </div>

          <div className="auth-highlight-grid">
            <article className="auth-highlight-card">
              <p className="auth-highlight-title">1. 注册即绑定学号与 PTA 昵称</p>
              <p className="auth-highlight-desc">账号创建后资料固定，系统将自动匹配你的考试数据。</p>
            </article>
            <article className="auth-highlight-card">
              <p className="auth-highlight-title">2. 查看能力面板</p>
              <p className="auth-highlight-desc">右侧可查看五大能力指标、Rating 当前值与历史变化。</p>
            </article>
            <article className="auth-highlight-card">
              <p className="auth-highlight-title">3. 发起 Agent 对话</p>
              <p className="auth-highlight-desc">左侧输入问题即可获得基于你学习数据的分析建议。</p>
            </article>
            <article className="auth-highlight-card">
              <p className="auth-highlight-title">4. 调整模型与上下文</p>
              <p className="auth-highlight-desc">在设置中切换模型，必要时可一键清空当前对话上下文。</p>
            </article>
          </div>
        </section>

        <section className="auth-form-pane">
          <div className="auth-form-header">
            <div>
              <h2 className="auth-form-title">
                {mode === "login" ? "账号登录" : "注册账号"}
              </h2>
            </div>
            <button
              type="button"
              className="auth-switch-btn"
              onClick={() => {
                clearError();
                setLocalError(null);
                setMode((prev) => (prev === "login" ? "register" : "login"));
              }}
            >
              {mode === "login" ? "去注册" : "去登录"}
            </button>
          </div>

          <form className="auth-form" onSubmit={handleSubmit}>
            <div className="auth-field">
              <label className="auth-label">账号</label>
              <input
                className="auth-input"
                value={username}
                onChange={(e) => {
                  setUsername(e.target.value);
                  clearError();
                  setLocalError(null);
                }}
                placeholder="4-32 位字母 / 数字 / 下划线"
                autoComplete="username"
              />
            </div>

            <div className="auth-field">
              <label className="auth-label">密码</label>
              <input
                className="auth-input"
                type="password"
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  clearError();
                  setLocalError(null);
                }}
                autoComplete={mode === "login" ? "current-password" : "new-password"}
              />
            </div>

            {mode === "register" && (
              <>
                <div className="auth-field">
                  <label className="auth-label">确认密码</label>
                  <input
                    className="auth-input"
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    autoComplete="new-password"
                  />
                </div>

                <div className="auth-field">
                  <label className="auth-label">学号（必填）</label>
                  <input
                    className="auth-input"
                    value={studentId}
                    onChange={(e) => {
                      setStudentId(e.target.value);
                      clearError();
                      setLocalError(null);
                    }}
                    placeholder="输入学号"
                    autoComplete="off"
                  />
                </div>

                <div className="auth-field">
                  <label className="auth-label">PTA 账号昵称（必填）</label>
                  <input
                    className="auth-input"
                    value={ptaNickname}
                    onChange={(e) => {
                      setPtaNickname(e.target.value);
                      clearError();
                      setLocalError(null);
                    }}
                    placeholder="输入 PTA 昵称"
                    autoComplete="off"
                  />
                </div>

                {(isContactPhoneRequired || requiresAnyContact) && (
                  <div className="auth-field">
                    <label className="auth-label">
                      手机号{isContactPhoneRequired ? "（必填）" : "（可选）"}
                    </label>
                    <input
                      className="auth-input"
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="手机号"
                    />
                  </div>
                )}

                {(isContactEmailRequired || requiresAnyContact) && (
                  <div className="auth-field">
                    <label className="auth-label">
                      邮箱{isContactEmailRequired ? "（必填）" : "（可选）"}
                    </label>
                    <input
                      className="auth-input"
                      type="email"
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="邮箱"
                    />
                  </div>
                )}
              </>
            )}

            <div className="auth-options-grid">
              <label className="auth-option">
                <input
                  type="checkbox"
                  checked={rememberPassword}
                  disabled={!credentialAvailable}
                  onChange={(e) => setRememberPassword(e.target.checked)}
                />
                <span>记住密码</span>
              </label>

              <label className="auth-option">
                <input
                  type="checkbox"
                  checked={autoLogin}
                  onChange={(e) => setAutoLogin(e.target.checked)}
                />
                <span>自动登录</span>
              </label>
            </div>

            {!credentialAvailable && (
              <p className="auth-hint">
                当前环境不支持系统安全凭据存储，已禁用“记住密码”。
              </p>
            )}

            {(localError || error) && <p className="auth-error">{localError ?? error}</p>}

            <button className="auth-submit" type="submit" disabled={submitting}>
              {submitting ? "处理中..." : mode === "login" ? "登录" : "注册并登录"}
            </button>
          </form>
        </section>
      </div>
    </div>
  );
}
