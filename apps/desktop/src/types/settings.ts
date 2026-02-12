export type ProviderType = "openai" | "anthropic" | "deepseek";

export interface ModelProvider {
  type: ProviderType;
  label: string;
  baseUrl: string;
  model: string;
  apiKey: string;
}

export interface AppSettings {
  activeProvider: ProviderType;
  providers: Record<ProviderType, ModelProvider>;
  /** Student ID to display metrics for (empty = not configured) */
  studentId: string;
  /** API base URL for the FastAPI backend */
  apiBaseUrl: string;
}

export const DEFAULT_PROVIDERS: Record<ProviderType, ModelProvider> = {
  openai: {
    type: "openai",
    label: "OpenAI",
    baseUrl: "https://api.openai.com/v1",
    model: "gpt-4o",
    apiKey: "",
  },
  anthropic: {
    type: "anthropic",
    label: "Anthropic",
    baseUrl: "https://api.anthropic.com",
    model: "claude-sonnet-4-20250514",
    apiKey: "",
  },
  deepseek: {
    type: "deepseek",
    label: "DeepSeek",
    baseUrl: "https://api.deepseek.com/v1",
    model: "deepseek-chat",
    apiKey: "",
  },
};
