import type { AdminModelProviderId } from "./api/admin";

export const MODEL_PROVIDER_IDS: AdminModelProviderId[] = [
  "siliconflow",
  "openai",
  "copilot",
  "deepseek",
  "mimo",
];

export const MODEL_PROVIDER_LABELS: Record<AdminModelProviderId, string> = {
  siliconflow: "硅基流动",
  openai: "OpenAI",
  copilot: "GitHub Copilot",
  deepseek: "DeepSeek",
  mimo: "MIMO",
};

export const RESPONSES_UNSUPPORTED_TEXT = "当前 AscendAny 运行时暂不支持 Responses API";
