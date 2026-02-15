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
                <strong>选择类型</strong> — 在左侧面板的下拉框中选择考试类型（数据结构 / PTA ICPC / PTA IOI）
              </li>
              <li>
                <strong>上传 ZIP</strong> — 将打包好的 .zip 文件拖入上传区域，或点击区域选择文件。
                ZIP 内应为完整的考试目录（含提交记录、成绩单等子目录）
              </li>
              <li>
                <strong>点击导入</strong> — 上传完成后点击「开始增量导入」按钮，日志区实时显示导入进度，完成后查看汇总报告
              </li>
            </ol>
          </section>

          {/* ZIP Format */}
          <section className="help-section">
            <h3>📦 ZIP 打包规范</h3>
            <div className="help-cards">
              <div className="help-card">
                <h4>目录结构</h4>
                <p>将一场考试的完整目录打成 .zip，例如：</p>
                <ul>
                  <li><code>月测1/提交记录/*.csv</code></li>
                  <li><code>月测1/成绩单/*.xlsx</code></li>
                  <li><code>月测1/答卷/*.html</code>（可选）</li>
                </ul>
                <p className="help-note">ZIP 文件名即为考试名称（去掉 .zip 后缀）。可一次上传多个 .zip。</p>
              </div>
            </div>
          </section>

          {/* Exam Types */}
          <section className="help-section">
            <h3>📂 三种考试类型</h3>
            <div className="help-cards">
              <div className="help-card">
                <h4>📚 数据结构月测</h4>
                <p>随机组卷模式的月度测验。每场考试包含：</p>
                <ul>
                  <li><code>答卷/*.html</code> — 题目抽签信息</li>
                  <li><code>提交记录/*.csv</code> — 提交数据 (UTF-8)</li>
                  <li><code>成绩单/*.xlsx</code> — 成绩汇总</li>
                </ul>
                <p className="help-note">提交者使用短昵称；系统会在导入时自动尝试按昵称认领绑定。</p>
              </div>

              <div className="help-card">
                <h4>🏆 PTA ICPC 题目集</h4>
                <p>ICPC 计分规则的编程练习。特点：</p>
                <ul>
                  <li>CSV 编码为 <code>gb18030</code></li>
                  <li>成绩单为 ICPC 榜单格式 (<code>+1\n61</code>)</li>
                  <li>提交记录含学号信息</li>
                </ul>
              </div>

              <div className="help-card">
                <h4>📝 PTA IOI 题目集</h4>
                <p>IOI 分数制的编程练习。特点：</p>
                <ul>
                  <li>CSV 编码为 <code>gb18030</code></li>
                  <li>IOI 计分，最高分取优</li>
                  <li>与数据结构月测类似的成绩单格式</li>
                </ul>
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
                上传的 ZIP 会解压到服务器的 <code>PRACTICE_DATA_ROOT</code> 对应的考试类型目录下。
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
              上传的 ZIP 文件会在服务端解压到 practice 目录，然后在线程池中调用
              <code>preprocess</code> 模块执行增量导入。
              导入进度通过 Server-Sent Events (SSE) 实时推送到前端。
            </p>
            <p>
              导入流程：上传 → 解压 → 指纹比对 → 解析 CSV/XLSX/HTML → 写入考试/题目/参赛者/提交记录
              → 计算五维能力指标 → 计算 Rating → 更新学生当前画像
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
