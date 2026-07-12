import { useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { useSession } from "../session/context";

const pageTitles: Record<string, { title: string; subtitle: string }> = {
  "/dashboard": {
    title: "能力画像",
    subtitle: "查看已发布的五维能力与 Rating 轨迹",
  },
  "/leaderboard": {
    title: "学生排行",
    subtitle: "比较同一分析版本下的能力表现",
  },
  "/chat": {
    title: "学习助手",
    subtitle: "在持久化对话中分析考试表现与学习问题",
  },
  "/notes": {
    title: "学习笔记",
    subtitle: "维护 Agent 可读取的版本化个人笔记",
  },
  "/exams": {
    title: "考试题目集",
    subtitle: "查看已导入的 Pintia 考试快照与题目统计",
  },
  "/oj": {
    title: "在线评测",
    subtitle: "运行或提交隔离执行的 C++20 程序",
  },
  "/account": {
    title: "账户设置",
    subtitle: "维护个人资料与登录设备",
  },
};

export function AppShell() {
  const { account, error, logout } = useSession();
  const location = useLocation();
  const [loggingOut, setLoggingOut] = useState(false);

  if (account === null) return null;

  const meta = location.pathname.startsWith("/exams/")
    ? { title: "考试详情", subtitle: "查看当前活动快照与题目统计" }
    : pageTitles[location.pathname] ?? pageTitles["/account"];
  const initial = account.displayName.trim().slice(0, 1).toUpperCase();

  const signOut = async () => {
    setLoggingOut(true);
    await logout();
    setLoggingOut(false);
  };

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">A</span>
          <span>
            <strong>AscendAny</strong>
            <small>学生能力分析平台</small>
          </span>
        </div>

        <nav className="primary-nav" aria-label="主要导航">
          {account.role === "student" ? (
            <>
              <NavItem to="/dashboard" icon="◈" label="能力画像" />
              <NavItem to="/leaderboard" icon="⌁" label="学生排行" />
              <NavItem to="/chat" icon="✦" label="学习助手" />
              <NavItem to="/notes" icon="□" label="学习笔记" />
            </>
          ) : null}
          <NavItem to="/exams" icon="▤" label="考试题目集" />
          <NavItem to="/oj" icon="⌘" label="在线评测" />
          <NavItem to="/account" icon="◎" label="账户设置" />
        </nav>

        <div className="sidebar-account">
          <span className="account-avatar" aria-hidden="true">{initial}</span>
          <span className="account-copy">
            <strong>{account.displayName}</strong>
            <small>{account.role === "student" ? account.studentNumber : "管理员"}</small>
          </span>
        </div>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div>
            <span className="eyebrow">ASCENDANY</span>
            <h1>{meta.title}</h1>
            <p>{meta.subtitle}</p>
          </div>
          <button
            className="ghost-button"
            type="button"
            disabled={loggingOut}
            onClick={() => void signOut()}
          >
            {loggingOut ? "正在退出…" : "退出登录"}
          </button>
        </header>

        {error !== null ? <div className="global-error" role="alert">{error}</div> : null}

        {account.role === "admin" && location.pathname === "/account" ? (
          <div className="admin-note">
            <strong>管理员账户</strong>
            <span>学生画像与排行榜只向 student role 提供。</span>
          </div>
        ) : null}

        <main className="page-content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function NavItem({
  to,
  icon,
  label,
}: {
  to: string;
  icon: string;
  label: string;
}) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) => "nav-link" + (isActive ? " active" : "")}
    >
      <span aria-hidden="true">{icon}</span>
      <strong>{label}</strong>
    </NavLink>
  );
}
