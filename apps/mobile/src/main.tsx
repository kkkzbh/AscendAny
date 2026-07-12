import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { createMobileSession } from "./api/client";
import { SessionProvider } from "./session/SessionContext";
import "./styles.css";

const rootElement = document.getElementById("root");
if (rootElement === null) throw new Error("Root element is required.");

const root = createRoot(rootElement);

try {
  const session = createMobileSession();
  root.render(
    <StrictMode>
      <SessionProvider session={session}>
        <App />
      </SessionProvider>
    </StrictMode>,
  );
} catch (error) {
  const message = error instanceof Error ? error.message : "Mobile configuration is invalid.";
  root.render(
    <main className="fatal-screen" role="alert">
      <section className="fatal-card">
        <span className="eyebrow">AscendAny Mobile</span>
        <h1>启动配置错误</h1>
        <p>{message}</p>
      </section>
    </main>,
  );
}
