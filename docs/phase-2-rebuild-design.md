# Projection Rebuild Operation（P2-1-004）

状态：Control plane and mapping-generation staging implemented；历史事件 replay 待事件保留策略冻结
日期：2026-09-17

## 目标与边界

rebuild 是 Owner 发起的、可审计的 generation 重建操作。它不直接修改业务事实，也不绕过
published source/mapping/schema 校验。当前垂直切片重建的是 projection mapping generation：

1. 从 Mongo 读取 published mappings；
2. 重新校验 canonical hash、source 状态、target schema、field allowlist 并编译 validator；
3. 将完整结果持久化为 operation 的 staging generation ID；
4. 在取消检查通过后，用一个 atomic pointer switch 激活 generation；
5. 把 active generation ID、mapping count、前后 generation、状态和安全错误摘要写入 operation。

历史事件的完整 replay 仍需先冻结 JetStream retention / event archive 作为唯一重放来源，再把
事件 replay 到隔离的 record staging collection；不能通过复用当前 Inbox/records 表或人工修改
projection 来冒充 rebuild。这个剩余项保持 `PARTIAL`，不会被本批的 generation 切换证据掩盖。

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
取消、运行中取消、receipt 冲突和 Mongo repository live index/CAS。历史 event replay、staging
record collection、读指针切换和 replay 后 record freshness 是下一批的明确工作项。
