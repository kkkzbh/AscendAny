import { useState } from "react";
import { AccountView } from "./components/AccountView";
import { AnalyticsView } from "./components/AnalyticsView";
import { AuthScreen } from "./components/AuthScreen";
import { ChatView } from "./components/ChatView";
import { ExamCatalogView } from "./components/ExamCatalogView";
import { LeaderboardView } from "./components/LeaderboardView";
import { OjView } from "./components/OjView";
import { useSession } from "./session/SessionContext";

type AppTab = "analytics" | "leaderboard" | "chat" | "oj" | "exams" | "account";

export default function App() {
  const { status, account, error, logout } = useSession();

  if (status === "booting") {
    return <main className="splash-screen" role="status"><div className="brand-mark">A</div><strong>AscendAny</strong><span>正在恢复安全会话…</span></main>;
  }
  if (status === "anonymous" || account === null) return <AuthScreen />;

  return <AuthenticatedShell accountRole={account.role} displayName={account.displayName} studentNumber={account.studentNumber} error={error} onLogout={logout} />;
}

function AuthenticatedShell({
  accountRole,
  displayName,
  studentNumber,
  error,
  onLogout,
}: {
  accountRole: "student" | "admin";
  displayName: string;
  studentNumber: string | null;
  error: string | null;
  onLogout: () => Promise<void>;
}) {
  const [tab, setTab] = useState<AppTab>(accountRole === "student" ? "analytics" : "exams");
  const activeTab = accountRole === "admin" && tab !== "oj" && tab !== "exams" && tab !== "account"
    ? "account"
    : tab;

  return (
    <div className="mobile-shell">
      <header className="app-header">
        <div><span className="eyebrow">ASCENDANY</span><strong>{displayName}</strong><p>{studentNumber ?? "管理员账号"}</p></div>
        <button className="header-action" type="button" onClick={() => void onLogout()} aria-label="退出登录">退出</button>
      </header>

      {error ? <div className="global-error" role="alert">{error}</div> : null}

      <main className="app-content">
        {accountRole === "admin" ? (
          <div className="admin-note"><strong>管理员移动视图</strong><span>当前账号可发布 OJ 不可变题目版本、查看考试并管理个人资料。</span></div>
        ) : null}
        {activeTab === "analytics" ? <AnalyticsView /> : null}
        {activeTab === "leaderboard" ? <LeaderboardView /> : null}
        {activeTab === "chat" ? <ChatView /> : null}
        {activeTab === "oj" ? <OjView /> : null}
        {activeTab === "exams" ? <ExamCatalogView /> : null}
        {activeTab === "account" ? <AccountView /> : null}
      </main>

      <nav className="bottom-nav" aria-label="主要导航">
        {accountRole === "student" ? (
          <>
            <TabButton active={activeTab === "analytics"} icon="◈" label="画像" onClick={() => setTab("analytics")} />
            <TabButton active={activeTab === "leaderboard"} icon="⌁" label="排行" onClick={() => setTab("leaderboard")} />
            <TabButton active={activeTab === "chat"} icon="✦" label="助手" onClick={() => setTab("chat")} />
          </>
        ) : null}
        <TabButton active={activeTab === "oj"} icon="⌘" label="评测" onClick={() => setTab("oj")} />
        <TabButton active={activeTab === "exams"} icon="▤" label="考试" onClick={() => setTab("exams")} />
        <TabButton active={activeTab === "account"} icon="◎" label="账户" onClick={() => setTab("account")} />
      </nav>
    </div>
  );
}

function TabButton({ active, icon, label, onClick }: { active: boolean; icon: string; label: string; onClick: () => void }) {
  return <button className={active ? "active" : ""} type="button" aria-current={active ? "page" : undefined} onClick={onClick}><span>{icon}</span><strong>{label}</strong></button>;
}
