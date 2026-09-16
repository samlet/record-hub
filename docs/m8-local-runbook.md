# Record Hub M8 本地运行与排障

这份 runbook 用于 fresh machine。普通停机保留 named volume；只有明确执行
`docker compose down --volumes` 才会删除本地数据。不要把 `.env.local`、client
secret、NATS credential 或 MongoDB password 提交到 Git。

## 1. 依赖

- Go 1.27、`curl`、`openssl`、`jq`；
- Docker Desktop/Engine，或已由 Homebrew/本机进程启动的 Dex、MongoDB replica set、NATS JetStream；
- 联合 gate 还需要 JDK/Maven 和 Fluxion Gradle wrapper。

先执行代码级门禁（不需要 Docker）：

```bash
make check
make m8-happy-path
make m8-failure-path
```

## 2. 启动本地依赖

### 2.1 Native 服务（推荐用于本机已有安装）

Docker 不是必需条件。先确认服务进程已经运行：

```bash
brew services list | grep -E 'mongodb|nats|postgres'
mongosh --quiet --host 127.0.0.1:27017 --eval 'rs.status().members.map(m => ({name:m.name,stateStr:m.stateStr}))'
nats --server nats://127.0.0.1:4222 stream ls
```

Homebrew 的 `mongodb-community@8.0` 默认使用单节点 replica set `rs0`。本机实例已有
数据时不要重命名或清理该 replica set；Record Hub native smoke 使用独立数据库名和以下
连接串：

```bash
export RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true'
export RECORD_HUB_NATS_URL='nats://127.0.0.1:4222'
# All three producers can emit metadata.workspaceId when these optional
# variables are set. Keep the value aligned with the target Record Hub
# workspace (or use different values when producers publish to different workspaces).
export APPROVER_RECORD_HUB_WORKSPACE_ID='workspace-local'
export FLUXION_RECORD_HUB_WORKSPACE_ID='workspace-local'
export RECORD_HUB_WORKSPACE_ID='workspace-local'
# Legacy events without metadata.workspaceId still need an explicit fallback
# or tenant map while they drain from the outboxes.
export RECORD_HUB_PROJECTION_WORKSPACE_ID='workspace-local'
# Optional and preferred when multiple tenants share one projector:
# export RECORD_HUB_PROJECTION_WORKSPACE_MAP='{"tenant-a":"workspace-a","tenant-b":"workspace-b"}'
```

直接运行 native 依赖/API smoke（不会启动、停止或删除本机服务）：

```bash
make m8-native-smoke
```

该命令验证 Mongo transaction/unique-index/CAS/change-stream、NATS JetStream topology、
Record Hub API health/metrics 以及 API 进程对 native repositories 的启动接线。完整的
worker 投影可用下方 `all` 模式命令验证。Dex/Web smoke 需要与当前运行 Dex 的 issuer、client 和
redirect URI 完全匹配时才启用：

```bash
RECORD_HUB_M8_DEX_LIVE=1 \
RECORD_HUB_WEB_ENABLED=true \
RECORD_HUB_WEB_ISSUER='http://127.0.0.1:5556/dex' \
RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT='http://127.0.0.1:5556/dex/auth' \
RECORD_HUB_WEB_TOKEN_ENDPOINT='http://127.0.0.1:5556/dex/token' \
RECORD_HUB_WEB_AUDIENCE='record-hub-web-local' \
RECORD_HUB_WEB_CLIENT_ID='record-hub-web-local' \
RECORD_HUB_WEB_CLIENT_SECRET='<local-secret>' \
RECORD_HUB_WEB_REDIRECT_URL='http://127.0.0.1:18080/auth/callback' \
RECORD_HUB_M8_LOCAL_LIVE=1 RECORD_HUB_M8_RUNTIME=native \
./scripts/verify-m8-local.sh
```

如果当前 Dex 是其他项目的配置（例如 issuer 没有 `/dex` 或没有 Record Hub client），
不要把失败归因于 Mongo/NATS；应启动独立的 Record Hub Dex 配置或仅运行不带 Web 的
native smoke。

