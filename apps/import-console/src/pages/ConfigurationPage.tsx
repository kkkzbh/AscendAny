import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import {
  getConfigurationItem,
  getConfigurations,
  getConfigurationVersions,
  probeModelConnection,
  putConfigurationVersion,
  type ConfigurationItem,
  type ConfigurationKind,
  type ConfigurationVersion,
  type CreateGenericConfigurationVersionRequest,
  type ModelConnectionProbeResult,
} from "../api/configuration";
import { EmptyState, Field, PageHeader } from "../components/ui";

type EditableConfigurationKind = CreateGenericConfigurationVersionRequest["kind"];

const kinds: EditableConfigurationKind[] = [
  "prompt",
  "model_connection",
  "feedback_policy",
  "feedback_delivery",
];
const KNOWLEDGE_CATALOG_KEY = "recommendation.catalog.active";

const kindLabels: Record<ConfigurationKind, string> = {
  prompt: "Prompt",
  model_connection: "模型连接",
  knowledge_catalog: "知识目录",
  feedback_policy: "反馈策略",
  feedback_delivery: "反馈投递",
};

function defaultSchemaId(kind: EditableConfigurationKind): string {
  return `ascendany.${kind}.v1`;
}

interface EditorState {
  key: string;
  kind: EditableConfigurationKind;
  expectedHeadRevision: number;
  schemaId: string;
  document: string;
  credentialRef: string;
}

function editorFromItem(item: ConfigurationItem | null): EditorState {
  if (item?.kind === "knowledge_catalog") {
    throw new Error("Knowledge catalog 仅由停机发布流程写入。");
  }
  const active = item?.activeVersion ?? null;
  return {
    key: item?.key ?? "",
    kind: item?.kind ?? "prompt",
    expectedHeadRevision: item?.headRevision ?? 0,
    schemaId: active?.schemaId ?? "ascendany.prompt.v1",
    document: JSON.stringify(active?.document ?? {}, null, 2),
    credentialRef: active?.credentialRef ?? "",
  };
}

function formatTime(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN");
}

function shortHash(value: string): string {
  return value.length <= 14 ? value : `${value.slice(0, 14)}…`;
}

