interface Props {
  open: boolean;
  onClose: () => void;
}

export function HelpDrawer({ open, onClose }: Props) {
  if (!open) return null;

  return (
    <div className="help-overlay" onClick={onClose}>
      <div className="help-drawer" role="dialog" aria-modal="true" aria-labelledby="help-title" onClick={(event) => event.stopPropagation()}>
        <div className="help-header">
          <h2 id="help-title">Pintia 快照导入指南</h2>
          <button className="btn btn-icon" type="button" onClick={onClose} title="关闭">✕</button>
        </div>

        <div className="help-body">
          <section className="help-section">
            <h3>操作流程</h3>
            <ol className="help-steps">
              <li>在 Pintia 题目集页面使用 AscendAny 浏览器插件导出完整 JSON 快照。</li>
              <li>将一份 <code>.json</code> 文件拖入上传区域，或点击上传区域选择文件。</li>
              <li>上传成功后任务自动进入持久队列；当前任务和实时日志会持续显示进度。</li>
              <li>任务结束后可在导入历史中查看状态、阶段和公开错误信息。</li>
            </ol>
          </section>

          <section className="help-section">
            <h3>Snapshot v2 契约</h3>
            <div className="help-cards">
              <div className="help-card">
                <p>控制台只接收浏览器插件生成的完整快照，顶层字段固定为：</p>
                <ul>
                  <li><code>schema: "ascendany.pintia.snapshot.v2"</code></li>
                  <li><code>schemaSha256</code>、<code>exporter</code>、<code>exam</code></li>
                  <li><code>problems</code>、<code>participants</code>、<code>submissions</code></li>
                  <li><code>completeness</code> 完整性证明</li>
                </ul>
                <p className="help-note">结构校验与跨记录语义校验通过后，整场考试才会写入。</p>
              </div>
            </div>
          </section>

          <section className="help-section">
            <h3>任务语义</h3>
            <dl className="help-defs">
              <dt>自动入队</dt>
              <dd>快照字节持久化后立即创建任务，无需再次点击启动。</dd>
              <dt>幂等上传</dt>
              <dd>服务端按 artifact SHA-256 识别相同内容；重复上传不会重复写入考试数据。</dd>
              <dt>考试事务</dt>
              <dd>每份完整快照对应一场考试，所有业务写入在同一事务中提交或回滚。</dd>
              <dt>可恢复事件流</dt>
              <dd>连接到期时控制台从最后一条 durable event sequence 继续读取。</dd>
            </dl>
          </section>

          <section className="help-section">
            <h3>故障处理</h3>
            <dl className="help-defs">
              <dt>上传被拒绝</dt>
              <dd>重新使用当前版本浏览器插件导出完整题目集快照，保留任务显示的错误信息用于定位。</dd>
              <dt>事件流中断</dt>
              <dd>控制台会基于最后事件序号恢复；刷新页面后也可从导入历史确认最终状态。</dd>
              <dt>任务失败</dt>
              <dd>失败任务不会留下部分考试数据。修复数据源并重新导出后，可上传新快照。</dd>
            </dl>
          </section>

          <section className="help-section">
            <h3>安全与架构</h3>
            <p>
              控制台通过生成的 <code>@ascendany/sdk</code> 调用 Go v2 runtime。Access token 只保存在内存，
              refresh credential 使用 HttpOnly cookie，浏览器仅持久化旋转 CSRF token。
            </p>
            <p>
              Go runtime 负责快照持久化、严格校验、任务租约、考试事务、分析生成和 PostgreSQL 状态；
              SSE 提供有序的持久任务事件。
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
