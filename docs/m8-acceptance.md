# M8-080 Web/OIDC 验收

本批完成 Record Hub Web/BFF 的认证边界。实现位于
`server/internal/web`，API 进程在 `RECORD_HUB_WEB_ENABLED=true` 时挂载；默认
关闭，因此 API-only/worker 部署不会意外暴露登录入口。

## 已实现

- `GET /auth/login`：Authorization Code + S256 PKCE，随机 `state`、`nonce`
  和 verifier；授权 URL 只使用显式配置的 client、redirect 和 scope。
- `GET /auth/callback`：校验签名 state cookie、请求 state、有效期和一次性
  pending state；code 交给服务端 exchanger，并要求 ID-token verifier 校验
  nonce、签名、issuer、audience、expiry 和 subject。
- `POST /auth/logout`：必须通过 CSRF 校验，清除 HttpOnly session cookie；可选
  的 post-logout 路径只能是同源相对路径。
- `GET /auth/session`：只返回当前已验证的稳定身份 `(iss, sub)`，不返回 access
  token 或 refresh token。
- Session 使用 HMAC-SHA256 签名的短期 cookie，只保存 user principal 的稳定
  标识和 audience。cookie 默认 `HttpOnly; SameSite=Lax; Path=/`，生产必须打开
  `RECORD_HUB_WEB_SECURE_COOKIES`。
- CSRF 使用签名 double-submit cookie；所有 POST/PUT/PATCH/DELETE 需要
  `X-CSRF-Token`，并在浏览器提供 `Origin`/`Referer` 时执行同源校验。
- pending OIDC state 使用有界、一次性内存 store。多实例部署前必须替换为共享
  短期 store（例如 Redis）；不能依赖实例粘滞来掩盖重启丢失。

M8-081 的资源路由组合也已完成：`NewResourceRouter` 把现有 records 和 schema
HTTP handlers 收敛到单一 `/api/v1` 浏览器入口，`/api/v1/schemas` 及其子路径只
能进入 schema handler，其余资源进入 records handler；相似前缀不会误分流，缺失
依赖 fail closed 为 503。路由本身不实现第二套授权，handler 收到的 principal
仍来自同一 Session middleware，OWNER/EDITOR/VIEWER allow/deny 仍由本地
membership authorizer 决定。`web/` 已补上 Next.js 控制台基础：同源 `/api`/`/auth`
rewrite、Dex 登录入口、tenant/workspace/table 选择、自定义表和记录写入、Schema
草稿/ETag 发布、View 多条件过滤/排序/列显隐、投影运维摘要与只读网格；所有浏览器
写操作自动带 CSRF、幂等键和请求 ID。Schema 读取也提供 workspace-scoped
`GET /api/v1/schemas/{schemaId}`。
真实 Dex 回调、多角色和投影数据的浏览器矩阵仍保持 PARTIAL。

M8-082 的 Operations 控制台边界也已完成：`NewConsoleRouter` 把
`/operations/events` 页面及 `/api/v1/operations/events` API 与资源路由组合，
页面只允许选择 Approver、Fluxion、Bids 三个已注册 projection consumer。服务端
同时校验 consumer、tenant/workspace 和每个 checkpoint 的 scope；freshness、GAP、
failed/rejected/DLQ 只以有界计数和 checkpoint 元数据展示，原始 inbox/event
payload 永不出现在页面或响应中。Next.js Operations 页进入后立即读取并每 10 秒
轮询，按生成时间标记 Fresh/Lagging/Stale。

M8-083 的跨仓库 happy-path gate 已完成，入口为 `make m8-happy-path`。它会重复
执行 Record Hub 的 Web/identity/schema/records/projection 测试，并执行 Approver、
Fluxion、Bids 的 summary contract、Outbox、Binding/diagnostic workflow/worker
测试。依赖不可用时命令仍能给出确定的代码级结果；真实浏览器/API + MongoDB +
NATS + Dex + Temporal/Conductor 联合路径必须由 M8-085 runbook 运行，不能由此
gate 冒充 live E2E。

M8-084 的 failure-path gate 已完成，入口为 `make m8-failure-path`。它覆盖坏 state、
CSRF、伪造 Session、nonce/issuer/audience/expiry、未知 projection consumer/scope、
gap/DLQ、Outbox ACK-loss 和两种 workflow retry/failure 测试；依赖故障注入仍需
M8-085/086 的受监督拓扑。

M8-085 runbook 位于 [m8-local-runbook.md](m8-local-runbook.md)，`make m8-local-smoke`
默认执行全部代码级 gate；设置 `RECORD_HUB_M8_LOCAL_LIVE=1` 后执行依赖 smoke。默认
runtime 为 Docker，设置 `RECORD_HUB_M8_RUNTIME=native` 会连接已经运行的本机
Dex/MongoDB/NATS 并探测 Go API，不启动或停止这些进程。

M8-086 恢复与轮换步骤位于 [m8-recovery-runbook.md](m8-recovery-runbook.md)，明确
GAP、DLQ、ACK loss、NATS/Mongo/Dex 和 Session secret 的边界；DLQ 摘要不含 payload，
恢复只能回到 source Outbox/原 subject 使用原 event ID。真实故障注入和滚动轮换仍保持
`PARTIAL`，直到受监督拓扑完成。

M8-087 汇总报告位于 [m8-acceptance-report.md](m8-acceptance-report.md)，包含四仓库
commit/hash、可重复 gate、live 限制和遗留风险；因此 M8-087 也保持 `PARTIAL`。

## 本地启动

先启动 Dex 并生成本地 secret：

```bash
make dex-env
make dex-up
```

复制 `.env.example` 中 Web 部分到未跟踪的环境文件，填入
`deploy/local/dex/.env.local` 的 `DEX_RECORD_HUB_WEB_SECRET`，并设置：

```bash
export RECORD_HUB_WEB_ENABLED=true
export RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS=true
export RECORD_HUB_WEB_SECURE_COOKIES=false
```

生产环境必须使用 HTTPS issuer、token/authorization endpoint、HTTPS redirect
URI、Secret Manager 中的至少 32 字节 session secret，并设置
`RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS=false` 和
`RECORD_HUB_WEB_SECURE_COOKIES=true`。

## 自动化证据

```bash
go test ./server/internal/web ./server/internal/modules/identity ./server/internal/config
```

测试覆盖 PKCE challenge、state/nonce 绑定、pending state replay、session cookie
flags、CSRF 负向/正向路径、logout 清 cookie、token endpoint form 和 nonce verifier
强制。真实 Dex 浏览器路径仍需使用与应用 issuer/client/redirect 完全匹配的本机或
Docker 配置运行 M8-083/084 联合 E2E；native Mongo/NATS smoke 不能替代真实 Dex 登录
和浏览器矩阵。
