# Record Hub M8 本地运行与排障

这份 runbook 用于 fresh machine。普通停机保留 named volume；只有明确执行
`docker compose down --volumes` 才会删除本地数据。不要把 `.env.local`、client
secret、NATS credential 或 MongoDB password 提交到 Git。

## 1. 依赖

- Go 1.27、`curl`、`openssl`、`jq`；
- Docker Desktop/Engine（运行 Dex、MongoDB replica set、NATS JetStream）；
- 联合 gate 还需要 JDK/Maven 和 Fluxion Gradle wrapper。

先执行代码级门禁（不需要 Docker）：

```bash
make check
make m8-happy-path
make m8-failure-path
```

## 2. 启动本地依赖

```bash
make dex-env
make dex-up

export MONGODB_ROOT_USERNAME=record_hub_root
export MONGODB_ROOT_PASSWORD='<random-local-password>'
export RECORD_HUB_MONGODB_USERNAME=record_hub
export RECORD_HUB_MONGODB_PASSWORD='<different-random-local-password>'
make mongo-up

export NATS_ADMIN_PASSWORD='<random-local-password>'
export NATS_APPROVER_PASSWORD='<different-random-local-password>'
export NATS_FLUXION_PASSWORD='<different-random-local-password>'
export NATS_BIDS_PASSWORD='<different-random-local-password>'
export NATS_RECORD_HUB_PASSWORD='<different-random-local-password>'
make nats-up
export RECORD_HUB_NATS_URL='nats://record-hub-admin:<url-encoded-password>@127.0.0.1:4222'
make nats-init
```

Dex issuer 是 `http://127.0.0.1:5556/dex`。四个系统各自使用独立 confidential
client；Record Hub client 的精确 callback 是 `:3004/auth/callback`（独立 Next
BFF）或 `:8080/auth/callback`（本仓库 Go BFF），只能启用实际拥有 callback 的一条。

## 3. 启动 Go BFF 路由

Record Hub Web 路由默认关闭。启用时把
`deploy/local/dex/.env.local` 中的 `DEX_RECORD_HUB_WEB_SECRET` 作为 client
secret；不要 source 整个文件，因为 bcrypt hash 含 `$`：

```bash
export RECORD_HUB_MODE=api
export RECORD_HUB_HTTP_ADDRESS=127.0.0.1:8080
export RECORD_HUB_WEB_ENABLED=true
export RECORD_HUB_WEB_ISSUER=http://127.0.0.1:5556/dex
export RECORD_HUB_WEB_AUDIENCE=record-hub-web-local
export RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT=http://127.0.0.1:5556/dex/auth
export RECORD_HUB_WEB_TOKEN_ENDPOINT=http://127.0.0.1:5556/dex/token
export RECORD_HUB_WEB_CLIENT_ID=record-hub-web-local
export RECORD_HUB_WEB_CLIENT_SECRET="$(sed -n 's/^DEX_RECORD_HUB_WEB_SECRET=//p' deploy/local/dex/.env.local)"
export RECORD_HUB_WEB_REDIRECT_URL=http://127.0.0.1:8080/auth/callback
export RECORD_HUB_WEB_SESSION_SECRET="$(openssl rand -hex 32)"
export RECORD_HUB_WEB_SECURE_COOKIES=false
export RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS=true
./build/record-hub serve
```

另一个终端验证：

```bash
curl -f http://127.0.0.1:8080/healthz
curl -f http://127.0.0.1:8080/metrics | grep record_hub_http_requests_total
curl -i http://127.0.0.1:8080/auth/login
```

浏览器完成 Dex 登录后，callback 会建立 `HttpOnly; SameSite=Lax` session。生产必须
使用 HTTPS endpoint/redirect、`RECORD_HUB_WEB_SECURE_COOKIES=true`，并关闭
`RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS`。

也可以运行自动 smoke（会停止本次启动的服务，但不删除 volume）：

```bash
RECORD_HUB_M8_LOCAL_LIVE=1 ./scripts/verify-m8-local.sh
```

## 4. 停止与排障

```bash
make nats-down
make mongo-down
make dex-down
```

- Docker socket 不可用：先运行 `make check && make m8-happy-path && make m8-failure-path`；这些结果只能作为代码级证据，不能标成 live 依赖验收。
- `redirect_uri` 被 Dex 拒绝：确认 callback 与 Dex config 的一条精确 URI 完全一致（scheme、host、port、path 均不能变）。
- callback 报 state invalid：不要重复使用旧 callback；pending state 是一次性的，API 重启也会使进行中的登录失效，重新访问 `/auth/login`。
- cookie 没有回传：本地 HTTP 必须保持 `WEB_SECURE_COOKIES=false`；HTTPS 部署必须反过来设为 true。
- logout 返回 403：先通过 GET 页面拿 CSRF cookie，再使用 `X-CSRF-Token` header；跨源 `Origin` 会被拒绝。
- `/readyz` 仍为 503：当前二进制在依赖适配器尚未接入时会 fail closed；检查依赖拓扑和相应 M8-086 runbook，不要用 `/healthz` 代替 readiness。
- `make dex-env` 提示文件已存在：删除该未跟踪文件后再执行，用于明确轮换本地凭据；不要在生产复用这些值。
