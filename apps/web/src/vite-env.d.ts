/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_CHAT_PROMPT_CONFIGURATION_KEY?: string;
  readonly VITE_CHAT_MODEL_CONFIGURATION_KEY?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
