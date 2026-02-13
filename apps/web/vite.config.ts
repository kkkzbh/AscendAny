import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

function normalizeBasePath(value: string | undefined): string {
  const trimmed = value?.trim();
  if (!trimmed) {
    return "/";
  }

  const withLeadingSlash = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  return withLeadingSlash.endsWith("/") ? withLeadingSlash : `${withLeadingSlash}/`;
}

export default defineConfig({
  base: normalizeBasePath(process.env.VITE_BASE_PATH),
  plugins: [react()],
});