export function ConfigurationPage() {
  const [kindFilter, setKindFilter] = useState<EditableConfigurationKind | "">("");
  const [items, setItems] = useState<ConfigurationItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [selected, setSelected] = useState<ConfigurationItem | null>(null);
  const [versions, setVersions] = useState<ConfigurationVersion[]>([]);
  const [nextBeforeNumber, setNextBeforeNumber] = useState<number | null>(null);
  const [editor, setEditor] = useState<EditorState>(() => editorFromItem(null));
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [probing, setProbing] = useState(false);
  const [probeResult, setProbeResult] = useState<ModelConnectionProbeResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const loadItems = useCallback(async (afterKey?: string) => {
    setLoading(true);
    setError(null);
    try {
      const page = await getConfigurations(30, kindFilter || undefined, afterKey);
      const editableItems = page.items.filter((item) => item.kind !== "knowledge_catalog");
      setItems((current) => afterKey ? [...current, ...editableItems] : editableItems);
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "配置列表加载失败");
    } finally {
      setLoading(false);
    }
  }, [kindFilter]);

  const selectItem = useCallback(async (key: string) => {
    setDetailLoading(true);
    setError(null);
    setNotice(null);
    try {
      const [item, history] = await Promise.all([
        getConfigurationItem(key),
        getConfigurationVersions(key, 20),
      ]);
      setSelected(item);
      setVersions(history.items);
      setNextBeforeNumber(history.nextBeforeNumber);
      setEditor(editorFromItem(item));
      setProbeResult(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "配置详情加载失败");
    } finally {
      setDetailLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadItems();
  }, [loadItems]);

  const beginNew = () => {
    setSelected(null);
    setVersions([]);
    setNextBeforeNumber(null);
    setEditor(editorFromItem(null));
    setProbeResult(null);
    setError(null);
    setNotice(null);
  };

  const schemaHint = useMemo(() => `schemaId 必须以 ascendany.${editor.kind}. 开头。`, [editor.kind]);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      if (editor.key === KNOWLEDGE_CATALOG_KEY) {
        throw new Error("recommendation.catalog.active 仅由停机发布流程写入。");
      }
      const parsed: unknown = JSON.parse(editor.document);
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        throw new Error("Document 顶层必须是 JSON object。");
      }
      const result = await putConfigurationVersion({
        key: editor.key,
        kind: editor.kind,
        expectedHeadRevision: editor.expectedHeadRevision,
        schemaId: editor.schemaId,
        document: parsed as Record<string, unknown>,
        credentialRef: editor.credentialRef === "" ? null : editor.credentialRef,
      });
      setSelected(result.item);
      setProbeResult(null);
      setEditor(editorFromItem(result.item));
      setItems((current) => {
        const remaining = current.filter((item) => item.key !== result.item.key);
        return [...remaining, result.item].sort((left, right) => left.key.localeCompare(right.key, "en"));
      });
      const history = await getConfigurationVersions(result.item.key, 20);
      setVersions(history.items);
      setNextBeforeNumber(history.nextBeforeNumber);
      setNotice(result.idempotent ? "当前 immutable version 已存在，本次请求完成幂等重放。" : `已发布 revision ${result.item.headRevision}。`);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "配置发布失败");
    } finally {
      setSaving(false);
    }
  };

  const loadMoreVersions = async () => {
    if (selected === null || nextBeforeNumber === null) return;
    setDetailLoading(true);
    setError(null);
    try {
      const page = await getConfigurationVersions(selected.key, 20, nextBeforeNumber);
      setVersions((current) => [...current, ...page.items]);
      setNextBeforeNumber(page.nextBeforeNumber);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "版本历史加载失败");
    } finally {
      setDetailLoading(false);
    }
  };

  const runModelConnectionTest = async () => {
    if (selected === null || selected.kind !== "model_connection" || selected.activeVersion === null) return;
    setProbing(true);
    setProbeResult(null);
    setError(null);
    setNotice(null);
    try {
      setProbeResult(await probeModelConnection(selected.key));
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : "模型连接测试失败");
    } finally {
      setProbing(false);
    }
  };

  return (
    <div className="page configuration-page">
      <PageHeader
        title="运行配置"
        description="所有配置按 immutable version 保存；发布使用 expectedHeadRevision 进行 compare-and-swap。"
        actions={(
          <>
            <button className="button" type="button" onClick={() => void loadItems()} disabled={loading}>刷新</button>
            <button className="button button-primary" type="button" onClick={beginNew}>新建配置</button>
          </>
        )}
      />
      {error ? <div className="notice notice-error" role="alert">{error}</div> : null}
      {notice ? <div className="notice notice-success" role="status">{notice}</div> : null}
      <div className="configuration-layout">
        <section className="panel configuration-list-panel">
          <div className="panel-title">
            <span>Configuration heads</span>
            <select
              aria-label="配置类型筛选"
              value={kindFilter}
              onChange={(event) => setKindFilter(event.target.value as EditableConfigurationKind | "")}
            >
              <option value="">全部类型</option>
              {kinds.map((kind) => <option key={kind} value={kind}>{kindLabels[kind]}</option>)}
            </select>
          </div>
          <div className="configuration-list">
            {items.map((item) => (
              <button
                className={`configuration-list-item${selected?.key === item.key ? " is-active" : ""}`}
                key={item.key}
                type="button"
                onClick={() => void selectItem(item.key)}
              >
                <span><strong>{item.key}</strong><small>{kindLabels[item.kind]}</small></span>
                <span className="configuration-revision">r{item.headRevision}</span>
              </button>
            ))}
            {!loading && items.length === 0 ? <EmptyState>当前筛选下没有配置。</EmptyState> : null}
          </div>
          {nextCursor ? (
            <button className="button button-ghost configuration-more" type="button" disabled={loading} onClick={() => void loadItems(nextCursor)}>加载更多</button>
          ) : null}
        </section>

        <section className="configuration-detail">
          <form className="panel configuration-editor" onSubmit={(event) => void save(event)}>
            <div className="panel-title"><span>{selected ? `编辑 ${selected.key}` : "新建 Configuration"}</span><span>CAS r{editor.expectedHeadRevision}</span></div>
            <div className="configuration-form-grid">
              <Field label="Key" hint="lowercase canonical key，创建后不可改。">
                <input required value={editor.key} disabled={selected !== null} onChange={(event) => setEditor((current) => ({ ...current, key: event.target.value }))} />
              </Field>
              <Field label="Kind">
                <select value={editor.kind} disabled={selected !== null} onChange={(event) => {
                  const kind = event.target.value as EditableConfigurationKind;
                  setEditor((current) => ({ ...current, kind, schemaId: defaultSchemaId(kind) }));
                }}>
                  {kinds.map((kind) => <option key={kind} value={kind}>{kindLabels[kind]}</option>)}
                </select>
              </Field>
              <Field label="Schema ID" hint={schemaHint}>
                <input required value={editor.schemaId} onChange={(event) => setEditor((current) => ({ ...current, schemaId: event.target.value }))} />
              </Field>
              <Field label="Credential ref" hint="仅 model_connection / feedback_delivery 可填写；credential 本体不进入数据库。">
                <input value={editor.credentialRef} onChange={(event) => setEditor((current) => ({ ...current, credentialRef: event.target.value }))} />
              </Field>
              <Field label="Document" hint="严格 JSON object；发布后只允许创建下一版本。">
                <textarea className="configuration-document" required value={editor.document} onChange={(event) => setEditor((current) => ({ ...current, document: event.target.value }))} spellCheck={false} />
              </Field>
            </div>
            {probeResult ? (
              <dl className="model-probe-result" aria-label="模型连接测试结果">
                <div><dt>Provider</dt><dd>{probeResult.authority}</dd></div>
                <div><dt>Model</dt><dd>{probeResult.model}</dd></div>
                <div><dt>Latency</dt><dd>{probeResult.latencyMilliseconds} ms</dd></div>
                <div><dt>Version</dt><dd>r{probeResult.configurationVersion} · {shortHash(probeResult.configurationSha256)}</dd></div>
                <div><dt>Checked</dt><dd>{formatTime(probeResult.checkedAt)}</dd></div>
              </dl>
            ) : null}
            <div className="configuration-editor-actions">
              <span>{detailLoading ? "正在读取 durable state…" : `expectedHeadRevision=${editor.expectedHeadRevision}`}</span>
              <div className="configuration-action-buttons">
                {selected?.kind === "model_connection" && selected.activeVersion !== null ? (
                  <button className="button" type="button" disabled={probing || saving || detailLoading} onClick={() => void runModelConnectionTest()}>{probing ? "测试中" : "测试模型连接"}</button>
                ) : null}
                <button className="button button-primary" type="submit" disabled={saving || probing || detailLoading}>{saving ? "发布中" : "发布 immutable version"}</button>
              </div>
            </div>
          </form>

          <section className="panel configuration-history">
            <div className="panel-title">版本历史</div>
            {versions.length === 0 ? <EmptyState>{selected ? "当前没有已发布版本。" : "选择配置后查看版本历史。"}</EmptyState> : (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>Revision</th><th>Schema</th><th>SHA-256</th><th>Credential ref</th><th>创建时间</th></tr></thead>
                  <tbody>{versions.map((version) => (
                    <tr key={version.number}>
                      <td>r{version.number}</td>
                      <td>{version.schemaId}</td>
                      <td><code title={version.documentSha256}>{shortHash(version.documentSha256)}</code></td>
                      <td>{version.credentialRef ?? "-"}</td>
                      <td>{formatTime(version.createdAt)}</td>
                    </tr>
                  ))}</tbody>
                </table>
              </div>
            )}
            {nextBeforeNumber !== null ? <button className="button button-ghost configuration-more" type="button" disabled={detailLoading} onClick={() => void loadMoreVersions()}>加载更早版本</button> : null}
          </section>
        </section>
      </div>
    </div>
  );
}
