# AscendAny v2 - Agent Instructions

## Runtime ownership

- `backend/` 是唯一在线后端、业务规则、事务、durable job、SSE/WebSocket 与 migration 实现，语言为 Go。
- `apps/web/`、`apps/desktop/`、`apps/mobile/`、`apps/import-console/`、`apps/site/` 与 Pintia exporter 使用 TypeScript strict mode。
- `packages/sdk/` 必须由 `contracts/openapi/ascendany-v2.yaml` 生成；first-party app 禁止手写 endpoint string 和重复 DTO。
- Production repository 与 deploy release 禁止 Python source、Python runtime、trainer process、trainer credential、training API 和 training database role。模型训练由后续独立模块负责。
- Production build 只接收已训练的 immutable `ascendany.recommendation.inference-model.v1` artifact；Go 负责 strict verification、immutable release binding 与 online inference。

## Fresh data boundary

- v2 从独立空数据库 `ascendany_v2` 启动，不迁移旧账号、session、业务数据或旧导入格式。
- 唯一允许的考试输入是 `ascendany.pintia.snapshot.v2`。
- 数据源由 TypeScript Manifest V3 插件从用户当前登录的 Pintia 题目集页面采集；Go import worker 负责 strict schema/semantic validation。
- Logical exam key 为 `(platform, problemSetId)`。相同 bytes、相同 typed domain content 和新 snapshot 必须分别具备确定的幂等语义。
- snapshot、analytics generation、recommendation model、配置版本与事件都保存 immutable provenance。

## Database and durability

- PostgreSQL 17 只部署在 km6。在线应用默认通过 6432 的 PgBouncer transaction mode 访问；migrate、backup 与 restore verification 直连 5432。
- 连接参数必须来自配置。Database URL 禁止包含密码；密码只通过 systemd credential file path 传递。
- migration 由 Go binary 内嵌固定 manifest 与 SHA-256，必须保持版本连续并拒绝 drift。
- 单个 snapshot import 是一个事务；analytics publish 必须验证完整 input manifest 并 CAS 当前 heads。
- artifact publish 使用 fsync、SHA-256、per-hash lock 与 atomic rename；backup/restore 必须验证数据库、artifact entry-set、size、mode 与 checksum。

## Security

- `ascendanyd`、migrator、backup、judge 与 LSP 使用独立 OS/数据库 capability identity。
- Judge 与 LSP 不接收数据库 credential，且没有 network fallback。
- Unknown fields、重复 identity、dangling reference、partial pagination、hash/count mismatch 和超限输入直接失败。
- 禁止提交 plaintext secret、`.env.local`、token、password 或 API key。
- 禁止加入 compatibility path、legacy parser、静默 fallback 或 host code execution。

## Engineering and verification

- Go 改动必须补测试；执行 `go test ./...`、`go vet ./...`，高并发/ownership 代码还需要 race test 与 PostgreSQL 17 integration test。
- 大型 Go/Node build 使用 guarded heavy-run，避免无界并发和全局 OOM。
- TypeScript app 改动必须通过对应 package 的 tests、strict typecheck 与 production build。
- OpenAPI 改动后执行 `pnpm --filter @ascendany/sdk generate` 和 `pnpm --filter @ascendany/sdk check`。
- Pintia contract 改动必须覆盖 JSON Schema、semantic negative fixtures、domain hash 与真实形状脱敏 fixture。
- 仓库 policy scan 必须证明 production tree、release manifest、systemd units、scripts 与 runtime closure 没有 Python 或 trainer execution path。

使用 `ssh km6` 连接生产服务器。唯一生产部署入口和 acceptance sequence 位于 `deploy/v2/README.md`；架构与最终验收边界位于 `doc/重写v2架构与验收.md`。
