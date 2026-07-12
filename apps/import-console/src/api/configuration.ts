import {
  createConfigurationVersion,
  getConfiguration,
  listConfigurations,
  listConfigurationVersions,
  testModelConnection,
  type ConfigurationItem,
  type ConfigurationItemPage,
  type ConfigurationKind,
  type ConfigurationVersionPage,
  type CreateConfigurationVersionResult,
  type CreateGenericConfigurationVersionRequest,
  type ModelConnectionProbeResult,
} from "@ascendany/sdk";
import { apiFailureMessage, browserSession, v2Client } from "./v2Client";

export type {
  ConfigurationItem,
  ConfigurationItemPage,
  ConfigurationKind,
  ConfigurationVersion,
  ConfigurationVersionPage,
  CreateConfigurationVersionResult,
  CreateGenericConfigurationVersionRequest,
  ModelConnectionProbeResult,
} from "@ascendany/sdk";

async function authenticated<T>(operation: () => Promise<T>): Promise<T> {
  try {
    await browserSession.ensureAuthenticated();
    return await operation();
  } catch (error) {
    throw new Error(apiFailureMessage(error));
  }
}

export function getConfigurations(
  limit = 30,
  kind?: ConfigurationKind,
  afterKey?: string,
): Promise<ConfigurationItemPage> {
  return authenticated(async () => {
    const result = await listConfigurations({
      client: v2Client,
      query: {
        limit,
        ...(kind ? { kind } : {}),
        ...(afterKey ? { afterKey } : {}),
      },
      throwOnError: true,
    });
    return result.data;
  });
}

export function getConfigurationItem(key: string): Promise<ConfigurationItem> {
  return authenticated(async () => {
    const result = await getConfiguration({
      client: v2Client,
      path: { key },
      throwOnError: true,
    });
    return result.data;
  });
}

export function getConfigurationVersions(
  key: string,
  limit = 20,
  beforeNumber?: number,
): Promise<ConfigurationVersionPage> {
  return authenticated(async () => {
    const result = await listConfigurationVersions({
      client: v2Client,
      path: { key },
      query: { limit, ...(beforeNumber === undefined ? {} : { beforeNumber }) },
      throwOnError: true,
    });
    return result.data;
  });
}

export function putConfigurationVersion(
  body: CreateGenericConfigurationVersionRequest,
): Promise<CreateConfigurationVersionResult> {
  return authenticated(async () => {
    const result = await createConfigurationVersion({
      client: v2Client,
      body,
      throwOnError: true,
    });
    return result.data;
  });
}

export function probeModelConnection(key: string): Promise<ModelConnectionProbeResult> {
  return authenticated(async () => {
    const result = await testModelConnection({
      client: v2Client,
      path: { key },
      throwOnError: true,
    });
    return result.data;
  });
}
