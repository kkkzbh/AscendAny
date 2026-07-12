import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { useAuth } from "./hooks/useAuth";
import { LoginPage } from "./pages/LoginPage";

export function App() {
  const { status, account, error, login, logout } = useAuth();

  if (status === "initializing") {
    return (
      <div className="login-page" role="status" aria-live="polite">
        <div className="login-card auth-loading-card">正在恢复管理员会话…</div>
      </div>
    );
  }

  if (status === "anonymous" || account === null) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage onLogin={login} sessionError={error} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  if (account.role !== "admin") {
    return (
      <div className="app-forbidden">
        <div className="forbidden-card">
          <h1>⛔ 无访问权限</h1>
          <p>当前账户 <strong>{account.username}</strong> 没有数据导入权限。</p>
          <p>请联系系统管理员授予管理员权限。</p>
          {error ? <div className="login-error" role="alert">{error}</div> : null}
          <button className="btn btn-secondary" onClick={() => void logout()}>退出登录</button>
        </div>
      </div>
    );
  }

  return (
    <AppShell account={account} authError={error} onLogout={logout} />
  );
}
