import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import type { AccountInfo } from "../hooks/useAuth";
import { ImportPage } from "../pages/ImportPage";
import { ModelConfigPage } from "../pages/ModelConfigPage";
import { PreprocessConfigPage } from "../pages/PreprocessConfigPage";
import { StudentReportsPage } from "../pages/StudentReportsPage";
import { UsersPage } from "../pages/UsersPage";
import { AuditLogPage } from "../pages/AuditLogPage";

interface Props {
  account: AccountInfo | null;
  onLogout: () => void;
}

const navItems = [
  { to: "/import", label: "数据导入" },
  { to: "/models", label: "模型配置" },
  { to: "/preprocess", label: "预处理参数" },
  { to: "/students", label: "学生报告" },
  { to: "/users", label: "用户与权限" },
  { to: "/audit", label: "审计日志" },
];

export function AppShell({ account, onLogout }: Props) {
  return (
    <div className="admin-shell">
      <aside className="admin-sidebar" aria-label="管理员导航">
        <nav className="admin-nav">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="admin-main">
        <header className="admin-topbar">
          <div className="admin-topbar-spacer" />
          <div className="admin-account">
            <span>{account?.username ?? "admin"}</span>
            <button className="button button-ghost" type="button" onClick={onLogout}>
              退出
            </button>
          </div>
        </header>
        <div className="admin-workspace">
          <Routes>
            <Route path="/" element={<Navigate to="/import" replace />} />
            <Route path="/import" element={<ImportPage />} />
            <Route path="/models" element={<ModelConfigPage />} />
            <Route path="/preprocess" element={<PreprocessConfigPage />} />
            <Route path="/students" element={<StudentReportsPage />} />
            <Route path="/users" element={<UsersPage account={account} />} />
            <Route path="/audit" element={<AuditLogPage />} />
            <Route path="*" element={<Navigate to="/import" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  );
}
