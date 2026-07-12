import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { PUBLIC_BASE_PATH } from "./publicDelivery.ts";

export default defineConfig({
  base: PUBLIC_BASE_PATH,
  plugins: [react()],
  server: {
    port: 6748,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:18000",
        changeOrigin: true,
      },
    },
  },
});
