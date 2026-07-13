import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConfigurationItem, ConfigurationVersionPage } from "@ascendany/sdk";
import { ConfigurationPage } from "./ConfigurationPage";

const activeItem: ConfigurationItem = {
  id: "11111111-1111-4111-8111-111111111111",
  key: "prompt.default",
  kind: "prompt",
  headRevision: 1,
  activeVersion: {
    id: "1",
    number: 1,
    schemaId: "ascendany.prompt.v1",
    document: { system: "Be exact." },
    documentSha256: "a".repeat(64),
    credentialRef: null,
    createdByAccountId: "22222222-2222-4222-8222-222222222222",
    createdBySessionId: "33333333-3333-4333-8333-333333333333",
    createdAt: "2026-07-11T08:00:00Z",
  },
  createdAt: "2026-07-11T08:00:00Z",
  updatedAt: "2026-07-11T08:00:00Z",
};

const nextItem: ConfigurationItem = {
  ...activeItem,
  headRevision: 2,
  activeVersion: {
    ...activeItem.activeVersion!,
    number: 2,
    document: { system: "Be concise." },
    documentSha256: "b".repeat(64),
    createdAt: "2026-07-11T09:00:00Z",
  },
  updatedAt: "2026-07-11T09:00:00Z",
};

const modelItem: ConfigurationItem = {
  ...activeItem,
  key: "chat.primary",
  kind: "model_connection",
  headRevision: 3,
  activeVersion: {
    ...activeItem.activeVersion!,
    number: 3,
    schemaId: "ascendany.model_connection.openai_compatible.v1",
    document: {
      endpoint: "https://api.example.com/v1/chat/completions",
      model: "provider-model-v3",
      timeoutMilliseconds: 5000,
      maxCompletionTokens: 1024,
    },
    documentSha256: "c".repeat(64),
    credentialRef: "models.chat.primary",
  },
};

const api = vi.hoisted(() => ({
  getConfigurations: vi.fn(),
  getConfigurationItem: vi.fn(),
  getConfigurationVersions: vi.fn(),
  putConfigurationVersion: vi.fn(),
  probeModelConnection: vi.fn(),
}));

vi.mock("../api/configuration", () => api);

function history(item: ConfigurationItem): ConfigurationVersionPage {
  return {
    key: item.key,
    kind: item.kind,
    headRevision: item.headRevision,
    items: item.activeVersion ? [item.activeVersion] : [],
    nextBeforeNumber: null,
  };
}

describe("ConfigurationPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getConfigurations.mockResolvedValue({ items: [activeItem], nextCursor: null });
    api.getConfigurationItem.mockResolvedValue(activeItem);
    api.getConfigurationVersions.mockImplementation(async () => history(
      api.putConfigurationVersion.mock.calls.length > 0 ? nextItem : activeItem,
    ));
    api.putConfigurationVersion.mockResolvedValue({ item: nextItem, idempotent: false });
    api.probeModelConnection.mockResolvedValue({
      configurationKey: modelItem.key,
      configurationHeadRevision: 3,
      configurationVersion: 3,
      configurationSha256: "c".repeat(64),
      authority: "api.example.com",
      model: "provider-model-v3",
      checkedAt: "2026-07-11T06:07:08Z",
      latencyMilliseconds: 42,
    });
  });

  it("loads one immutable head and publishes its next CAS revision", async () => {
    const { container } = render(<ConfigurationPage />);

    const itemButton = await screen.findByRole("button", { name: /prompt\.default/ });
    fireEvent.click(itemButton);
    await waitFor(() => expect(api.getConfigurationItem).toHaveBeenCalledWith("prompt.default"));
    expect(screen.getByText("CAS r1")).toBeInTheDocument();
    expect(screen.getByText("aaaaaaaaaaaaaa…")).toBeInTheDocument();

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea.configuration-document");
    expect(textarea).not.toBeNull();
    fireEvent.change(textarea as HTMLTextAreaElement, { target: { value: `{"system":"Be concise."}` } });
    fireEvent.click(screen.getByRole("button", { name: "发布 immutable version" }));

    await waitFor(() => {
      expect(api.putConfigurationVersion).toHaveBeenCalledWith({
        key: "prompt.default",
        kind: "prompt",
        expectedHeadRevision: 1,
        schemaId: "ascendany.prompt.v1",
        document: { system: "Be concise." },
        credentialRef: null,
      });
      expect(screen.getByText("已发布 revision 2。")).toBeInTheDocument();
    });
    expect(screen.getByText("CAS r2")).toBeInTheDocument();
  });

  it("rejects a non-object document before calling the API", async () => {
    const { container } = render(<ConfigurationPage />);
    fireEvent.click(await screen.findByRole("button", { name: "新建配置" }));
    const inputs = container.querySelectorAll<HTMLInputElement>(".configuration-form-grid input");
    fireEvent.change(inputs[0]!, { target: { value: "prompt.new" } });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea.configuration-document");
    fireEvent.change(textarea as HTMLTextAreaElement, { target: { value: "[]" } });
    fireEvent.click(screen.getByRole("button", { name: "发布 immutable version" }));

    expect(await screen.findByText("Document 顶层必须是 JSON object。")).toBeInTheDocument();
    expect(api.putConfigurationVersion).not.toHaveBeenCalled();
  });

  it("keeps knowledge catalog ownership out of the generic editor", async () => {
    api.getConfigurations.mockResolvedValue({
      items: [{ ...activeItem, key: "recommendation.catalog.active", kind: "knowledge_catalog" }],
      nextCursor: null,
    });
    render(<ConfigurationPage />);

    expect(await screen.findByText("当前筛选下没有配置。")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "新建配置" }));
    const kindSelect = screen.getByRole("combobox", { name: "Kind" });
    expect(within(kindSelect).queryByRole("option", { name: "知识目录" })).not.toBeInTheDocument();
  });

  it("rejects the reserved knowledge catalog key in the generic editor", async () => {
    const { container } = render(<ConfigurationPage />);
    fireEvent.click(await screen.findByRole("button", { name: "新建配置" }));
    const inputs = container.querySelectorAll<HTMLInputElement>(".configuration-form-grid input");
    fireEvent.change(inputs[0]!, { target: { value: "recommendation.catalog.active" } });
    fireEvent.click(screen.getByRole("button", { name: "发布 immutable version" }));

    expect(await screen.findByText("recommendation.catalog.active 仅由推荐知识目录页面维护。")).toBeInTheDocument();
    expect(api.putConfigurationVersion).not.toHaveBeenCalled();
  });

  it("tests only the selected active model connection and renders safe metadata", async () => {
    api.getConfigurations.mockResolvedValue({ items: [modelItem], nextCursor: null });
    api.getConfigurationItem.mockResolvedValue(modelItem);
    api.getConfigurationVersions.mockResolvedValue(history(modelItem));
    render(<ConfigurationPage />);

    fireEvent.click(await screen.findByRole("button", { name: /chat\.primary/ }));
    const testButton = await screen.findByRole("button", { name: "测试模型连接" });
    fireEvent.click(testButton);

    await waitFor(() => expect(api.probeModelConnection).toHaveBeenCalledWith("chat.primary"));
    expect(screen.getByText("api.example.com")).toBeInTheDocument();
    expect(screen.getByText("provider-model-v3")).toBeInTheDocument();
    expect(screen.getByText("42 ms")).toBeInTheDocument();
    expect(screen.getByText(/r3 · c{14}…/)).toBeInTheDocument();
  });
});
