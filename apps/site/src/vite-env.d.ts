/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_RELEASE_OWNER?: string;
  readonly VITE_RELEASE_REPO?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
