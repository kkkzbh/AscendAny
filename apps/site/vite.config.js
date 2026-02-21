import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
function normalizeBasePath(value) {
    var trimmed = value === null || value === void 0 ? void 0 : value.trim();
    if (!trimmed) {
        return "/";
    }
    var withLeadingSlash = trimmed.startsWith("/") ? trimmed : "/".concat(trimmed);
    return withLeadingSlash.endsWith("/") ? withLeadingSlash : "".concat(withLeadingSlash, "/");
}
export default defineConfig({
    base: normalizeBasePath(process.env.VITE_BASE_PATH),
    plugins: [react()],
});
