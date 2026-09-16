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
membership authorizer 决定。

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
强制。真实 Dex 浏览器路径仍需在 Docker 可用时运行 M8-083/084 联合 E2E；本机若
没有 Docker，不能把 fake issuer 测试当成 live Dex 证据。
