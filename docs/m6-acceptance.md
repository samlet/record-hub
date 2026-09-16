# M6 第一批验收记录：Workflow Binding 基础

本批完成 M6-060～063：Record Hub 的不可变 snapshot、机器身份策略，以及 Go/Java/Kotlin
调用 facade。Temporal Activity、Conductor Worker 和双引擎 E2E 属于后续批次。

## 已交付

- `POST /api/v1/bindings/snapshots` 创建 snapshot，`GET /api/v1/bindings/snapshots/{id}`
  按 tenant/workspace 读取。
- `Idempotency-Key` 作为稳定 operation ID；同请求重放返回原 snapshot，不同请求 hash 返回
  `409 IDEMPOTENCY_CONFLICT`。
- snapshot 保存 `recordRef`、schema/record/source version、purpose、canonical data 和
  `snapshotHash`，没有更新或删除 API。
- MongoDB store 使用 `(tenantId, workspaceId, operationId)` 唯一索引，并以 BSON object
  保存 data；内部 operation/request hash 不出现在响应。
- service principal 必须命中精确的 issuer/subject/audience/tenant/workspace/resource/
  purpose allowlist。没有 wildcard、跨租户 fallback 或用户角色继承。
- `sdk/go/recordhub` 提供 context-aware client、超时/取消、bounded response 和 typed API
  error；`sdk/java` 提供 Java 17+/Kotlin-callable、无 Temporal/Spring 依赖的 facade。

## 契约与实现位置

| 项目 | 位置 |
| --- | --- |
| OpenAPI | `api/openapi.yaml` 的 `Bindings` tag、snapshot paths 和 schemas |
| Go server | `server/internal/modules/binding` |
| Go SDK | `sdk/go/recordhub` |
| Java/Kotlin SDK | `sdk/java` |
| canonical hash fixture | `contracts/bindings/snapshot-hash-v1.json` |

## 验收命令

```bash
make check
go test ./sdk/go/recordhub
mvn -q test -f sdk/java/pom.xml
```

覆盖项包括 snapshot replay/conflict、record/source/schema version 冲突、用户授权、机器
policy 的 tenant/workspace/resource/purpose/audience 负向矩阵、HTTP 错误映射、canonical
hash fixture、Mongo document round-trip、Go context cancellation 和 Java API error。

## 身份边界

M1-014 的 Dex `client_credentials` 仍保持延期；本批只实现可替换的 policy boundary，未用
password grant 冒充 machine identity。后续选择稳定 Authorization Server、mTLS 或 workload
identity 后，再把真实 token 签发契约接入 policy 测试。
