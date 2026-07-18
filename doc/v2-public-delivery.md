# AscendAny v2 public delivery contract

本文定义 `ascendany.kkkzbh.cn` 的唯一 public delivery path。Cloudflare 只承担 edge 与 Tunnel transport；Go `ascendanyd` 是 km6 的唯一 HTTP origin，并交付由 TypeScript 构建的 immutable static assets。

## Ownership 与 topology

```mermaid
flowchart LR
  B["Browser / Desktop / Mobile"] --> C["Cloudflare edge"]
  C --> T["locally managed Cloudflare Tunnel"]
  T -->|"HTTP/1.1 to 127.0.0.1:18000"| G["Go ascendanyd"]
  G -->|"/api/v2, health, SSE, WebSocket"| A["Go API handler"]
  G -->|"/, /app/, /admin/"| S["verified embedded TypeScript assets"]
```

| Public path | Owner | Behavior |
| --- | --- | --- |
| `/api/v1/*` | Go Agent frontend API | 原 Agent frontend 的 HTTP 与 SSE contract |
| `/api/v2` 与 `/api/v2/*` | Go API | importer、operator、HTTP、SSE 与 LSP WebSocket |
| `/livez`、`/readyz`、`/version` | Go API | public health 与 release identity |
| `/` 与 site assets | TypeScript site bytes，由 Go 交付 | product site；无 SPA fallback |
| `/app/` | TypeScript Agent frontend bytes，由 Go 交付 | 原 desktop Agent Web build；HTML navigation 才允许 app index fallback |
| `/admin/` | TypeScript import console bytes，由 Go 交付 | BrowserRouter basename `/admin`；HTML navigation 才允许 admin index fallback |

`/app` 与 `/admin` 永久重定向到带尾部 `/` 的 canonical route。Static handler 拒绝 non-canonical/encoded/traversal/duplicate-slash path、未知 asset extension 与 mutation method。SPA fallback 不处理带 extension 的 missing asset，也不处理缺少 `Accept: text/html` 的请求。API、health、SSE 与 WebSocket 在 static validation 前进入 Go handler。

## Same-release build closure

三个 Vite source 使用固定 base：`@ascendany/site` → `/`、`@ascendany/desktop` 的 Agent Web build → `/app/`、`@ascendany/import-console` → `/admin/`。`apps/web` 不进入 production public asset closure。

```bash
pnpm public-assets:generate
pnpm public-assets:check
```

生成器清除 ambient `VITE_*` 与 `NODE_OPTIONS`，重建三个 package，将 closed output tree 写入 `backend/internal/publicdelivery/assets/`，并生成 `ascendany.public-assets.v1` manifest。Manifest 固定每个 regular file 的 relative path、byte size、SHA-256 与 cache class；entry-set 完整、唯一并 byte-sorted。`ascendanyd` 通过 `go:embed` 编入这些 bytes，启动时重新验证 manifest 与 digest。最终 server binary SHA-256 将 static bytes 纳入同一个 release identity。

生成器拒绝 inline script/style、event attribute、外部或缺失的 active resource、symlink、特殊文件、未知 extension、超限文件/总量/数量和 manifest drift。`--write` 与 `--check` 共享 kernel advisory lock。

## HTTP security 与 caching

- Hashed Vite assets：`Cache-Control: public, max-age=31536000, immutable` 与 content SHA-256 ETag。
- HTML、favicon、release metadata：`Cache-Control: no-cache`。
- Closed extension→`Content-Type` mapping，并设置 `nosniff`。
- Static responses 设置 CSP、frame denial、COOP、Permissions Policy 与 Referrer Policy；app/admin `connect-src` 只允许同源。
- Site 只读取固定 AscendAny GitHub Releases endpoint；请求失败时明确显示 metadata unavailable，下载按钮保持关闭。
- API response headers 由 Go handler 独占，static wrapper 不改写 CORS、SSE、WebSocket 或 API cache policy。

## Cloudflare edge contract

Production acceptance 必须证明：

1. `ascendany.kkkzbh.cn` 是唯一 public application hostname；`ascendany-v2.kkkzbh.cn` 是 acceptance-only shadow hostname。两者 origin 均为 `http://127.0.0.1:18000`。
2. Locally managed ingress 只有 ordered public、shadow 与 global `http_status:404` 三条规则。Retired trainer hostname和path route均不存在。
3. Hostname 没有 overlapping Worker route，WebSocket 在 zone 上启用；cutover operator 保存 Cloudflare zone read evidence。
4. Tunnel 保留 HTTP/1.1 chunked transfer；Go SSE response 使用 `text/event-stream`。
5. Connector 固定为 release lock 中的 signed Cloudflared RPM；native systemd unit 使用 `DynamicUser`、empty capabilities、seccomp、`NoNewPrivileges`、closed address families 和 encrypted Tunnel-scoped JSON credential。
6. `ascendanyd` 只信任 `127.0.0.1/32` proxy，并只从 `CF-Connecting-IP` 读取 client IP。
7. Independent public E2E覆盖 site、app/admin deep route、health/version、authorized API read、SSE reconnect 与 LSP WebSocket attach。

Cloudflare 版本、RPM、signer、file manifest 与 binary digest 由 `deploy/v2/config/fedora-runtime-packages.json` 和 production validator 固定。可执行 DNS transition 与 acceptance sequence 只位于 `deploy/v2/README.md`。

## Cutover 与 rollback

Read-only smoke drop-in 在 DNS 指向 v2 时关闭全部 mutation。Public 与 shadow `/version` bytes、loopback readiness、static/API/SSE/WebSocket smoke 通过后移除 drop-in并启用 service；该动作是 write-activation commit point。

Write activation 前可以停止 v2并恢复先前 public DNS route。Write activation 后只允许 v2 roll-forward。Model rollback通过新 reviewed release携带选定的已训练 artifact，并产生新 activation event；禁止修改已安装 release bytes。
