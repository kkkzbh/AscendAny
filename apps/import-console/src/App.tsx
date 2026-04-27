import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { useAuth } from "./hooks/useAuth";
import { LoginPage } from "./pages/LoginPage";

export function App() {
  const { token, account, login, logout } = useAuth();

  if (!token) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage onLogin={login} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  if (account && !account.isAdmin) {
    return (
      <div className="app-forbidden">
        <div className="forbidden-card">
          <h1>⛔ 无访问权限</h1>
          <p>当前账户 <strong>{account.username}</strong> 不是管理员，无法使用管理员控制平台。</p>
          <p>请联系系统管理员授予管理员权限。</p>
          <button className="btn btn-secondary" onClick={logout}>退出登录</button>
        </div>
      </div>
    );
  }

  return (
    <AppShell account={account} onLogout={logout} />
  );
}
