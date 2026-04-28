import type { AdminModelTabId } from "./api/admin";

export const MODEL_TAB_IDS: AdminModelTabId[] = [
  "siliconflow",
  "openai",
  "copilot",
  "deepseek",
];

export const MODEL_TAB_LABELS: Record<AdminModelTabId, string> = {
  siliconflow: "硅基流动",
  openai: "OpenAI",
  copilot: "GitHub Copilot",
  deepseek: "DeepSeek",
};

export const RESPONSES_UNSUPPORTED_TEXT = "当前 AscendAny 运行时暂不支持 Responses API";
