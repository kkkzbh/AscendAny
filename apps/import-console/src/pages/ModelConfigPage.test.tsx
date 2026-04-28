import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelConfigPage } from "./ModelConfigPage";
import type {
  AdminDeepSeekModelsResponse,
  AdminModelConfigResponse,
  AdminModelConnectionTestResponse,
} from "../api/admin";

const adminMocks = vi.hoisted(() => ({
  getAdminModelConfig: vi.fn<() => Promise<AdminModelConfigResponse>>(),
  patchAdminModelConfig: vi.fn<(payload: unknown) => Promise<AdminModelConfigResponse>>(),
  testAdminModelConnection: vi.fn<(payload: unknown) => Promise<AdminModelConnectionTestResponse>>(),
  listAdminDeepSeekModels: vi.fn<(payload: unknown) => Promise<AdminDeepSeekModelsResponse>>(),
}));

vi.mock("../api/admin", () => adminMocks);

const BASE_CONFIG: AdminModelConfigResponse = {
  configPath: "/tmp/api.yaml",
  envFilePath: "/tmp/.env.local",
  activeTab: "deepseek",
  serverDefault: {
    mode: "openai_compatible",
    baseUrl: "https://api.deepseek.com",
    model: "deepseek-v4-flash",
    apiKeyEnv: "TEST_DEEPSEEK_KEY",
  },
  tabs: [
    {
      id: "siliconflow",
      title: "硅基流动",
      provider: "siliconflow",
      strategyId: "siliconflow-kimi-main-chat",
      baseUrl: "https://api.siliconflow.cn/v1",
      model: "Pro/moonshotai/Kimi-K2.5",
      transportModel: "Pro/moonshotai/Kimi-K2.5",
      apiKeyEnv: "TEST_SILICONFLOW_KEY",
      apiKeyConfigured: false,
      active: false,
      requestMode: "chat_completions",
      modelOptions: [
        {
          modelId: "Pro/moonshotai/Kimi-K2.5",
          label: "Pro/moonshotai/Kimi-K2.5",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
      description: "siliconflow",
      modelHint: "kimi",
    },
    {
      id: "openai",
      title: "OpenAI",
      provider: "openai",
      strategyId: "openai-gpt54-main-chat",
      baseUrl: "https://shell.wyzai.top/v1",
      model: "openai/gpt-5.4-medium-thinking",
      transportModel: "gpt-5.4-medium-thinking",
      apiKeyEnv: "TEST_OPENAI_KEY",
      apiKeyConfigured: true,
      active: false,
      requestMode: "chat_completions",
      modelOptions: [
        {
          modelId: "openai/gpt-5.4-medium-thinking",
          label: "GPT-5.4 (medium thinking)",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
      description: "openai",
      modelHint: "openai hint",
    },
    {
      id: "copilot",
      title: "GitHub Copilot",
      provider: "openai",
      strategyId: "copilot-github-oauth-main-chat",
      baseUrl: "http://127.0.0.1:5140/api/internal/copilot/v1",
      model: "openai/gpt-5-mini",
      transportModel: "gpt-5-mini",
      apiKeyEnv: "TEST_COPILOT_KEY",
      apiKeyConfigured: false,
      active: false,
      requestMode: "chat_completions",
      modelOptions: [
        {
          modelId: "openai/gpt-5.4-mini",
          label: "GPT-5.4 mini (0.33x)",
          requestMode: "responses",
          deprecated: false,
          disabled: true,
          disabledReason: "AscendAny 第一版暂不支持 Responses API",
        },
        {
          modelId: "openai/gpt-5-mini",
          label: "GPT-5 mini (0x)",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
      description: "copilot",
      modelHint: "copilot hint",
    },
    {
      id: "deepseek",
      title: "DeepSeek",
      provider: "deepseek",
      strategyId: "deepseek-official-main-chat",
      baseUrl: "https://api.deepseek.com",
      model: "deepseek-v4-flash",
      transportModel: "deepseek-v4-flash",
      apiKeyEnv: "TEST_DEEPSEEK_KEY",
      apiKeyConfigured: false,
      active: true,
      requestMode: "chat_completions",
      modelOptions: [
        {
          modelId: "deepseek-v4-flash",
          label: "deepseek-v4-flash",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
      description: "deepseek",
      modelHint: "deepseek hint",
    },
  ],
};

describe("ModelConfigPage", () => {
  beforeEach(() => {
    adminMocks.getAdminModelConfig.mockReset();
    adminMocks.patchAdminModelConfig.mockReset();
    adminMocks.testAdminModelConnection.mockReset();
    adminMocks.listAdminDeepSeekModels.mockReset();
  });

  it("renders model tabs and saves changed OpenAI config without requiring a visible key", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.patchAdminModelConfig.mockResolvedValue({
      ...BASE_CONFIG,
      activeTab: "openai",
      serverDefault: {
        mode: "openai_compatible",
        baseUrl: "https://new.example.com/v1",
        model: "gpt-5.4-medium-thinking",
        apiKeyEnv: "TEST_OPENAI_KEY",
      },
      tabs: BASE_CONFIG.tabs.map((tab) => (
        tab.id === "openai"
          ? { ...tab, active: true, baseUrl: "https://new.example.com/v1" }
          : { ...tab, active: false }
      )),
    });

    render(<ModelConfigPage />);

    fireEvent.click(await screen.findByRole("tab", { name: /OpenAI/ }));
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://new.example.com/v1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存配置" }));

    await waitFor(() => {
      expect(adminMocks.patchAdminModelConfig).toHaveBeenCalledWith({
        activeTab: "openai",
        tab: {
          id: "openai",
          baseUrl: "https://new.example.com/v1",
          model: "openai/gpt-5.4-medium-thinking",
          apiKeyEnv: "TEST_OPENAI_KEY",
          apiKey: undefined,
        },
      });
    });
    expect(await screen.findByText("模型配置已保存")).toBeInTheDocument();
  });

  it("shows disabled Copilot Responses-only models and refreshes DeepSeek list", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.listAdminDeepSeekModels.mockResolvedValue({
      source: "dynamic",
      error: null,
      models: [
        {
          modelId: "deepseek-v4-pro",
          label: "deepseek-v4-pro",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
    });

    render(<ModelConfigPage />);

    fireEvent.click(await screen.findByRole("tab", { name: /GitHub Copilot/ }));
    const select = screen.getByRole("combobox");
    const disabled = within(select).getByRole("option", { name: /GPT-5.4 mini/ });
    expect(disabled).toBeDisabled();

    fireEvent.click(screen.getByRole("tab", { name: /DeepSeek/ }));
    fireEvent.click(screen.getByRole("button", { name: "刷新 DeepSeek 模型列表" }));

    await waitFor(() => {
      expect(adminMocks.listAdminDeepSeekModels).toHaveBeenCalled();
    });
    expect(await screen.findByText("DeepSeek 模型列表已刷新")).toBeInTheDocument();
  });

  it("renders connection test failures", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.testAdminModelConnection.mockResolvedValue({
      ok: false,
      status: "missing_key",
      message: "未配置 API Key：TEST_DEEPSEEK_KEY",
      provider: "DeepSeek",
      model: "deepseek-v4-flash",
      elapsedMs: 0,
    });

    render(<ModelConfigPage />);

    fireEvent.click(await screen.findByRole("button", { name: "连接测试" }));

    expect(await screen.findByText(/失败：未配置 API Key/)).toBeInTheDocument();
  });
});
