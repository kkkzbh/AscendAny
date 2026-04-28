import { useEffect, useMemo, useState } from "react";
import {
  getAdminModelConfig,
  listAdminDeepSeekModels,
  patchAdminModelConfig,
  testAdminModelConnection,
  type AdminModelConfigResponse,
  type AdminModelOption,
  type AdminModelProviderConfig,
  type AdminModelProviderId,
} from "../api/admin";
import { Alert, Field, PageHeader } from "../components/ui";
import { MODEL_PROVIDER_IDS, MODEL_PROVIDER_LABELS } from "../modelTabs";

type ModelDraft = Pick<AdminModelProviderConfig, "id" | "adapter" | "baseUrl" | "model" | "apiKeyEnv" | "requestMode">;
type DraftMap = Partial<Record<AdminModelProviderId, ModelDraft>>;
type ApiKeyMap = Partial<Record<AdminModelProviderId, string>>;
type DeepSeekModelState = {
  options: AdminModelOption[];
  source: "dynamic" | "static";
  error: string | null;
};

function getProviderList(config: AdminModelConfigResponse): AdminModelProviderConfig[] {
  if (!Array.isArray(config.providers)) {
    throw new Error("模型配置接口返回旧结构或异常结构，请重启 API 服务后刷新页面。");
  }
  return config.providers;
}

function draftsFromConfig(config: AdminModelConfigResponse): DraftMap {
  return Object.fromEntries(
    getProviderList(config).map((provider) => [
      provider.id,
      {
        id: provider.id,
        adapter: provider.adapter,
        baseUrl: provider.baseUrl,
        model: provider.model,
        apiKeyEnv: provider.apiKeyEnv,
        requestMode: provider.requestMode,
      },
    ]),
  ) as DraftMap;
}

function sameDraft(a?: ModelDraft, b?: ModelDraft): boolean {
  if (!a || !b) return false;
  return a.adapter === b.adapter && a.baseUrl === b.baseUrl && a.model === b.model && a.apiKeyEnv === b.apiKeyEnv && a.requestMode === b.requestMode;
}

function makeEmptyKeyMap(): ApiKeyMap {
  return MODEL_PROVIDER_IDS.reduce<ApiKeyMap>((acc, id) => {
    acc[id] = "";
    return acc;
  }, {});
}

function optionWithCurrent(options: AdminModelOption[], model: string): AdminModelOption[] {
  if (options.some((option) => option.modelId === model)) return options;
  return [
    {
      modelId: model,
      label: `${model}（当前值）`,
      requestMode: "chat_completions",
      deprecated: false,
      disabled: false,
      disabledReason: null,
    },
    ...options,
  ];
}

