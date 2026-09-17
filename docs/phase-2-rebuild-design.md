# Projection Rebuild Operation（P2-1-004）

状态：DONE（archive-backed replay、staging records、read pointer switch 已实现；archive 启用前的历史事件不在重放窗口内）
日期：2026-09-17

## 目标与边界

rebuild 是 Owner 发起的、可审计的 generation 重建操作。它不直接修改业务事实，也不绕过
published source/mapping/schema 校验。当前垂直切片重建的是 projection mapping generation：

1. 从 Mongo 读取 published mappings；
2. 重新校验 canonical hash、source 状态、target schema、field allowlist 并编译 validator；
3. 将完整结果持久化为 operation 的 staging generation ID；
4. 在取消检查通过后，用一个 atomic pointer switch 激活 generation；
5. 把 active generation ID、mapping count、前后 generation、状态和安全错误摘要写入 operation。

事件在 projector 入口先写入 `projection_event_archive`，保留原始 envelope 和规范化路由字段。
rebuild worker 读取 tenant/workspace scope 的 archive，按记录版本单调地写入
`projection_staging_records_<operationId>`，不会复用 live Inbox、checkpoint 或 audit；超过 archive
窗口会安全失败。回放完成后，在 Mongo transaction 中为目标表退休旧 pointer、写入新的
`projection_read_pointers`，并补齐 replay 期间的 aggregate checkpoints；随后 live projector 会
根据 pointer 继续写入同一 staging collection，API 读取也会遵循 pointer。生成指针切换仍保留
last-known-good 语义，失败或取消不会暴露 staging collection。

## API

```text
POST /api/v1/projection/rebuilds
GET  /api/v1/projection/rebuilds/{operationID}?tenantId=&workspaceId=
POST /api/v1/projection/rebuilds/{operationID}/cancel
```

创建和取消要求 `OWNER`、`Idempotency-Key`、`X-Request-ID`；读取要求 `schema.read`。所有
operation receipt、audit 和 operation state 都按 tenant/workspace 隔离。重复 key 复用原结果，
request hash 冲突返回 409。

## 状态机与恢复

```text
ACCEPTED -> RUNNING -> SUCCEEDED
    |         |             
    v         v             
CANCELLED  CANCEL_REQUESTED -> CANCELLED
    
RUNNING -> FAILED
```

worker 通过 revision CAS 领取和更新 operation。Mongo 短暂不可用时 worker 保持进程存活并重试；
`RUNNING` operation 在进程重启后会再次从 published catalog 组装同一 generation，完成未决的
active pointer switch。generation 构建或校验失败只写 `FAILED`，不会改变 last-known-good pointer。
取消在 pointer switch 前生效；switch 后 operation 已进入提交窗口，不承诺跨数据库事务回滚。

## 验收

```bash
make check
RECORD_HUB_MONGODB_URI=mongodb://127.0.0.1:27017/?replicaSet=rs0\&directConnection=true \
  go test ./server/internal/modules/projection \
  -run 'TestMongoRebuildRepositoryCASAndReceipt|TestProjectionRebuild' -count=1 -v
```

已覆盖：幂等创建、Owner 权限边界、CAS、staging/active generation、失败保留旧指针、接受前
取消、运行中取消、receipt 冲突、Mongo archive/index/CAS、staging record replay、read pointer
切换、checkpoint 补齐和 pointer 切换后的 live record freshness。
