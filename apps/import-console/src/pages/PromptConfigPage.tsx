import { useEffect, useMemo, useState } from "react";
import {
  getAdminPrompt,
  getAdminPrompts,
  patchAdminPrompt,
  previewAdminPrompt,
  restoreAdminPrompt,
  type AdminPromptDetail,
  type AdminPromptSummary,
} from "../api/admin";
import { Alert, Field, PageHeader } from "../components/ui";

const CATEGORY_LABELS: Record<string, string> = {
  chat: "聊天与分析",
  context: "上下文片段",
  role: "角色风格",
};

function formatTime(value: string | null): string {
  if (!value) return "未保存";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}

function groupPrompts(items: AdminPromptSummary[]): Array<[string, AdminPromptSummary[]]> {
  const groups = new Map<string, AdminPromptSummary[]>();
  for (const item of items) {
    const key = item.category || "chat";
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }
  return Array.from(groups.entries());
}

export function PromptConfigPage() {
  const [items, setItems] = useState<AdminPromptSummary[]>([]);
  const [selectedKey, setSelectedKey] = useState<string>("");
  const [detail, setDetail] = useState<AdminPromptDetail | null>(null);
  const [draft, setDraft] = useState("");
  const [changeNote, setChangeNote] = useState("");
  const [preview, setPreview] = useState("");
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [restoringVersion, setRestoringVersion] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const groupedItems = useMemo(() => groupPrompts(items), [items]);
  const dirty = Boolean(detail && draft !== detail.content);
  const canSave = Boolean(detail && (detail.category === "role" || draft.trim()));

  async function reloadList(nextSelectedKey?: string) {
    const response = await getAdminPrompts();
    setItems(response.items);
    const targetKey = nextSelectedKey || selectedKey || response.items[0]?.key || "";
    setSelectedKey(targetKey);
    return targetKey;
  }

  async function loadDetail(key: string) {
    if (!key) return;
    setDetailLoading(true);
    setError(null);
    try {
      const response = await getAdminPrompt(key);
      setDetail(response);
      setDraft(response.content);
      setChangeNote("");
      setPreview("");
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "提示词加载失败");
    } finally {
      setDetailLoading(false);
    }
  }

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const response = await getAdminPrompts();
        if (cancelled) return;
        setItems(response.items);
        const firstKey = response.items[0]?.key ?? "";
        setSelectedKey(firstKey);
        if (firstKey) {
          const prompt = await getAdminPrompt(firstKey);
          if (cancelled) return;
          setDetail(prompt);
          setDraft(prompt.content);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "提示词配置加载失败");
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function selectPrompt(key: string) {
    if (key === selectedKey) return;
    setSelectedKey(key);
    setMessage(null);
    await loadDetail(key);
  }

  async function save() {
    if (!detail) return;
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const response = await patchAdminPrompt(detail.key, {
        content: draft,
        changeNote: changeNote.trim() || undefined,
      });
      setDetail(response);
      setDraft(response.content);
      setChangeNote("");
      setPreview("");
      await reloadList(response.key);
      setMessage("提示词已保存");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "提示词保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function runPreview() {
    if (!detail) return;
    setPreviewing(true);
    setError(null);
    setPreview("");
    try {
      const response = await previewAdminPrompt(detail.key, { content: draft });
      setPreview(response.rendered);
    } catch (previewError) {
      setError(previewError instanceof Error ? previewError.message : "提示词预览失败");
    } finally {
      setPreviewing(false);
    }
  }

  async function restore(version: number) {
    if (!detail) return;
    setRestoringVersion(version);
    setError(null);
    setMessage(null);
    try {
      const response = await restoreAdminPrompt(detail.key, version);
      setDetail(response);
      setDraft(response.content);
      setChangeNote("");
      setPreview("");
      await reloadList(response.key);
      setMessage(`已回滚到历史版本 ${version}`);
    } catch (restoreError) {
      setError(restoreError instanceof Error ? restoreError.message : "提示词回滚失败");
    } finally {
      setRestoringVersion(null);
    }
  }

  return (
    <div className="page">
      <PageHeader
        title="提示词管理"
        description="集中管理聊天、分析流程、上下文片段和内置角色风格提示词。"
        actions={
          <>
            <button className="button" type="button" onClick={runPreview} disabled={!detail || previewing}>
              {previewing ? "预览中" : "预览"}
            </button>
            <button className="button button-primary" type="button" onClick={save} disabled={!dirty || !canSave || saving}>
              {saving ? "保存中" : "保存提示词"}
            </button>
          </>
        }
      />

      {error ? <Alert>{error}</Alert> : null}
      {message ? <Alert tone="success">{message}</Alert> : null}

      <section className="prompt-config-layout">
        <aside className="panel prompt-list-panel" aria-label="提示词列表">
          {loading ? <div className="empty-state">正在加载提示词</div> : null}
          {!loading && groupedItems.length === 0 ? <div className="empty-state">暂无提示词</div> : null}
          {groupedItems.map(([category, prompts]) => (
            <div className="prompt-list-group" key={category}>
              <div className="prompt-list-group-title">
                {CATEGORY_LABELS[category] ?? category}
              </div>
              {prompts.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  className={`prompt-list-item${selectedKey === item.key ? " is-active" : ""}`}
                  onClick={() => void selectPrompt(item.key)}
                >
                  <span>{item.title}</span>
                  <small>{item.key}</small>
                  <i>v{item.version}</i>
                </button>
              ))}
            </div>
          ))}
        </aside>

        <div className="prompt-editor-stack">
          <section className="panel prompt-editor-panel">
            {!detail || detailLoading ? (
              <div className="empty-state">正在加载提示词详情</div>
            ) : (
              <>
                <div className="prompt-editor-header">
                  <div>
                    <h2>{detail.title}</h2>
                    <p>{detail.description}</p>
                  </div>
                  <div className="prompt-meta">
                    <span>{CATEGORY_LABELS[detail.category] ?? detail.category}</span>
                    <span>v{detail.version}</span>
                    <span>{formatTime(detail.updatedAt)}</span>
                  </div>
                </div>

                <div className="prompt-variable-row">
                  <span>可用变量</span>
                  {detail.allowedVariables.length ? (
                    detail.allowedVariables.map((variable) => (
                      <code key={variable}>{`{${variable}}`}</code>
                    ))
                  ) : (
                    <em>无变量</em>
                  )}
                </div>

                <Field label="提示词内容">
                  <textarea
                    className="prompt-editor-textarea"
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    spellCheck={false}
                  />
                </Field>

                <Field label="变更说明">
                  <input
                    className="input"
                    value={changeNote}
                    onChange={(event) => setChangeNote(event.target.value)}
                    placeholder="例如：收紧工具调用规则"
                  />
                </Field>

                <div className="prompt-editor-actions">
                  <button type="button" className="button" onClick={() => setDraft(detail.defaultContent)}>
                    使用内置默认
                  </button>
                  <button type="button" className="button" onClick={() => setDraft(detail.content)} disabled={!dirty}>
                    放弃草稿
                  </button>
                </div>
              </>
            )}
          </section>

          {preview ? (
            <section className="panel prompt-preview-panel">
              <h3>渲染预览</h3>
              <pre>{preview}</pre>
            </section>
          ) : null}

          {detail ? (
            <section className="panel prompt-history-panel">
              <h3>历史版本</h3>
              <div className="prompt-history-list">
                {detail.history.length === 0 ? <div className="empty-state">暂无历史版本</div> : null}
                {detail.history.map((version) => (
                  <div className="prompt-history-item" key={version.versionId}>
                    <div>
                      <b>v{version.version}</b>
                      <span>{version.changeNote || "无变更说明"}</span>
                      <small>{formatTime(version.createdAt)}</small>
                    </div>
                    <button
                      type="button"
                      className="button button-ghost"
                      onClick={() => void restore(version.version)}
                      disabled={restoringVersion === version.version}
                    >
                      {restoringVersion === version.version ? "回滚中" : "回滚"}
                    </button>
                  </div>
                ))}
              </div>
            </section>
          ) : null}
        </div>
      </section>
    </div>
  );
}
