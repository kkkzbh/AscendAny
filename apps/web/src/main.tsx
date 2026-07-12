import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
import { createWebSession } from "./api/client";
import { SessionProvider } from "./session/SessionContext";
import { ROUTER_BASENAME } from "../publicDelivery.ts";
import "./index.css";

const session = createWebSession();
const root = document.getElementById("root");

if (root === null) {
  throw new Error("The #root application mount is missing.");
}

createRoot(root).render(
  <StrictMode>
    <BrowserRouter
      basename={ROUTER_BASENAME}
      future={{
        v7_relativeSplatPath: true,
        v7_startTransition: true,
      }}
    >
      <SessionProvider session={session}>
        <App />
      </SessionProvider>
    </BrowserRouter>
  </StrictMode>,
);
