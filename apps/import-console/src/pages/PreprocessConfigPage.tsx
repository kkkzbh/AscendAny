import { useEffect, useState } from "react";
import { getAdminConfig, patchAdminConfig, type AdminPreprocessConfig } from "../api/admin";
import { Alert, Field, PageHeader } from "../components/ui";

const EMPTY_PREPROCESS: AdminPreprocessConfig = {
  practiceRoot: "",
  encodings: [],
  fingerprintRoles: [],
  timezone: "Asia/Shanghai",
  metrics: {
    winsorLow: 0.05,
    winsorHigh: 0.95,
    flexibilityModeDefault: "approx",
    includedProblemKinds: [],
    randomExamMissingDrawnSetPolicy: "",
    randomExamSlotSourcePriority: [],
  },
  mapping: {
    primaryKeys: [],
    actorSources: [],
    strictMode: true,
    autoBindOnIngest: true,
    claimIdentitySource: "",
  },
  fusionHalfLifeDays: {
    knowledge: 45,
    accuracy: 21,
    quality: 45,
    flexibility: 21,
    proficiency: 21,
  },
  rating: {
    initialRating: 800,
    maxBinarySearchRating: 8000,
    minBinarySearchRating: -2000,
    binarySearchSteps: 30,
  },
  warmup: {
    enabled: false,
    apiBaseUrl: null,
    tokenEnv: "",
    timeoutSeconds: 30,
    roleId: "xiaoD",
  },
};

function joinList(values: string[]): string {
  return values.join(", ");
}

function splitList(value: string): string[] {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

export function PreprocessConfigPage() {
  const [config, setConfig] = useState<AdminPreprocessConfig>(EMPTY_PREPROCESS);
  const [configPath, setConfigPath] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const response = await getAdminConfig();
        if (!cancelled) {
          setConfig(response.preprocess);
          setConfigPath(response.preprocessConfigPath);
        }
      } catch (loadError) {
        if (!cancelled) setError(loadError instanceof Error ? loadError.message : "配置加载失败");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const save = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const response = await patchAdminConfig({ preprocess: config });
      setConfig(response.preprocess);
      setMessage("预处理参数已保存");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="page">
      <PageHeader
        title="预处理参数"
        description="管理导入根目录、编码、指标计算、rating 与学生身份绑定参数。"
        actions={
          <button className="button button-primary" type="button" onClick={save} disabled={loading || saving}>
            {saving ? "保存中" : "保存配置"}
          </button>
        }
      />

      {error ? <Alert>{error}</Alert> : null}
      {message ? <Alert tone="success">{message}</Alert> : null}

      <div className="settings-grid settings-grid-wide">
        <section className="panel settings-panel">
          <div className="panel-title">数据源与解析</div>
          <div className="form-grid">
            <Field label="Practice Root">
              <input value={config.practiceRoot} onChange={(event) => setConfig({ ...config, practiceRoot: event.target.value })} />
            </Field>
            <Field label="Encodings" hint="逗号分隔，按顺序尝试。">
              <input value={joinList(config.encodings)} onChange={(event) => setConfig({ ...config, encodings: splitList(event.target.value) })} />
            </Field>
            <Field label="Fingerprint Roles">
              <input value={joinList(config.fingerprintRoles)} onChange={(event) => setConfig({ ...config, fingerprintRoles: splitList(event.target.value) })} />
            </Field>
            <Field label="Timezone">
              <input value={config.timezone} onChange={(event) => setConfig({ ...config, timezone: event.target.value })} />
            </Field>
          </div>
        </section>

        <section className="panel settings-panel">
          <div className="panel-title">指标与 Rating</div>
          <div className="form-grid compact-grid">
            <Field label="Winsor Low">
              <input type="number" step="0.01" value={config.metrics.winsorLow} onChange={(event) => setConfig({ ...config, metrics: { ...config.metrics, winsorLow: Number(event.target.value) } })} />
            </Field>
            <Field label="Winsor High">
              <input type="number" step="0.01" value={config.metrics.winsorHigh} onChange={(event) => setConfig({ ...config, metrics: { ...config.metrics, winsorHigh: Number(event.target.value) } })} />
            </Field>
            <Field label="Initial Rating">
              <input type="number" value={config.rating.initialRating} onChange={(event) => setConfig({ ...config, rating: { ...config.rating, initialRating: Number(event.target.value) } })} />
            </Field>
            <Field label="Binary Search Steps">
              <input type="number" value={config.rating.binarySearchSteps} onChange={(event) => setConfig({ ...config, rating: { ...config.rating, binarySearchSteps: Number(event.target.value) } })} />
            </Field>
            <Field label="Included Problem Kinds">
              <input value={joinList(config.metrics.includedProblemKinds)} onChange={(event) => setConfig({ ...config, metrics: { ...config.metrics, includedProblemKinds: splitList(event.target.value) } })} />
            </Field>
            <Field label="Flexibility Mode">
              <input value={config.metrics.flexibilityModeDefault} onChange={(event) => setConfig({ ...config, metrics: { ...config.metrics, flexibilityModeDefault: event.target.value } })} />
            </Field>
          </div>
        </section>

        <section className="panel settings-panel">
          <div className="panel-title">半衰期与身份绑定</div>
          <div className="form-grid compact-grid">
            {(["knowledge", "accuracy", "quality", "flexibility", "proficiency"] as const).map((key) => (
              <Field key={key} label={`${key} Half-life`}>
                <input
                  type="number"
                  value={config.fusionHalfLifeDays[key]}
                  onChange={(event) => setConfig({
                    ...config,
                    fusionHalfLifeDays: { ...config.fusionHalfLifeDays, [key]: Number(event.target.value) },
                  })}
                />
              </Field>
            ))}
            <Field label="Primary Keys">
              <input value={joinList(config.mapping.primaryKeys)} onChange={(event) => setConfig({ ...config, mapping: { ...config.mapping, primaryKeys: splitList(event.target.value) } })} />
            </Field>
            <Field label="Actor Sources">
              <input value={joinList(config.mapping.actorSources)} onChange={(event) => setConfig({ ...config, mapping: { ...config.mapping, actorSources: splitList(event.target.value) } })} />
            </Field>
            <Field label="Claim Identity Source">
              <input value={config.mapping.claimIdentitySource} onChange={(event) => setConfig({ ...config, mapping: { ...config.mapping, claimIdentitySource: event.target.value } })} />
            </Field>
            <label className="check-control inline-check">
              <input
                type="checkbox"
                checked={config.mapping.autoBindOnIngest}
                onChange={(event) => setConfig({ ...config, mapping: { ...config.mapping, autoBindOnIngest: event.target.checked } })}
              />
              导入时自动绑定
            </label>
          </div>
        </section>

        <aside className="panel inspector-panel">
          <div className="panel-title">配置来源</div>
          <dl className="detail-list">
            <dt>文件</dt>
            <dd>{configPath || "-"}</dd>
            <dt>Warmup</dt>
            <dd>{config.warmup.enabled ? "启用" : "关闭"}</dd>
            <dt>Role</dt>
            <dd>{config.warmup.roleId}</dd>
          </dl>
        </aside>
      </div>
    </div>
  );
}
