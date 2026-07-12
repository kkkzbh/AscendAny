import { Navigate, Outlet, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { AccountPage } from "./pages/AccountPage";
import { AnalyticsPage } from "./pages/AnalyticsPage";
import { AuthPage } from "./pages/AuthPage";
import { ChatPage } from "./pages/ChatPage";
import { ExamDetailPage } from "./pages/ExamDetailPage";
import { ExamListPage } from "./pages/ExamListPage";
import { LeaderboardPage } from "./pages/LeaderboardPage";
import { NotesPage } from "./pages/NotesPage";
import { OjPage } from "./pages/OjPage";
import { useSession } from "./session/context";

function LoadingScreen() {
  return (
    <main className="splash-screen" role="status">
      <div className="brand-mark" aria-hidden="true">A</div>
      <strong>AscendAny</strong>
      <span>正在恢复安全会话…</span>
    </main>
  );
}

function defaultRoute(role: "student" | "admin"): string {
  return role === "student" ? "/dashboard" : "/account";
}

function AnonymousRoute({ mode }: { mode: "login" | "claim" }) {
  const { status, account } = useSession();
  if (status === "booting") return <LoadingScreen />;
  if (account !== null) return <Navigate to={defaultRoute(account.role)} replace />;
  return <AuthPage mode={mode} />;
}

function AuthenticatedRoute() {
  const { status, account } = useSession();
  if (status === "booting") return <LoadingScreen />;
  if (account === null) return <Navigate to="/login" replace />;
  return <Outlet />;
}

function StudentRoute() {
  const { account } = useSession();
  if (account?.role !== "student") return <Navigate to="/account" replace />;
  return <Outlet />;
}

function HomeRoute() {
  const { account } = useSession();
  if (account === null) return <Navigate to="/login" replace />;
  return <Navigate to={defaultRoute(account.role)} replace />;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<AnonymousRoute mode="login" />} />
      <Route path="/claim" element={<AnonymousRoute mode="claim" />} />

      <Route element={<AuthenticatedRoute />}>
        <Route element={<AppShell />}>
          <Route index element={<HomeRoute />} />
          <Route path="exams" element={<ExamListPage />} />
          <Route path="exams/:examId" element={<ExamDetailPage />} />
          <Route path="oj" element={<OjPage />} />
          <Route element={<StudentRoute />}>
            <Route path="dashboard" element={<AnalyticsPage />} />
            <Route path="leaderboard" element={<LeaderboardPage />} />
            <Route path="chat" element={<ChatPage />} />
            <Route path="notes" element={<NotesPage />} />
          </Route>
          <Route path="account" element={<AccountPage />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
