import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { createDesktopSession } from "./api/client";
import { SessionProvider } from "./session/SessionProvider";
import "./index.css";

const root = document.getElementById("root");
if (root === null) {
  throw new Error("The #root application mount is missing.");
}

const session = createDesktopSession();

createRoot(root).render(
  <StrictMode>
    <SessionProvider session={session}>
      <App />
    </SessionProvider>
  </StrictMode>,
);
