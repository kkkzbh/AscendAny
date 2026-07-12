# AscendAny

[English](README.md) | 简体中文

<p align="center">
  <img src="image/LOGO_SHRIND.png" alt="AscendAny Logo" width="96" />
</p>

<p align="center">
  <strong>学生能力分析平台</strong>
</p>

AscendAny 把 Pintia 程序设计题目集的完整快照转换为可追溯的能力画像、Rating、成就、排行榜、考试分析和个性化学习建议。Web、Desktop 和 Mobile 共享账号、画像、考试、AI 对话与推荐等核心学生能力；各客户端按平台提供适配交互。管理员通过独立 Import Console 管理导入、账号、配置、审计与模型训练任务。

## v2 架构

| 范围 | 唯一 owner |
| --- | --- |
| Public HTTP、认证、业务事务、durable jobs、SSE/WebSocket | Go `ascendanyd` |
| PostgreSQL migration | Go `ascendany-migrate` |
| 备份、校验与恢复演练 | Go `ascendany-backup` |
| 隔离 OJ 与 C++ LSP | Go `ascendany-judge`、`ascendany-lsp` |
| Web、Desktop、Mobile、Import Console、官网 | TypeScript |
| Pintia 数据采集 | TypeScript Manifest V3 浏览器插件 |
| 推荐模型编排 | Go `ascendany-trainer-agent` |
| 推荐模型训练实现 | 隔离 Python trainer |

在线 runtime 不依赖 Python。Python trainer 只读取 Go 生成的 immutable training bundle，并在无网络、无数据库凭据的子进程中写出一个受限 output bundle。

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
- fresh/stale/unavailable 推荐状态、学习路径与知识详情；
- OJ run/submit 和 clangd LSP，执行进程不持有数据库凭据；
- Pintia v2 导入、任务历史、失败诊断与断线续传事件；
- 账号、学生、审计、prompt/model 配置、模型连接测试与训练任务管理。

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
| `trainers/recommendation/` | 唯一允许保留 Python 的隔离训练器。 |
| `contracts/` | OpenAPI 与 Pintia snapshot v2 contract/fixtures。 |
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

.venv/bin/python -m unittest discover -s trainers/recommendation/tests -v
```

### Rootless PostgreSQL 演练

一次性集成环境使用 digest-pinned PostgreSQL 17 镜像，以及 release lock 固定的
Fedora 44 x86_64 native PgBouncer 1.25.2 RPM。PgBouncer 的临时配置与 HBA 规则
直接派生自 production release，私有 runtime tree 只保存 SCRAM verifier，服务仅
绑定 loopback。PostgreSQL 镜像缺失时需要显式执行 pull；演练脚本不会拉取或启动
PgBouncer 镜像。
每次重置一次性 role password 后，integration runner 都会通过同目录 fsync 与
atomic rename 发布精确的 admin/legacy/runtime SCRAM verifier 集合，再显式执行 PgBouncer
`RELOAD` 和 database `RECONNECT`。

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
  --confirm-reset drop-disposable-ascendany-v2-backup-restore
```

使用受保护的真实导出文件时传入
`--snapshot /absolute/canonical/snapshot.json`，无需修改脚本或仓库内容。

release 到 restore 的完整验收入口、guarded 本地运行方式，以及独立且 fail-closed
的真实 Judge/LSP sandbox gate 见 [AscendAny v2 full E2E](doc/v2-full-e2e.md)。

生产结构、credential boundary 和 install/migrate/bootstrap/import/backup/restore 顺序见 [deploy/v2/README.md](deploy/v2/README.md)。完整验收定义见 [v2 重写架构与验收](doc/重写v2架构与验收.md)。
