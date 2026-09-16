# M6 Workflow Binding 验收记录

Record Hub 基础能力（M6-060～063）、Fluxion Temporal 适配（M6-064～065）与 Bids
Conductor Worker（M6-066）已完成。M6-067 已提供跨仓库 contract gate 和可选 live smoke，
但真实 Record Hub/Mongo/Dex/双引擎重启恢复尚未在当前环境执行，因此保持 PARTIAL。

## 双引擎 E2E（M6-067，PARTIAL）

`scripts/verify-m6-bindings.sh` 先运行 Fluxion Temporal 与 Bids Conductor 的定向测试，避免把
本地 fake 当成跨服务验收。设置 `RECORD_HUB_M6_LIVE=1` 并提供 Record Hub URL、Dex machine
token、tenant/workspace、project/tender ID 后，它会对两个 schema 执行真实 snapshot + 相同
operation ID replay；在人工重启 Record Hub 后追加 `RECORD_HUB_M6_VERIFY_RESTART=1`，验证
snapshot GET 仍能读取同一 ID。

```bash
./scripts/verify-m6-bindings.sh
```

默认 gate 已通过；live smoke 需要 Record Hub API 已接入 Mongo store、Mongo replica set 和
Dex service principals，当前主机缺少这些可用依赖，不能安全伪报通过。

## Bids Conductor 适配（M6-066）

Bids 提交 `616e7fd` 已推送到 Gitee。新增独立 Workflow
`bids_record_hub_tender_diagnostic` v1 与 SIMPLE task
`bids_record_hub_tender_snapshot_v1`，Worker 通过公共 Binding API 固定
`bids:TENDER:{id}` / `urn:record-hub:summary:tender:v1`，以稳定 operation ID 重试。task
output 只含 snapshot 引用、版本、hash、replay 标记和安全摘要，不含 `data`、联系人、报价或文件。

Bids 验收命令：

```bash
cd /Users/xiaofeiwu/apps/bids/backend
go test ./...
```

覆盖 Conductor 定义、HTTP 契约、同 operation ID 重复调用和 4xx 非重试映射。

## Fluxion Temporal 适配（M6-064～065）

Fluxion 提交 `6d496b0` 已推送到 Gitee。`ProjectDiagnosticWorkflow` 通过
`ProjectDiagnosticBindingActivity` 使用公共 snapshot API，固定 `fluxion:PROJECT:{id}` 和
`urn:record-hub:summary:project:v1`，重试复用同一 operation ID；Activity 只返回 snapshot
引用/版本/hash 及 PII-free `type/status/currentStage/version` 安全摘要，原始动态记录不会进入
Temporal history。Fluxion `docs/m6-acceptance.md` 记录了实现与测试细节。

Fluxion 验收命令：

```bash
cd /Users/xiaofeiwu/portals/fluxion/server
./gradlew test
```

覆盖成功、超时重试、重复 operation、history 字段隔离和 `Worker.replayWorkflowExecution`。

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
