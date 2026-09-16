# 认证、授权与租户隔离

## 1. 结论

本地开发和联合测试可以使用一套本机 Dex，为 Approver、Fluxion、Bids 和 Record Hub 提供统一 OIDC issuer。

Dex 承担身份认证和 token 签发；各系统仍负责自己的租户映射、角色、资源和字段授权。Dex group 只能作为角色映射输入，不能成为唯一授权事实。

## 2. 人类用户登录

每个 Web/BFF 注册独立 confidential client，使用 Authorization Code + PKCE：

| Client | 用途 |
| --- | --- |
| `approver-web-local` | Approver 用户登录 |
| `fluxion-web-local` | Fluxion 用户登录 |
| `bids-web-local` | Bids 用户登录 |
| `record-hub-web-local` | Record Hub 用户登录 |

要求：

- redirect URI 精确匹配，不使用生产通配符；
- 用户稳定主键为 `(iss, sub)`，不用 email/username；
- 校验签名、issuer、audience、expiry 和 nonce/state；
- BFF 保存安全 Session，浏览器不持有长期 service token；
- 每个系统把 `(iss, sub)` 映射为本地 tenant membership 和角色；
- group claim 的变化不得绕过本地资源级授权。

Record Hub 的 OIDC verifier 只接受显式配置的 issuer、单一 audience 和 RS256。它以 `(iss, sub)` 构造稳定身份，缓存已经验证过的 JWKS key，并在遇到未知 `kid` 时刷新 JWKS。错误 issuer/audience/expiry、空 subject、未知 key 且 JWKS 不可用时均 fail closed。本地 HTTP issuer 必须显式开启开发例外，其他环境只接受 HTTPS。

本地授权以精确的 `(tenantId, workspaceId, iss, sub)` membership 为准，token group 不参与请求时的 allow/deny。角色基线如下：

| Capability | OWNER | EDITOR | VIEWER |
| --- | --- | --- | --- |
| 读取 workspace/schema/record/view/index | 允许 | 允许 | 允许 |
| 写 custom record/view | 允许 | 允许 | 拒绝 |
| 管理 workspace/membership/schema/index | 允许 | 拒绝 | 拒绝 |
| 写 projection | 拒绝 | 拒绝 | 拒绝 |

Projection 写入只属于事件 projector 的独立服务策略，不继承任何人类角色。membership 缺失、已撤销、tenant/workspace/identity 不匹配、未知角色或未知 action 均 fail closed。

## 3. 服务间身份

目标模型是每个信任方向使用独立 machine identity，不共享一个超级 client：

```text
approver-to-record-hub
fluxion-to-record-hub
bids-to-record-hub
record-hub-to-approver
record-hub-to-fluxion
record-hub-to-bids
fluxion-to-approver
bids-to-approver
approver-to-fluxion
approver-to-bids
```

Dex 2.45.1 的稳定版尚未实现 `client_credentials`；该能力目前只存在于 Dex 未发布的开发分支，不能因为文档已经提前出现就把稳定镜像视为支持。因此 `RH-M1-014` 暂缓，禁止用已废弃的 password grant 冒充机器身份。后续必须通过 ADR 在“等待含该能力的 Dex 稳定版”“专用 Authorization Server”或“mTLS/workload identity”之间选择。

若后续选定的 Authorization Server 支持 client credentials，仍须满足：

- 每个 client 使用独立 secret 和 audience，只请求所选 issuer 当前支持且确有需要的最小 scope；
- Dex 的 scope 集合不是通用业务权限模型；API 依据 client identity、audience 和本地 client-capability/tenant mapping 授权；
- requester 用户身份作为独立业务字段传递，不能冒充机器 token subject；
- token 短时有效，secret 支持双版本轮换；
- 禁止复用浏览器 Session、BFF secret、NATS credential 或 MongoDB credential。

启用前必须把所选 issuer 实际签发的 client-credentials token 固化为契约测试，验证 `iss/sub/aud/exp/scope` 的真实形态，不能从用户 token 的 claims 推测机器 token。

如果生产环境需要动态 client 注册、丰富的自定义 OAuth scope、细粒度 consent 或完整 service-account 生命周期，应重新评估专用 Authorization Server；不能为了沿用本地 Dex 而把业务授权编码进非标准 claims。

## 4. JetStream 身份

NATS 连接不使用 Dex 用户 token。开发环境至少使用独立 NATS user；生产推荐 NKeys/JWT accounts 或 mTLS，并按 subject 设置 publish/subscribe 权限。

示例权限方向：

| Principal | Publish | Subscribe |
| --- | --- | --- |
| Approver relay | `events.approver.>` | 必要的 `commands.approver.>` |
| Fluxion relay | `events.fluxion.>` | 必要的 `commands.fluxion.>` |
| Bids relay | `events.bids.>` | 必要的 `commands.bids.>` |
| Record Hub projector | `events.record-hub.>` | 三个系统批准的 event subjects |

Record Hub 不应获得所有 `commands.>` 的默认发布权限。

## 5. MongoDB 身份

- 只有 Record Hub 后端访问 MongoDB。
- Web、Workflow Worker 和其他业务系统不持有 MongoDB 凭据。
- 开发使用独立 SCRAM user；生产使用独立 secret 或 X.509。
- API 层强制 tenant filter，数据库索引包含 tenantId。
- 备份、导出和审计数据按租户与敏感级别控制。

## 6. 本地与生产差异

| 项目 | 本地 | 生产 |
| --- | --- | --- |
| Dex | 单实例、开发 connector/静态用户可接受 | TLS、HA storage、正式上游 IdP、备份和密钥轮换 |
| client secret | 本地 secret 文件/环境变量 | Secret Manager，短周期轮换 |
| NATS | 单节点 JetStream 可用于开发 | 集群、多副本、账号隔离、TLS、备份 |
| MongoDB | 本地 replica set 以支持 transaction/change stream | replica set/sharded cluster、TLS、备份、容量治理 |

生产是否继续使用 Dex，应在上线前重新评估 HA、运维、上游 IdP、MFA、审计和机器身份要求；本地采用 Dex 不意味着生产必须采用相同部署形态。
