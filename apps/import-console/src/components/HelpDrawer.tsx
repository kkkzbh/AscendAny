interface Props {
  open: boolean;
  onClose: () => void;
}

export function HelpDrawer({ open, onClose }: Props) {
  if (!open) return null;

  return (
    <div className="help-overlay" onClick={onClose}>
      <div className="help-drawer" onClick={(e) => e.stopPropagation()}>
        <div className="help-header">
          <h2>📖 操作指南</h2>
          <button className="btn btn-icon" onClick={onClose} title="关闭">✕</button>
        </div>

        <div className="help-body">
          {/* Quick Start */}
          <section className="help-section">
            <h3>🚀 快速开始</h3>
            <ol className="help-steps">
              <li>
                <strong>导出 JSON</strong> — 在 Pintia 题目集页面使用 AscendAny 浏览器插件导出单 JSON 文件。
              </li>
              <li>
                <strong>上传 JSON</strong> — 将插件导出的 .json 文件拖入上传区域，或点击区域选择文件。
                Pintia JSON 会自动识别考试类型，不需要手工选择。
              </li>
              <li>
                <strong>点击导入</strong> — 上传完成后点击「开始增量导入」按钮，日志区实时显示导入进度，完成后查看汇总报告
              </li>
            </ol>
          </section>

          {/* JSON Format */}
          <section className="help-section">
            <h3>📦 Pintia JSON 规范</h3>
            <div className="help-cards">
              <div className="help-card">
                <h4>主入口</h4>
                <p>默认只需要上传浏览器插件生成的单 JSON 文件。顶层必须包含：</p>
                <ul>
                  <li><code>schema: "ascendany.pintia.unit.v1"</code></li>
                  <li><code>problems</code> / <code>participants</code> / <code>submissions</code></li>
                  <li><code>integrity</code> 完整性计数</li>
                </ul>
                <p className="help-note">JSON 会递归落到 PRACTICE_DATA_ROOT 后被预处理识别为 Pintia 考试。</p>
              </div>
            </div>
          </section>

          {/* Legacy Types */}
          <section className="help-section">
            <h3>📂 Legacy ZIP</h3>
            <div className="help-cards">
              <div className="help-card">
                <h4>旧兼容格式</h4>
                <p>旧 CSV/XLSX/HTML ZIP 入口仅用于历史数据维护，不再作为推荐训练和 Pintia 外部考试的主链路。</p>
                <ul>
                  <li><code>答卷/*.html</code></li>
                  <li><code>提交记录/*.csv</code></li>
                  <li><code>成绩单/*.xlsx</code></li>
                </ul>
                <p className="help-note">新数据请优先使用 Pintia JSON。</p>
              </div>
            </div>
          </section>

          {/* Options */}
          <section className="help-section">
            <h3>⚙️ 操作选项</h3>
            <dl className="help-defs">
              <dt>Dry Run（试运行）</dt>
              <dd>
                勾选后，系统仅扫描并报告哪些考试将被处理，<strong>不会写入数据库</strong>。
                适合在正式导入前预览变更。
              </dd>

              <dt>Force（强制重处理）</dt>
              <dd>
                忽略指纹比较，<strong>强制重新处理所有已发现的考试</strong>。
                适用于数据修复或算法更新后需要全量刷新的场景。
              </dd>

              <dt>提交自动绑定</dt>
              <dd>
                导入时系统会自动将提交记录中的昵称绑定到已认领学生。
                未认领昵称会保留为“待认领提交”，学生注册或改昵称后会自动回填。
              </dd>
            </dl>
          </section>

          {/* FAQ */}
          <section className="help-section">
            <h3>❓ 常见问题</h3>
            <dl className="help-defs">
              <dt>导入失败怎么办？</dt>
              <dd>
                单场考试的导入失败不会影响其他考试。失败原因会在日志中详细显示。
                可以修复数据后再次执行导入（幂等保证）。
              </dd>

              <dt>重复导入安全吗？</dt>
              <dd>
                完全安全。系统通过指纹 (SHA256) 比对检测变更，未变化的考试自动跳过。
                行级数据通过 <code>row_hash</code> 唯一约束保证幂等。
              </dd>

              <dt>数据在哪里？</dt>
              <dd>
                上传的 JSON 或 legacy ZIP 会写入服务器的 <code>PRACTICE_DATA_ROOT</code>。
                导入后的结构化数据存储在 PostgreSQL 数据库中。
              </dd>

              <dt>导入进度卡住了？</dt>
              <dd>
                大量数据导入可能需要较长时间。日志区会持续显示心跳信号。
                如确认异常，可刷新页面重新操作（不会产生重复数据）。
              </dd>
            </dl>
          </section>

          {/* Architecture */}
          <section className="help-section">
            <h3>🔧 技术架构</h3>
            <p>
              本控制台通过 FastAPI 后端的 <code>/api/v1/import/*</code> 系列端点操作。
              上传的 Pintia JSON 会保存到 practice 目录，legacy ZIP 会在服务端解压，然后在线程池中调用
              <code>preprocess</code> 模块执行增量导入。
              导入进度通过 Server-Sent Events (SSE) 实时推送到前端。
            </p>
            <p>
              导入流程：上传 → 指纹比对 → 解析 Pintia JSON → 写入外部考试/题目/参赛者/提交/代码
              → 计算五维能力指标 → 计算 Rating → 更新学生当前画像
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
