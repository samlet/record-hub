# ADR-0003：分离 Human Identity 与 Workload Identity

- 状态：Accepted
- 日期：2026-09-17
- 关联需求：P2-SEC-001..004、RH-M1-014、RH-M6-067

## 背景

Record Hub 同时处理浏览器用户和 Approver、Fluxion、Bids、Temporal/Conductor Worker 的
服务请求。两类主体的认证流程、凭据生命周期和授权语义不同：

- 用户需要 Authorization Code + PKCE、state/nonce、Session、退出和上游 IdP；
- workload 需要非交互短期凭据、精确 audience/scope、独立撤销与滚动轮换；
- service principal 不得继承用户 membership/group，也不得使用 BFF、NATS 或 MongoDB 凭据；
- Binding policy 还必须约束 tenant、workspace、purpose、resource system/type。

本机安装的是 Dex 2.45.1。Dex 的 `client_credentials` 实现于 2026-03-03 合并到 master，
晚于该稳定版；当前在线文档已经描述该能力，但不能据此假设 2.45.1 含有实现。对应上游
[PR #4583](https://github.com/dexidp/dex/pull/4583)也显示该能力是 opt-in，并且其 token
claim 形态不等同于 Record Hub 已冻结的共享 resource audience + 独立 workload subject。

## 决策

### 1. 分离信任边界

- Dex 继续作为 **human issuer**，只服务浏览器 Authorization Code + PKCE。
- workload 使用独立 issuer。Record Hub 通过标准 OIDC discovery/JWKS 验证其 RS256 JWT，
  不依赖具体厂商管理 API。
- 一个 Record Hub API deployment 当前只接受一个 workload issuer 和一个精确 resource
  audience；浏览器 Session 使用独立 Web OIDC 配置，不与之复用。

### 2. 冻结 workload token contract

| Claim/属性 | 约束 |
| --- | --- |
| `iss` | 精确等于配置的 workload issuer；本地可显式允许 HTTP，其他环境必须 HTTPS |
| `sub` | 稳定 workload client identity，例如 `fluxion-to-record-hub`；不得使用 email/name |
| `aud` | 必须包含 Record Hub resource audience，例如 `record-hub-api` |
| `exp`/`iat` | 必须存在；Beta 目标 TTL 不超过 15 分钟，本地 fixture 为 5 分钟 |
| `scope` | 空格分隔的最小 capability；Binding 首个 scope 为 `recordhub.binding.snapshot` |
| signature | RS256，`kid` 必须能从 issuer JWKS 解析；未知 key 时刷新，失败后 fail closed |

认证成功只建立 `PrincipalService`。授权仍由本地 exact allowlist 完成：

```text
(iss, sub, aud, scope, tenant, workspace, purpose, resourceSystem, resourceType)
```

所有字段非空且不支持 wildcard。token scope 不能替代业务 policy；业务 policy 也不能放宽
token audience/scope。

### 3. 本地与 CI issuer

仓库提供 `tools/workload-issuer`，用于隔离的本地/CI 验收。它只支持：

- `client_secret_basic` + `client_credentials`；
- 启动时生成的临时 RSA key；
- 启动时注入、至少 32 字节且彼此独立的 client secret；
- 精确 audience、client allowlist 和 scope allowlist；
- 5 分钟 access token、OIDC discovery 和 JWKS。

该工具不持久化 key/secret，不提供管理面、撤销、HA、审计或 HSM/KMS 集成，因此 **禁止用于
共享、Beta 或生产部署**。它的作用是让 token contract、错误凭据、越权 scope、JWKS 和
Record Hub verifier 在无 Docker、无外部授权服务器时仍可做 live 验收。

### 4. Beta 与生产部署

- Beta 必须接入正式、独立的 OAuth 2.0/OIDC Authorization Server，支持短期
  client-credentials token、共享 resource audience、独立 client、JWKS rotation 和撤销；
- 产品选择保持可替换，验收以本 ADR 的 token contract 为准，而不是依赖厂商私有 claims；
- 若将来 Dex 稳定版的实际 token fixture 满足本 contract，可作为 Beta 候选，但不能仅凭
  文档或版本号切换；
- Kubernetes/多主机生产阶段评估 SPIFFE/SPIRE。SPIFFE Workload API 可签发短期 SVID，
  JWT-SVID 也要求调用方指定 audience；是否采用它由部署平台 ADR 决定；
- 禁止 password grant、长期自签 JWT、共享 API key 和浏览器 Session 充当 workload identity。

## 运行时配置

```text
RECORD_HUB_OIDC_ISSUER
RECORD_HUB_OIDC_AUDIENCE
RECORD_HUB_OIDC_PRINCIPAL_KIND=service
RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER=false
RECORD_HUB_BINDING_MACHINE_POLICIES=[...]
```

`RECORD_HUB_BINDING_MACHINE_POLICIES` 是有界 JSON array。启动时拒绝未知字段、空值、
wildcard、重复 policy、issuer/audience 不匹配和超过 128 项的配置。

## 验收

```bash
make p2-workload-identity-smoke
```

该 gate 使用专用默认端口 `15557`，发现端口已被占用时直接失败，不停止或复用其他项目服务；
也可通过 `RECORD_HUB_WORKLOAD_ISSUER_PORT` 选择另一隔离端口。它验证两个独立 client 的
discovery、签名、issuer、subject、audience、TTL、scope，以及错误 secret/scope 的负向路径。

## 后果

- 好处：人类与服务凭据职责清晰；Dex 版本不再阻塞 Binding；授权维度可审计且 fail closed。
- 代价：Beta 需要多运维一个 workload issuer，并管理 client lifecycle 与双 key/secret 轮换。
- 限制：本 ADR 不选择生产厂商，也不等于已完成 rotation/revocation live 验收。

## 参考

- [Dex OAuth2 grants](https://dexidp.io/docs/configuration/oauth2/)
- [Dex client credentials PR #4583](https://github.com/dexidp/dex/pull/4583)
- [OAuth 2.0 Client Credentials Grant, RFC 6749 §4.4](https://www.rfc-editor.org/rfc/rfc6749#section-4.4)
- [SPIFFE concepts](https://spiffe.io/docs/latest/spiffe/concepts/)
- [SPIFFE Workload API](https://spiffe.io/docs/latest/spiffe-specs/spiffe_workload_api/)