export function ModelConfigPage() {
  const [config, setConfig] = useState<AdminModelConfigResponse | null>(null);
  const [originalDrafts, setOriginalDrafts] = useState<DraftMap>({});
  const [drafts, setDrafts] = useState<DraftMap>({});
  const [apiKeys, setApiKeys] = useState<ApiKeyMap>(makeEmptyKeyMap);
  const [activeProvider, setActiveProvider] = useState<AdminModelProviderId>("deepseek");
  const [originalActiveProvider, setOriginalActiveProvider] = useState<AdminModelProviderId>("deepseek");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [refreshingModels, setRefreshingModels] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [deepSeekState, setDeepSeekState] = useState<DeepSeekModelState | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const response = await getAdminModelConfig();
        if (cancelled) return;
        const nextDrafts = draftsFromConfig(response);
        setConfig(response);
        setOriginalDrafts(nextDrafts);
        setDrafts(nextDrafts);
        setActiveProvider(response.activeProvider);
        setOriginalActiveProvider(response.activeProvider);
        setApiKeys(makeEmptyKeyMap());
        const deepseekTab = response.providers.find((provider) => provider.id === "deepseek");
        if (deepseekTab) {
          setDeepSeekState({
            options: deepseekTab.modelOptions,
            source: "static",
            error: null,
          });
        }
      } catch (loadError) {
        if (!cancelled) setError(loadError instanceof Error ? loadError.message : "模型配置加载失败");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const providerById = useMemo(() => {
    const map = new Map<AdminModelProviderId, AdminModelProviderConfig>();
    if (Array.isArray(config?.providers)) {
      config.providers.forEach((provider) => map.set(provider.id, provider));
    }
    return map;
  }, [config]);

  const currentProvider = providerById.get(activeProvider);
  const currentDraft = drafts[activeProvider];
  const currentApiKey = apiKeys[activeProvider] ?? "";

  const dirtyProviderIds = MODEL_PROVIDER_IDS.filter((providerId) => {
    const keyChanged = Boolean((apiKeys[providerId] ?? "").trim());
    return keyChanged || !sameDraft(drafts[providerId], originalDrafts[providerId]);
  });
  const hasDirty = dirtyProviderIds.length > 0 || activeProvider !== originalActiveProvider;

  const currentOptions = useMemo(() => {
    if (!currentProvider || !currentDraft) return [];
    const options = activeProvider === "deepseek" && deepSeekState
      ? deepSeekState.options
      : currentProvider.modelOptions;
    return optionWithCurrent(options, currentDraft.model);
  }, [activeProvider, currentDraft, currentProvider, deepSeekState]);

  const updateDraft = (patch: Partial<ModelDraft>) => {
    setDrafts((prev) => {
      const current = prev[activeProvider];
      if (!current) return prev;
      return {
        ...prev,
        [activeProvider]: { ...current, ...patch },
      };
    });
  };

  const applyResponse = (response: AdminModelConfigResponse) => {
    const nextDrafts = draftsFromConfig(response);
    setConfig(response);
    setOriginalDrafts(nextDrafts);
    setDrafts(nextDrafts);
    setActiveProvider(response.activeProvider);
    setOriginalActiveProvider(response.activeProvider);
    setApiKeys(makeEmptyKeyMap());
    const deepseekTab = response.providers.find((provider) => provider.id === "deepseek");
    if (deepseekTab) {
      setDeepSeekState((prev) => ({
        options: prev?.source === "dynamic" ? prev.options : deepseekTab.modelOptions,
        source: prev?.source ?? "static",
        error: prev?.error ?? null,
      }));
    }
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    setTestResult(null);
    try {
      let response: AdminModelConfigResponse | null = null;
      if (dirtyProviderIds.length === 0) {
        response = await patchAdminModelConfig({ activeProvider });
      } else {
        for (const providerId of dirtyProviderIds) {
          const draft = drafts[providerId];
          if (!draft) continue;
          response = await patchAdminModelConfig({
            activeProvider,
            provider: {
              id: providerId,
              baseUrl: draft.baseUrl,
              model: draft.model,
              apiKeyEnv: draft.apiKeyEnv,
              adapter: draft.adapter,
              requestMode: draft.requestMode,
              apiKey: (apiKeys[providerId] ?? "").trim() || undefined,
            },
          });
        }
      }
      if (response) {
        applyResponse(response);
        setMessage("模型配置已保存");
      }
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const runConnectionTest = async () => {
    if (!currentDraft) return;
    setTesting(true);
    setError(null);
    setTestResult(null);
    try {
      const result = await testAdminModelConnection({
        providerId: activeProvider,
        baseUrl: currentDraft.baseUrl,
        model: currentDraft.model,
        apiKeyEnv: currentDraft.apiKeyEnv,
        adapter: currentDraft.adapter,
        requestMode: currentDraft.requestMode,
        apiKey: currentApiKey.trim() || undefined,
      });
      setTestResult(`${result.ok ? "成功" : "失败"}：${result.message}（${result.elapsedMs}ms）`);
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : "连接测试失败");
    } finally {
      setTesting(false);
    }
  };

  const refreshDeepSeekModels = async () => {
    const deepseekDraft = drafts.deepseek;
    if (!deepseekDraft) return;
    setRefreshingModels(true);
    setError(null);
    try {
      const response = await listAdminDeepSeekModels({
        baseUrl: deepseekDraft.baseUrl,
        model: deepseekDraft.model,
        apiKeyEnv: deepseekDraft.apiKeyEnv,
        adapter: deepseekDraft.adapter,
        requestMode: deepseekDraft.requestMode,
        apiKey: (apiKeys.deepseek ?? "").trim() || undefined,
      });
      setDeepSeekState({
        options: response.models,
        source: response.source,
        error: response.error,
      });
      setMessage(response.source === "dynamic" ? "DeepSeek 模型列表已刷新" : "已使用 DeepSeek 静态兜底模型列表");
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : "刷新 DeepSeek 模型失败");
    } finally {
      setRefreshingModels(false);
    }
  };

  if (!currentProvider || !currentDraft) {
    return (
      <div className="page">
        <PageHeader title="模型配置" description="加载管理员全局模型配置。" />
        {error ? <Alert>{error}</Alert> : null}
        {loading ? <div className="panel empty-state">正在加载模型配置</div> : null}
      </div>
    );
  }

  return (
    <div className="page">
      <PageHeader
        title="模型配置"
        description="配置管理员全局模型接口；聊天与考试分析统一使用当前启用的服务端默认模型。"
        actions={
          <>
            <button className="button" type="button" onClick={runConnectionTest} disabled={loading || testing}>
              {testing ? "测试中" : "连接测试"}
            </button>
            <button className="button button-primary" type="button" onClick={save} disabled={loading || saving || !hasDirty}>
              {saving ? "保存中" : "保存配置"}
            </button>
          </>
        }
      />

      {error ? <Alert>{error}</Alert> : null}
      {message ? <Alert tone="success">{message}</Alert> : null}
      {testResult ? <Alert tone={testResult.startsWith("成功") ? "success" : "warning"}>{testResult}</Alert> : null}

      <section className="panel model-config-panel">
        <div className="model-tab-row" role="tablist" aria-label="模型 Provider">
          {MODEL_PROVIDER_IDS.map((providerId) => {
            const provider = providerById.get(providerId);
            const dirty = dirtyProviderIds.includes(providerId);
            return (
              <button
                key={providerId}
                type="button"
                role="tab"
                aria-selected={activeProvider === providerId}
                className={`model-tab${activeProvider === providerId ? " is-active" : ""}${dirty ? " is-dirty" : ""}`}
                onClick={() => setActiveProvider(providerId)}
              >
                <span>{provider?.title ?? MODEL_PROVIDER_LABELS[providerId]}</span>
                {provider?.active ? <b>当前</b> : null}
                {dirty ? <i aria-label="未保存" /> : null}
              </button>
            );
          })}
        </div>

        <div className="model-config-body">
          <div className="model-config-form">
            <div className="model-badges">
              <span className="status status-neutral">{currentProvider.provider}</span>
              <span className="status status-neutral">{currentProvider.strategyId}</span>
              <span className="status status-neutral">{currentProvider.requestMode === "responses" ? "responses" : "chat/completions"}</span>
              {activeProvider === config?.activeProvider ? (
                <span className="status status-success">当前使用</span>
              ) : (
                <span className="status status-warning">保存后启用</span>
              )}
            </div>

            <div className="form-grid">
              <Field label="Base URL">
                <input value={currentDraft.baseUrl} onChange={(event) => updateDraft({ baseUrl: event.target.value })} />
              </Field>
              <Field label="默认模型" hint={currentProvider.modelHint}>
                <select
                  value={currentDraft.model}
                  onChange={(event) => {
                    const model = event.target.value;
                    const option = currentOptions.find((item) => item.modelId === model);
                    const requestMode = option?.requestMode ?? "chat_completions";
                    updateDraft({
                      model,
                      requestMode,
                      adapter: requestMode === "responses" ? "responses" : "openai_compatible",
                    });
                  }}
                >
                  {currentOptions.map((option) => (
                    <option key={option.modelId} value={option.modelId} disabled={option.disabled}>
                      {option.label}{option.disabledReason ? ` - ${option.disabledReason}` : ""}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="API Key Env" hint="保存 Key 时写入这个环境变量名。">
                <input value={currentDraft.apiKeyEnv} onChange={(event) => updateDraft({ apiKeyEnv: event.target.value })} />
              </Field>
              <Field
                label="API Key"
                hint={currentProvider.apiKeyConfigured ? "已配置，留空不变。" : "尚未配置；输入后写入本地 .env 文件。"}
              >
                <input
                  type="password"
                  value={currentApiKey}
                  placeholder={currentProvider.apiKeyConfigured ? "已配置，留空不变" : "输入 API Key"}
                  onChange={(event) => setApiKeys((prev) => ({ ...prev, [activeProvider]: event.target.value }))}
                />
              </Field>
            </div>

            {activeProvider === "deepseek" ? (
              <div className="model-secondary-action">
                <button className="button" type="button" onClick={refreshDeepSeekModels} disabled={refreshingModels}>
                  {refreshingModels ? "刷新中" : "刷新 DeepSeek 模型列表"}
                </button>
                <span>
                  来源：{deepSeekState?.source === "dynamic" ? "官方动态" : "静态兜底"}
                  {deepSeekState?.error ? ` · ${deepSeekState.error}` : ""}
                </span>
              </div>
            ) : null}
          </div>

          <aside className="model-config-inspector">
            <div className="panel-title">运行时镜像</div>
            <dl className="detail-list">
              <dt>配置文件</dt>
              <dd>{config?.configPath ?? "-"}</dd>
              <dt>本地 Env 文件</dt>
              <dd>{config?.envFilePath ?? "-"}</dd>
              <dt>Server Default</dt>
              <dd>{config?.activeRuntime.model ?? "-"} · {config?.activeRuntime.apiKeyEnv ?? "-"}</dd>
              <dt>Provider 说明</dt>
              <dd>{currentProvider.description}</dd>
              <dt>Transport Model</dt>
              <dd>{currentProvider.transportModel}</dd>
            </dl>
          </aside>
        </div>
      </section>
    </div>
  );
}
