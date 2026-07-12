import { useState } from "react";
import { AccountView } from "./components/AccountView";
import { AnalyticsView } from "./components/AnalyticsView";
import { AuthScreen } from "./components/AuthScreen";
import { ChatView } from "./components/ChatView";
import { ExamCatalogView } from "./components/ExamCatalogView";
import { LeaderboardView } from "./components/LeaderboardView";
import { OjView } from "./components/OjView";
import { UpdaterView } from "./components/UpdaterView";
import { WindowTitleBar } from "./components/WindowTitleBar";
import { useSession } from "./session/context";

type DesktopTab = "analytics" | "leaderboard" | "chat" | "exams" | "oj" | "account" | "updates";

const tabMeta: Record<DesktopTab, { title: string; subtitle: string }> = {
  analytics: {
    title: "能力画像",
    subtitle: "查看已发布的五维能力与 Rating 轨迹",
  },
  leaderboard: {
    title: "学生排行",
    subtitle: "比较同一分析版本下的能力表现",
  },
  chat: {
    title: "学习助手",
    subtitle: "在持久化对话中分析考试表现与学习问题",
  },
  exams: {
    title: "考试分析",
    subtitle: "查看活动考试快照与实时分析生成进度",
  },
  oj: {
    title: "在线评测",
    subtitle: "浏览不可变题目版本并运行或提交 C++20 程序",
  },
  account: {
    title: "账户设置",
    subtitle: "维护个人资料与登录设备",
  },
  updates: {
    title: "客户端更新",
    subtitle: "检查并安装 AscendAny Desktop 新版本",
  },
};

export default function App() {
  const { status, account } = useSession();

  return (
    <div className="desktop-root">
      <WindowTitleBar />
      {status === "booting" ? <BootScreen /> : null}
      {status === "anonymous" ? <AuthScreen /> : null}
      {status === "authenticated" && account !== null ? (
        <AuthenticatedDesktop />
      ) : null}
    </div>
  );
}

function BootScreen() {
  return (
    <main className="boot-screen" role="status">
      <span className="brand-mark" aria-hidden="true">A</span>
      <strong>AscendAny</strong>
      <p>正在恢复安全会话…</p>
    </main>
  );
}

function AuthenticatedDesktop() {
  const { account, error, logout, session } = useSession();
  const [tab, setTab] = useState<DesktopTab>(
    account?.role === "student" ? "analytics" : "account",
  );
  const [loggingOut, setLoggingOut] = useState(false);

  if (account === null) return null;

  const activeTab =
    account.role === "admin" && (tab === "analytics" || tab === "leaderboard" || tab === "chat")
      ? "account"
      : tab;
  const meta = tabMeta[activeTab];

  const signOut = async () => {
    setLoggingOut(true);
    await logout();
    setLoggingOut(false);
  };

  return (
    <div className="desktop-shell">
      <aside className="desktop-sidebar">
        <div className="sidebar-brand">
          <span className="brand-mark" aria-hidden="true">A</span>
          <span>
            <strong>AscendAny</strong>
            <small>Desktop</small>
          </span>
        </div>

        <nav className="desktop-nav" aria-label="主要导航">
          {account.role === "student" ? (
            <>
              <TabButton
                active={activeTab === "analytics"}
                icon="◈"
                label="能力画像"
                onClick={() => setTab("analytics")}
              />
              <TabButton
                active={activeTab === "leaderboard"}
                icon="⌁"
                label="学生排行"
                onClick={() => setTab("leaderboard")}
              />
              <TabButton
                active={activeTab === "chat"}
                icon="✦"
                label="学习助手"
                onClick={() => setTab("chat")}
              />
            </>
          ) : null}
          <TabButton
            active={activeTab === "exams"}
            icon="▤"
            label="考试分析"
            onClick={() => setTab("exams")}
          />
          <TabButton
            active={activeTab === "oj"}
            icon="⌘"
            label="在线评测"
            onClick={() => setTab("oj")}
          />
          <TabButton
            active={activeTab === "account"}
            icon="◎"
            label="账户设置"
            onClick={() => setTab("account")}
          />
          <TabButton
            active={activeTab === "updates"}
            icon="↥"
            label="客户端更新"
            onClick={() => setTab("updates")}
          />
        </nav>

        <div className="sidebar-identity">
          <span className="identity-avatar" aria-hidden="true">
            {account.displayName.slice(0, 1).toUpperCase()}
          </span>
          <span>
            <strong>{account.displayName}</strong>
            <small>
              {account.role === "student" ? account.studentNumber : "管理员"}
            </small>
          </span>
        </div>
      </aside>

      <section className="desktop-workspace">
        <header className="workspace-header">
          <div>
            <span className="eyebrow">ASCENDANY V2</span>
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

        {account.role === "admin" && activeTab === "account" ? (
          <div className="admin-note">
            <strong>管理员账户</strong>
            <span>学生画像与排行榜只向 student role 提供。</span>
          </div>
        ) : null}

        <main className="workspace-content">
          {activeTab === "analytics" ? <AnalyticsView /> : null}
          {activeTab === "leaderboard" ? <LeaderboardView /> : null}
          {activeTab === "chat" ? <ChatView /> : null}
          {activeTab === "exams" ? <ExamCatalogView session={session} /> : null}
          {activeTab === "oj" ? <OjView /> : null}
          {activeTab === "account" ? <AccountView /> : null}
          {activeTab === "updates" ? <UpdaterView /> : null}
        </main>
      </section>
    </div>
  );
}

function TabButton({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: string;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      className={"nav-button" + (active ? " active" : "")}
      type="button"
      aria-current={active ? "page" : undefined}
      onClick={onClick}
    >
      <span aria-hidden="true">{icon}</span>
      <strong>{label}</strong>
    </button>
  );
}