### 2.2 Docker 服务

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
# API 使用真实 Mongo repositories；如需在同一进程消费 projection，再设置 NATS 和
# workspace fallback，并将 mode 改为 all。
export RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true'
export RECORD_HUB_MONGODB_DATABASE=record_hub
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

单独启动 projection worker（API 与 worker 也可以用 `RECORD_HUB_MODE=all` 合并）：

```bash
export RECORD_HUB_MODE=worker
export RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true'
export RECORD_HUB_MONGODB_DATABASE=record_hub
export RECORD_HUB_NATS_URL='nats://127.0.0.1:4222'
export RECORD_HUB_PROJECTION_WORKSPACE_ID=workspace-local
./build/record-hub serve
```

启动三个 producer 时分别设置对应的 workspace 变量：

```bash
export APPROVER_RECORD_HUB_WORKSPACE_ID=workspace-local
export FLUXION_RECORD_HUB_WORKSPACE_ID=workspace-local
export RECORD_HUB_WORKSPACE_ID=workspace-local
```

它们只影响新写入的 summary envelope `metadata.workspaceId`，不改变业务系统的
Temporal/Conductor workflow 或本地租户字段。未设置时保持旧行为，由 Record Hub 的
tenant map 或显式 fallback 兼容历史事件。

worker 只接受三种已注册的 summary event；workspace scope 按
`metadata.workspaceId` → `RECORD_HUB_PROJECTION_WORKSPACE_MAP` →
`RECORD_HUB_PROJECTION_WORKSPACE_ID` 的顺序解析。三者都缺失的事件不会写入 Mongo，而是
按确定性错误重投并最终进入 `dlq.record-hub.<consumer>`。

三 producer 的 live runtime gate（会生成带时间租户名的本地测试记录，不会删除现有业务
数据）可执行：

```bash
make m5-runtime-smoke
```

监督式启动三个 producer relay 并验证真实 Outbox → JetStream → projection 链路（需要
本机 PostgreSQL、Conductor、MongoDB、NATS 和可选 MinIO；脚本只创建临时 producer 数据库）：

```bash
RECORD_HUB_M5_SUPERVISED_LIVE=1 make m5-supervised-live
```

该 gate 启动 Approver API、Fluxion API、Bids worker/API 与 Record Hub all-mode。默认
`RECORD_HUB_M5_SUPERVISED_API_MODE=auto` 会优先调用业务 API（Approver 创建临时流程
application、Fluxion 创建 customer/project、Bids 完成 project approval 并创建 tender），
随后等待各自事务 Outbox 经 relay 发布并落成 projection。Bids API 只有在本机 MinIO
healthz 可用时才启动；缺少可选依赖时 auto 模式才回退到受控 Outbox seed。设置
`RECORD_HUB_M5_SUPERVISED_API_MODE=required` 可禁止回退，设置为 `off` 可显式复现
direct-outbox gate。该命令覆盖业务 HTTP API，不等于完整浏览器 UI E2E；成功后脚本自动
停止自己启动的进程并删除临时数据库。

验证 Record Hub 自身重启恢复（事件会在进程停止期间留在 JetStream，并在重启后按原
event ID 幂等处理；脚本只会创建带时间租户名的测试记录）：

```bash
make m7-native-restart
```

该 gate 会先投影版本 1，停止并重新启动 Record Hub，再投影版本 2（重复发送一次），
最后检查 Mongo 中记录、checkpoint、Inbox 和 audit 均无重复且状态为 `CURRENT`。它不
停止 MongoDB/NATS，也不覆盖已有租户数据。

验证 NATS outage/recovery（脚本会在临时端口启动并停止一个隔离的 NATS JetStream，
不会触碰本机 `:4222` 服务）：

```bash
make m7-native-nats-recovery
```

该 gate 会确认 NATS 不可用时 Record Hub 进程仍存活、`/healthz` 保持可用而 `/readyz`
正确降级为 503；NATS 用同一 JetStream store 恢复后，Record Hub 自动重连并完成重复
事件的单次投影。三个业务系统自身的 Outbox 积压恢复仍需在联合拓扑中验收。

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
