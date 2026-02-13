export const PROVIDER_ORDER = [
  "server_default",
  "openai",
  "anthropic",
  "deepseek",
] as const;

export type ProviderType = (typeof PROVIDER_ORDER)[number];

export function isProviderType(value: string): value is ProviderType {
  return (PROVIDER_ORDER as readonly string[]).includes(value);
}

export interface ModelProvider {
  type: ProviderType;
  label: string;
  baseUrl: string;
  model: string;
  apiKey: string;
  usesServerConfig: boolean;
  enabled: boolean;
}

export interface AppSettings {
  activeProvider: ProviderType;
  providers: Record<ProviderType, ModelProvider>;
  /** Provider key configured by backend as server default target */
  serverDefaultTarget: string;
  /** Human-readable provider label for current server default target */
  serverDefaultTargetLabel: string;
  /** Model name configured on backend for current server default target */
  serverDefaultModel: string;
  /** Student ID to display metrics for (empty = not configured) */
  studentId: string;
  /** PTA account nickname for data matching */
  ptaNickname: string;
}

export const DEFAULT_PROVIDERS: Record<ProviderType, ModelProvider> = {
  server_default: {
    type: "server_default",
    label: "默认",
    baseUrl: "",
    model: "",
    apiKey: "",
    usesServerConfig: true,
    enabled: true,
  },
  openai: {
    type: "openai",
    label: "OpenAI",
    baseUrl: "https://api.openai.com/v1",
    model: "gpt-4o",
    apiKey: "",
    usesServerConfig: false,
    enabled: true,
  },
  anthropic: {
    type: "anthropic",
    label: "Anthropic",
    baseUrl: "https://api.anthropic.com",
    model: "claude-sonnet-4-20250514",
    apiKey: "",
    usesServerConfig: false,
    enabled: true,
  },
  deepseek: {
    type: "deepseek",
    label: "DeepSeek",
    baseUrl: "https://api.deepseek.com/v1",
    model: "deepseek-chat",
    apiKey: "",
    usesServerConfig: false,
    enabled: true,
  },
};
