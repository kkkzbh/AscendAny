import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigurationItem,
  ConfigurationItemPage,
  ConfigurationVersionPage,
  CreateConfigurationVersionResult,
  CreateGenericConfigurationVersionRequest,
  ModelConnectionProbeResult,
} from "@ascendany/sdk";
import {
  getConfigurationItem,
  getConfigurations,
  getConfigurationVersions,
  probeModelConnection,
  putConfigurationVersion,
} from "./configuration";

const sdk = vi.hoisted(() => ({
  createConfigurationVersion: vi.fn(),
  getConfiguration: vi.fn(),
  listConfigurations: vi.fn(),
  listConfigurationVersions: vi.fn(),
  testModelConnection: vi.fn(),
}));

const transport = vi.hoisted(() => ({
  ensureAuthenticated: vi.fn(),
  client: { kind: "browser-session-client" },
}));

vi.mock("@ascendany/sdk", () => sdk);
vi.mock("./v2Client", () => ({
  browserSession: { ensureAuthenticated: transport.ensureAuthenticated },
  v2Client: transport.client,
  apiFailureMessage: (error: unknown) => error instanceof Error ? error.message : "请求失败",
}));

const item: ConfigurationItem = {
  id: "11111111-1111-4111-8111-111111111111",
  key: "prompt.default",
  kind: "prompt",
  headRevision: 0,
  activeVersion: null,
  createdAt: "2026-07-11T00:00:00Z",
  updatedAt: "2026-07-11T00:00:00Z",
};

describe("v2 configuration API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.listConfigurations.mockResolvedValue({ data: { items: [item], nextCursor: null } satisfies ConfigurationItemPage });
    sdk.getConfiguration.mockResolvedValue({ data: item });
    sdk.listConfigurationVersions.mockResolvedValue({ data: {
      key: item.key,
      kind: item.kind,
      headRevision: item.headRevision,
      items: [],
      nextBeforeNumber: null,
    } satisfies ConfigurationVersionPage });
    sdk.createConfigurationVersion.mockResolvedValue({ data: { item, idempotent: false } satisfies CreateConfigurationVersionResult });
    sdk.testModelConnection.mockResolvedValue({ data: {
      configurationKey: "chat.primary",
      configurationHeadRevision: 3,
      configurationVersion: 3,
      configurationSha256: "c".repeat(64),
      authority: "api.example.com",
      model: "provider-model-v3",
      checkedAt: "2026-07-11T06:07:08Z",
      latencyMilliseconds: 42,
    } satisfies ModelConnectionProbeResult });
  });

  it("uses generated canonical pagination and detail operations", async () => {
    await getConfigurations(12, "prompt", "prompt.before");
    await getConfigurationItem(item.key);
    await getConfigurationVersions(item.key, 8, 3);

    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(3);
    expect(sdk.listConfigurations).toHaveBeenCalledWith({
      client: transport.client,
      query: { limit: 12, kind: "prompt", afterKey: "prompt.before" },
      throwOnError: true,
    });
    expect(sdk.getConfiguration).toHaveBeenCalledWith({
      client: transport.client,
      path: { key: item.key },
      throwOnError: true,
    });
    expect(sdk.listConfigurationVersions).toHaveBeenCalledWith({
      client: transport.client,
      path: { key: item.key },
      query: { limit: 8, beforeNumber: 3 },
      throwOnError: true,
    });
  });

  it("publishes the exact CAS request through the generated operation", async () => {
    const request: CreateGenericConfigurationVersionRequest = {
      key: "feedback.delivery.default",
      kind: "feedback_delivery",
      expectedHeadRevision: 0,
      schemaId: "ascendany.feedback_delivery.webhook.v1",
      document: { url: "https://feedback.example/v1", timeoutMilliseconds: 5000 },
      credentialRef: "feedback.webhook.primary",
    };

    await expect(putConfigurationVersion(request)).resolves.toEqual({ item, idempotent: false });
    expect(sdk.createConfigurationVersion).toHaveBeenCalledWith({
      client: transport.client,
      body: request,
      throwOnError: true,
    });
    expect(transport.ensureAuthenticated.mock.invocationCallOrder[0]).toBeLessThan(
      sdk.createConfigurationVersion.mock.invocationCallOrder[0] ?? 0,
    );
  });

  it("tests a model connection through the generated empty-body operation", async () => {
    const result = await probeModelConnection("chat.primary");

    expect(result).toMatchObject({
      configurationKey: "chat.primary",
      authority: "api.example.com",
      latencyMilliseconds: 42,
    });
    expect(sdk.testModelConnection).toHaveBeenCalledWith({
      client: transport.client,
      path: { key: "chat.primary" },
      throwOnError: true,
    });
    expect(transport.ensureAuthenticated.mock.invocationCallOrder[0]).toBeLessThan(
      sdk.testModelConnection.mock.invocationCallOrder[0] ?? 0,
    );
  });

});
