# M8-086 故障恢复与凭据轮换

本 runbook 只允许使用原始 `eventId`、`operationId`、`consumer` 和已登记的
`recordRef`。DLQ 是安全摘要，不含原始业务 payload；禁止根据 DLQ reason 手工
拼装一条“等价事件”，也禁止修改 source system 的事实数据来绕过 gap。

## 1. 发现与冻结范围

1. 在 Operations 页面或 API 中锁定 `tenantId`、`workspaceId` 和精确
   `consumer`（`record-hub-approver-projection-v1`、`record-hub-fluxion-projection-v1`
   或 `record-hub-bids-projection-v1`）。
2. 保存返回的 checkpoint `lastEventId`、`sourceVersion`、`status`、
   `aggregateId` 以及查询时使用的 `operationId`（如有）。不要把 payload、token、
   email 或文件 URL 复制到工单。
3. 记录当前 inbox `processing/applied/rejected/failed` 和
   `oldestProcessingAt`，以便恢复后比较；Operations API 最多返回 100 条 checkpoint。

## 2. GAP 恢复

1. 在 source system 的 Outbox 中按原始 aggregate、`sourceVersion` 或
   `lastEventId` 定位缺失事件；确认它仍是该系统事务产生的原 event。
2. 让 source relay 使用原 event ID 重发到原始 `events.<system>.<aggregate>...`
   subject。不要生成新 event ID，也不要直接向 Record Hub Mongo 写 projection。
3. 等待对应 durable consumer 重连并处理；若 handler 仍返回 gap，继续沿 source
   version 顺序补齐，不能跳过版本。
4. 用同一 tenant/workspace/consumer 查询 Operations，确认 checkpoint 从 `GAP`
   前进为 `CURRENT`，`sourceVersion` 连续，inbox processing 下降。只读 metadata
   的 recovery 结果必须与原 event ID 关联。

## 3. DLQ 恢复

1. 读取 `dlq.record-hub.<consumer>` 的安全 envelope，记录其中的原始 `eventId`、
   `originalSubject`、`attempts` 和 bounded `reason`。不要把 reason 当作重放 payload。
2. 先修复确定性原因（schema/contract、membership、配置或 projection 代码），并
   发布经过 review 的版本。
3. 回到 source system Outbox 或 JetStream 保留区，用原 `eventId` 和原始 payload
   重投到 `originalSubject`；Record Hub Inbox 的 `(consumer,eventId)` 去重保证已经
   完成的事务不会重复产生业务副作用。
4. 重新检查同一 checkpoint、record version、audit 条目和 Operations 计数。重投
   仍失败时保留原 DLQ ID 和 event ID，不能通过不断生成新 ID 掩盖问题。

## 4. ACK loss / NATS outage

- 业务事务和 Outbox 不依赖 NATS 同步成功；先确认 source Outbox lease/retry 没有
  被人工删除。
- NATS 恢复后使用原 message ID 发布；不要 purge stream 或重建 durable consumer
  来“清空”问题。
- 对应 projector 只使用预建 durable pull consumer、explicit ACK 和 `MaxDeliver`；
  recovery 后重复的 event 应落在 Inbox duplicate 路径，而不是新建记录。
- 使用 `make nats-smoke`、`make nats-permissions-smoke` 和
  `./scripts/verify-m7-nats-recovery.sh` 记录拓扑、权限和代码级恢复证据。

## 5. 凭据轮换

### Dex Web client

1. 在 Secret Manager 生成新 client secret；保留旧 secret 直到四个 BFF 都完成重启。
2. 更新 Dex 的 `DEX_*_WEB_SECRET`，滚动重启 Dex，再对每个 exact redirect URI 运行
   `make dex-smoke`。
3. 更新 Approver、Fluxion、Bids 和 Record Hub 的对应环境变量并滚动重启。不要把
   Web client secret 复用为 Session/NATS/Mongo credential。

### NATS

1. 为目标 principal 生成新 password/NKey；先在服务端配置双凭据窗口，再滚动更新单个
   relay/projector，观察连接、Outbox lease 和 backlog。
2. 运行 `make nats-permissions-smoke`，确认 producer 仍只能发布自己的
   `events.<system>.>`，projector 仍只能访问 allowlist。
3. 旧凭据全部不再使用后撤销；不得通过共享 admin credential 恢复业务消息。

### MongoDB

1. 为 Record Hub application user 生成新 password，先在受控的 replica set 上创建/更新
   credential；只更新 Record Hub，不向 Web、workflow worker 或其他系统分发 Mongo URI。
2. 滚动重启 API/worker，确认 transaction、change stream、checkpoint 和 operations
   查询恢复，再撤销旧 password。保留 outage 前后的 event/operation ID 证据。

### Web Session secret

当前 MVP 使用单一 HMAC session secret。轮换会使全部浏览器 session 失效，这是预期的
安全行为：

1. 生成至少 32 字节随机值，更新 `RECORD_HUB_WEB_SESSION_SECRET` 并滚动重启 BFF；
2. 重新登录，验证新 cookie 的 `HttpOnly`、`SameSite=Lax`、生产 `Secure`；
3. 旧 cookie 必须被拒绝，不要为了平滑迁移把旧 secret 写回代码或日志。

生产若需要无感轮换，应另行实现双 key verify/单 key sign 和审计窗口；本 MVP 不假设
该能力已经存在。

## 6. 验收与限制

```bash
./scripts/verify-m7-mongo-faults.sh
./scripts/verify-m7-nats-recovery.sh
./scripts/verify-m7-dex-rotation.sh
make m8-failure-path
```

上述命令在依赖不可用时只给出 deterministic 结果并明确 skipped。Mongo/NATS/Dex 真正
的 outage、DLQ 重放、凭据滚动和三个 projection backlog 清空，必须在 M8-085 的受监督
拓扑中执行；没有 Docker 或真实 source Outbox 时，M8-086 保持 `PARTIAL`。
