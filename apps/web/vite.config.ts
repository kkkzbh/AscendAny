import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { PUBLIC_BASE_PATH } from "./publicDelivery.ts";

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? "http://127.0.0.1:18000";

export default defineConfig({
  base: PUBLIC_BASE_PATH,
  plugins: [react()],
  publicDir: false,
  server: {
    port: 5175,
    proxy: {
      "/api": {
        target: apiProxyTarget,
        changeOrigin: true,
      },
    },
  },
});
