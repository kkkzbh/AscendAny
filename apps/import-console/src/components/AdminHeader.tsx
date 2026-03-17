import { NavLink } from "react-router-dom";
import type { AccountInfo } from "../hooks/useAuth";

interface Props {
  account: AccountInfo | null;
  title: string;
  onLogout: () => void;
  onOpenHelp?: () => void;
}

const navItems = [
  { to: "/", label: "导入任务", end: true },
  { to: "/exam-analysis", label: "考试分析" },
];

export function AdminHeader({ account, title, onLogout, onOpenHelp }: Props) {
  return (
    <header className="topbar">
      <div className="topbar-left">
        <span className="topbar-logo">🔧</span>
        <div className="topbar-title-block">
          <h1>{title}</h1>
          <nav className="topbar-nav" aria-label="控制台导航">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                className={({ isActive }) => `topbar-nav-link${isActive ? " is-active" : ""}`}
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
        </div>
      </div>
      <div className="topbar-right">
        {onOpenHelp ? (
          <button className="btn btn-ghost" onClick={onOpenHelp} title="帮助">
            ❓ 帮助
          </button>
        ) : null}
        <span className="topbar-user">👤 {account?.username ?? "admin"}</span>
        <button className="btn btn-ghost" onClick={onLogout}>退出</button>
      </div>
    </header>
  );
}
