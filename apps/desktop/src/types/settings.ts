export const PROVIDER_ORDER = [
  "server_default",
  "openai",
  "anthropic",
  "deepseek",
] as const;

export type ProviderType = (typeof PROVIDER_ORDER)[number];
export type ThemeMode = "light" | "dark";
export const ZOOM_PERCENT_MIN = 80;
export const ZOOM_PERCENT_MAX = 130;
export const ZOOM_PERCENT_STEP = 5;
export const DEFAULT_ZOOM_PERCENT = 100;

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
  theme: ThemeMode;
  /** True uses solid window background, false enables translucency when OS supports it */
  useOpaqueWindowBackground: boolean;
  /** UI zoom percentage applied to the desktop renderer */
  zoomPercent: number;
  activeProvider: ProviderType;
  providers: Record<ProviderType, ModelProvider>;
  /** Provider key configured by backend as server default target */
  serverDefaultTarget: string;
  /** Human-readable provider label for current server default target */
  serverDefaultTargetLabel: string;
  /** Model name configured on backend for current server default target */
  serverDefaultModel: string;
  /** Currently selected role id */
  activeRole: string;
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
