import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelConfigPage } from "./ModelConfigPage";
import type {
  AdminModelConfigResponse,
  AdminModelConnectionTestResponse,
  AdminProviderModelsResponse,
} from "../api/admin";

const adminMocks = vi.hoisted(() => ({
  getAdminModelConfig: vi.fn<() => Promise<AdminModelConfigResponse>>(),
  patchAdminModelConfig: vi.fn<(payload: unknown) => Promise<AdminModelConfigResponse>>(),
  testAdminModelConnection: vi.fn<(payload: unknown) => Promise<AdminModelConnectionTestResponse>>(),
  listAdminProviderModels: vi.fn<(payload: unknown) => Promise<AdminProviderModelsResponse>>(),
}));

vi.mock("../api/admin", () => adminMocks);

const BASE_CONFIG: AdminModelConfigResponse = {
  configPath: "/tmp/api.yaml",
  envFilePath: "/tmp/.env.local",
  activeProvider: "deepseek",
  activeRuntime: {
    mode: "openai_compatible",
    baseUrl: "https://api.deepseek.com",
    model: "deepseek-v4-flash",
    apiKeyEnv: "TEST_DEEPSEEK_KEY",
  },
  providers: [
    {
      id: "siliconflow",
      title: "硅基流动",
      provider: "siliconflow",
      strategyId: "siliconflow-kimi-main-chat",
      adapter: "openai_compatible",
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
      adapter: "openai_compatible",
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
      adapter: "openai_compatible",
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
          disabled: false,
          disabledReason: null,
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
      adapter: "openai_compatible",
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
    {
      id: "mimo",
      title: "MIMO",
      provider: "mimo",
      strategyId: "mimo-official-main-chat",
      adapter: "openai_compatible",
      baseUrl: "https://token-plan-cn.xiaomimimo.com/v1",
      model: "mimo-v2.5-pro",
      transportModel: "mimo-v2.5-pro",
      apiKeyEnv: "TEST_MIMO_KEY",
      apiKeyConfigured: false,
      active: false,
      requestMode: "chat_completions",
      modelOptions: [
        {
          modelId: "mimo-v2.5-pro",
          label: "MiMo V2.5 Pro",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
        {
          modelId: "mimo-v2.5",
          label: "MiMo V2.5",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
        {
          modelId: "mimo-v2-pro",
          label: "MiMo V2 Pro",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
        {
          modelId: "mimo-v2-omni",
          label: "MiMo V2 Omni",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
      description: "mimo",
      modelHint: "mimo hint",
    },
  ],
};

describe("ModelConfigPage", () => {
  beforeEach(() => {
    adminMocks.getAdminModelConfig.mockReset();
    adminMocks.patchAdminModelConfig.mockReset();
    adminMocks.testAdminModelConnection.mockReset();
    adminMocks.listAdminProviderModels.mockReset();
  });

  it("renders model providers and saves changed OpenAI config without requiring a visible key", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.patchAdminModelConfig.mockResolvedValue({
      ...BASE_CONFIG,
      activeProvider: "openai",
      activeRuntime: {
        mode: "openai_compatible",
        baseUrl: "https://new.example.com/v1",
        model: "gpt-5.4-medium-thinking",
        apiKeyEnv: "TEST_OPENAI_KEY",
      },
      providers: BASE_CONFIG.providers.map((provider) => (
        provider.id === "openai"
          ? { ...provider, active: true, baseUrl: "https://new.example.com/v1" }
          : { ...provider, active: false }
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
        activeProvider: "openai",
        provider: {
          id: "openai",
          baseUrl: "https://new.example.com/v1",
          model: "openai/gpt-5.4-medium-thinking",
          apiKeyEnv: "TEST_OPENAI_KEY",
          adapter: "openai_compatible",
          requestMode: "chat_completions",
          apiKey: undefined,
        },
      });
    });
    expect(await screen.findByText("模型配置已保存")).toBeInTheDocument();
  });

  it("shows Copilot Responses models and refreshes DeepSeek list", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.listAdminProviderModels.mockResolvedValue({
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

    expect(await screen.findByText(/DeepSeek thinking 内容会在聊天中流式显示/)).toBeInTheDocument();

    fireEvent.click(await screen.findByRole("tab", { name: /GitHub Copilot/ }));
    const select = screen.getByRole("combobox");
    const responsesOption = within(select).getByRole("option", { name: /GPT-5.4 mini/ });
    expect(responsesOption).toBeEnabled();

    fireEvent.click(screen.getByRole("tab", { name: /DeepSeek/ }));
    fireEvent.click(screen.getByRole("button", { name: "刷新 DeepSeek 模型列表" }));

    await waitFor(() => {
      expect(adminMocks.listAdminProviderModels).toHaveBeenCalled();
    });
    expect(await screen.findByText("DeepSeek 模型列表已刷新")).toBeInTheDocument();
  });

  it("renders MIMO models, refreshes the provider list, and saves chat config", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue(BASE_CONFIG);
    adminMocks.listAdminProviderModels.mockResolvedValue({
      source: "dynamic",
      error: null,
      models: [
        {
          modelId: "mimo-v2.5-pro",
          label: "MiMo V2.5 Pro",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
        {
          modelId: "mimo-v2-omni",
          label: "MiMo V2 Omni",
          requestMode: "chat_completions",
          deprecated: false,
          disabled: false,
          disabledReason: null,
        },
      ],
    });
    adminMocks.patchAdminModelConfig.mockResolvedValue({
      ...BASE_CONFIG,
      activeProvider: "mimo",
      activeRuntime: {
        mode: "openai_compatible",
        baseUrl: "https://token-plan-cn.xiaomimimo.com/v1",
        model: "mimo-v2-omni",
        apiKeyEnv: "TEST_MIMO_KEY",
      },
      providers: BASE_CONFIG.providers.map((provider) => (
        provider.id === "mimo"
          ? { ...provider, active: true, model: "mimo-v2-omni", transportModel: "mimo-v2-omni" }
          : { ...provider, active: false }
      )),
    });

    render(<ModelConfigPage />);

    fireEvent.click(await screen.findByRole("tab", { name: /MIMO/ }));
    expect(screen.getByText(/MIMO 使用 OpenAI 兼容/)).toBeInTheDocument();
    const select = screen.getByRole("combobox");
    expect(within(select).getByRole("option", { name: /MiMo V2.5 Pro/ })).toBeEnabled();
    expect(within(select).queryByRole("option", { name: /tts/i })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "刷新 MIMO 模型列表" }));
    await waitFor(() => {
      expect(adminMocks.listAdminProviderModels).toHaveBeenCalledWith({
        providerId: "mimo",
        baseUrl: "https://token-plan-cn.xiaomimimo.com/v1",
        model: "mimo-v2.5-pro",
        apiKeyEnv: "TEST_MIMO_KEY",
        adapter: "openai_compatible",
        requestMode: "chat_completions",
        apiKey: undefined,
      });
    });
    expect(await screen.findByText("MIMO 模型列表已刷新")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "mimo-v2-omni" } });
    fireEvent.click(screen.getByRole("button", { name: "保存配置" }));

    await waitFor(() => {
      expect(adminMocks.patchAdminModelConfig).toHaveBeenCalledWith({
        activeProvider: "mimo",
        provider: {
          id: "mimo",
          baseUrl: "https://token-plan-cn.xiaomimimo.com/v1",
          model: "mimo-v2-omni",
          apiKeyEnv: "TEST_MIMO_KEY",
          adapter: "openai_compatible",
          requestMode: "chat_completions",
          apiKey: undefined,
        },
      });
    });
  });

  it("does not render providers missing from the API response", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue({
      ...BASE_CONFIG,
      providers: BASE_CONFIG.providers.filter((provider) => provider.id !== "mimo"),
    });

    render(<ModelConfigPage />);

    expect(await screen.findByRole("tab", { name: /DeepSeek/ })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /MIMO/ })).not.toBeInTheDocument();
    expect(screen.queryByText("加载管理员全局模型配置。")).not.toBeInTheDocument();
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

  it("shows a clear error when the API still returns the old model config shape", async () => {
    adminMocks.getAdminModelConfig.mockResolvedValue({
      activeTab: "deepseek",
      tabs: [],
    } as unknown as AdminModelConfigResponse);

    render(<ModelConfigPage />);

    expect(await screen.findByText(/模型配置接口返回旧结构或异常结构/)).toBeInTheDocument();
  });
});
