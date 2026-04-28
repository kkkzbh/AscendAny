/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_BASE_PATH?: string;
  readonly VITE_TOKEN_HANDOFF?: string;
  /** Set to "true" to use HashRouter (for GitHub Pages / static hosting). */
  readonly VITE_HASH_ROUTER?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
