# AscendAny

[English](README.md) | 简体中文

<p align="center">
  <img src="image/LOGO_SHRIND.png" alt="AscendAny Logo" width="96" />
</p>

<p align="center">
  <strong>学生能力分析平台</strong>
</p>

AscendAny 把 Pintia 程序设计题目集的完整快照转换为可追溯的能力画像、Rating、成就、排行榜、考试分析和个性化学习建议。Web、Desktop 和 Mobile 共享账号、画像、考试、AI 对话与推荐等核心学生能力；各客户端按平台提供适配交互。管理员通过独立 Import Console 管理导入、账号、配置、审计与推荐知识目录。

## v2 架构

| 范围 | 唯一 owner |
| --- | --- |
| Public HTTP、认证、业务事务、durable jobs、SSE/WebSocket | Go `ascendanyd` |
| PostgreSQL migration | Go `ascendany-migrate` |
| 备份、校验与恢复演练 | Go `ascendany-backup` |
| 隔离 OJ 与 C++ LSP | Go `ascendany-judge`、`ascendany-lsp` |
| Web、Desktop、Mobile、Import Console、官网 | TypeScript |
| Pintia 数据采集 | TypeScript Manifest V3 浏览器插件 |
| 推荐模型制品校验与在线推理 | Go `ascendany-model`、`ascendanyd` |

生产 release build 接收一个外部训练完成的 immutable `ascendany.recommendation.inference-model.v1` 制品。Release builder 校验其精确 SHA-256、闭合 contract、feature schema、parameter digests 与 golden vectors。`ascendanyd` 将制品绑定为不可变数据库 model release，并在 Go 中执行在线推理。训练属于后续独立模块，不进入本仓库、生产 release、systemd unit、credential、HTTP API 或数据库 role。

## 数据边界

- v2 从空 PostgreSQL 数据库启动，不迁移旧账号或旧业务数据。
- 唯一考试输入格式为 `ascendany.pintia.snapshot.v2`。
- 浏览器插件从当前已登录的 Pintia 题目集读取官方页面接口，并导出一个完整 snapshot JSON。
- Import Console 以流式方式上传 snapshot；Go 后端执行 SHA-256、严格 schema/semantic validation、幂等入库、analytics generation 和 ordered SSE。
- 每个新 snapshot、analytics generation、recommendation model 与配置版本都保留不可变 provenance。

## 产品能力

- enrollment claim、登录、refresh、logout、profile、session 撤销与 role authorization；
- 五维能力、Rating 历史、成就、排行榜、考试列表与考试分析；
- durable AI chat、自动分析、笔记工具调用与审计；
- fresh/unavailable 推荐状态、学习路径、evidence、不可变模型 provenance 与知识详情；
- OJ run/submit 和 clangd LSP，执行进程不持有数据库凭据；
- Pintia v2 导入、任务历史、失败诊断与断线续传事件；
- 账号、学生、审计、prompt/model 配置、模型连接测试与推荐知识目录管理。

## 仓库入口

| 路径 | 内容 |
| --- | --- |
| `backend/` | Go 服务、worker、CLI、migration 与领域测试。 |
| `apps/web/` | 学生 Web 应用。 |
| `apps/desktop/` | Electron 学生端。 |
| `apps/mobile/` | Capacitor 移动端。 |
| `apps/import-console/` | 管理员控制台。 |
| `apps/site/` | 产品官网。 |
| `packages/sdk/` | 由最终 OpenAPI contract 生成的唯一 TypeScript SDK。 |
| `tools/pintia-exporter-extension/` | Pintia snapshot v2 Chrome 插件。 |
| `contracts/` | OpenAPI、Pintia snapshot v2 与外部推荐模型 contract/fixtures。 |
| `deploy/v2/` | systemd、权限、配置和生产验收 contract。 |
| `doc/重写v2架构与验收.md` | v2 ownership、数据流、清理范围和验收门槛。 |

## 本地验证

需要 Go 1.26、Node.js 22、pnpm 9.15.4，以及用于集成测试的 PostgreSQL 17。

```bash
cd backend
go test ./...
go vet ./...

cd ..
pnpm install --frozen-lockfile
pnpm --filter @ascendany/sdk check
pnpm --filter @ascendany/pintia-exporter check
pnpm --filter @ascendany/web check
pnpm --filter @ascendany/mobile check
pnpm --filter @ascendany/import-console check
pnpm --filter @ascendany/desktop test
pnpm --filter @ascendany/desktop build
```

### Rootless PostgreSQL 演练

一次性集成环境使用 digest-pinned PostgreSQL 17 镜像，以及 release lock 固定的
Fedora 44 x86_64 native PgBouncer 1.25.2 RPM。PgBouncer 的临时配置与 HBA 规则
直接派生自 production release，服务仅绑定 loopback。私有 auth file 保存 console
SCRAM verifier，以及 runtime、catalog publisher 两条精确明文密码；production 将后两条
记录持久化为 host-encrypted systemd credential，明文只出现在 PgBouncer 私有的 runtime
credential mount。客户端与 PostgreSQL backend 认证继续使用 `scram-sha-256`。该约定规避
PgBouncer 1.25.2 的 stored-verifier reconnect 回归；上游修复见
[PgBouncer #1504](https://github.com/pgbouncer/pgbouncer/pull/1504)。PostgreSQL 镜像缺失时
需要显式执行 pull；演练脚本不会拉取或启动 PgBouncer 镜像。
每次重置一次性 role password 后，integration runner 都会通过同目录 fsync 与 atomic
rename 发布精确的应用 identity 记录，并在显式执行 PgBouncer `RELOAD` 和 database
`RECONNECT` 的前后分别验证两种 identity。

```bash
tools/run-v2-postgres-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2
```

默认输入为脱敏的完整 Pintia fixture。使用真实导出文件时传入绝对路径；默认端口
`55432` 或 `56432` 被占用时，显式选择其他空闲 loopback 端口：

```bash
tools/run-v2-postgres-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2 \
  --snapshot /absolute/path/to/ascendany-pintia-snapshot.json \
  --direct-port 55433 \
  --pgbouncer-port 56433
```

确认值只授权重置本次新建的一次性 cluster。退出 trap 会终止 native PgBouncer
child，删除带有本次随机 label 的演练 Pod 和临时凭据目录，并校验既有 Podman
container 与 Pod 的 ID 保持不变。

独立的备份/恢复演练默认使用同一份已提交脱敏 snapshot，先通过 production Go
Pintia validator 校验，再执行真实的 create、verify、restore-verify、owner、ACL
与清理链路：

```bash
tools/run-v2-backup-restore-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2-backup-restore \
  --recommendation-model /absolute/canonical/recommendation-model.json \
  --recommendation-model-sha256 64_lowercase_hex
```

使用受保护的真实导出文件时传入
`--snapshot /absolute/canonical/snapshot.json`，无需修改脚本或仓库内容。模型参数必须
指向外部训练并经过 review 的制品；演练只验证、绑定模型并执行 Go inference，不执行训练。

release 到 restore 的完整验收入口、guarded 本地运行方式，以及独立且 fail-closed
的真实 Judge/LSP sandbox gate 见 [AscendAny v2 full E2E](doc/v2-full-e2e.md)。

生产结构、credential boundary 和 install/migrate/bootstrap/import/backup/restore 顺序见 [deploy/v2/README.md](deploy/v2/README.md)。完整验收定义见 [v2 重写架构与验收](doc/重写v2架构与验收.md)。
