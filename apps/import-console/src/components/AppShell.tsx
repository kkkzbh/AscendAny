import { useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import type { Account } from "@ascendany/sdk";
import { AccountsPage } from "../pages/AccountsPage";
import { AuditPage } from "../pages/AuditPage";
import { ImportPage } from "../pages/ImportPage";
import { ConfigurationPage } from "../pages/ConfigurationPage";
import { RecommendationKnowledgeCatalogPage } from "../pages/RecommendationKnowledgeCatalogPage";
import { StudentsPage } from "../pages/StudentsPage";
import { HelpDrawer } from "./HelpDrawer";

interface Props {
  account: Account;
  authError: string | null;
  onLogout: () => Promise<void>;
}

export function AppShell({ account, authError, onLogout }: Props) {
  const [helpOpen, setHelpOpen] = useState(false);

  return (
    <div className="admin-shell">
      <aside className="admin-sidebar" aria-label="管理员导航">
        <div className="admin-product">AscendAny</div>
        <nav className="admin-nav">
          <NavLink
            to="/import"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            Pintia 数据导入
          </NavLink>
          <NavLink
            to="/configuration"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            运行配置
          </NavLink>
          <NavLink
            to="/recommendation-catalog"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            推荐知识目录
          </NavLink>
          <NavLink
            to="/accounts"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            账户管理
          </NavLink>
          <NavLink
            to="/students"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            学生身份
          </NavLink>
          <NavLink
            to="/audit"
            className={({ isActive }) => `admin-nav-item${isActive ? " is-active" : ""}`}
          >
            审计日志
          </NavLink>
        </nav>
      </aside>
      <main className="admin-main">
        <header className="admin-topbar">
          <button className="button button-ghost" type="button" onClick={() => setHelpOpen(true)}>
            操作指南
          </button>
          <div className="admin-topbar-spacer" />
          <div className="admin-account">
            <span>{account.displayName}</span>
            <button className="button button-ghost" type="button" onClick={() => void onLogout()}>
              退出
            </button>
          </div>
        </header>
        {authError ? <div className="session-error" role="alert">{authError}</div> : null}
        <div className="admin-workspace">
          <Routes>
            <Route path="/" element={<Navigate to="/import" replace />} />
            <Route path="/import" element={<ImportPage />} />
            <Route path="/configuration" element={<ConfigurationPage />} />
            <Route path="/recommendation-catalog" element={<RecommendationKnowledgeCatalogPage />} />
            <Route path="/accounts" element={<AccountsPage />} />
            <Route path="/students" element={<StudentsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="*" element={<Navigate to="/import" replace />} />
          </Routes>
        </div>
      </main>
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}
