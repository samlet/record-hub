# 事件与 NATS JetStream 契约

## 1. 交付模型

```text
业务事务 -> 本地 Outbox -> JetStream publish -> Durable Consumer
                                      -> Inbox/业务事务 -> ACK
```

规则：

- 生产者不得在业务事务中直接双写数据库和 NATS。
- `eventId` 同时作为 Outbox 唯一键、JetStream message ID 和消费者 Inbox 唯一键。
- 消费者必须在本地事务提交后 ACK。
- ACK 丢失导致的重投必须返回已完成结果，不能重复产生业务副作用。
- 业务幂等不能只依赖 JetStream 的发布去重窗口。

## 2. Stream 基线

| Stream | Subjects | 用途 |
| --- | --- | --- |
| `DOMAIN_EVENTS` | `events.approver.>`, `events.fluxion.>`, `events.bids.>`, `events.record-hub.>` | 多消费者领域事件与重放 |
| `APPROVAL_COMMANDS` | `commands.approver.>` | 单一处理方的审批命令 |
| `OWNER_COMMANDS` | `commands.fluxion.>`, `commands.bids.>` | 回到事实来源执行的命令 |
| `COMMAND_RESULTS` | `results.approver.>`, `results.fluxion.>`, `results.bids.>` | owner 事务提交后的命令终态结果 |
| `DEAD_LETTERS` | `dlq.>` | 超限失败和人工恢复 |

生产配置应使用 file storage 和多副本；具体副本数、容量、MaxAge 和地域拓扑在容量测试后确定。

## 3. Subject 约定

```text
events.<system>.<aggregate>.<event>.v<major>
commands.<target-system>.<capability>.<action>.v<major>
results.<owner-system>.<action>.v<major>
dlq.<original-system>.<consumer>
```

示例：

```text
events.approver.application.completed.v1
events.fluxion.project.stage-changed.v1
events.bids.tender.published.v1
commands.approver.approval.request.v1
commands.bids.payment-approval.apply.v1
results.fluxion.project.annotate.v1
```

tenant、aggregate ID 和用户 ID 放在 envelope，不放入 subject，避免高基数 subject 和授权规则膨胀。

## 4. Consumer 约定

- 默认使用 durable pull consumer 和 explicit ACK。
- 一个逻辑消费用途对应一个 durable 名称，例如 `record-hub-projector-v1`、`approver-command-inbox-v1` 和 `record-hub-command-results-v1`。
- 同一 durable 可以水平扩展 worker pool。
- `AckWait` 大于 handler 正常最大处理时间；长任务定期续期或拆分。
- `MaxDeliver` 后由消费者发布安全、脱敏的 DLQ envelope。
- 重放必须经过管理员权限、原因、命令 ID 和审计。

## 5. 顺序与版本

不依赖跨 subject 或跨 aggregate 的全局顺序。每个投影使用：

```text
aggregateVersion == currentVersion + 1  -> apply
aggregateVersion <= currentVersion      -> duplicate/ignore
aggregateVersion > currentVersion + 1   -> gap/pause/recover
```

无法提供连续版本的事件类型必须明确声明为 append-only observation，不能覆盖当前状态。

## 6. Command 与 Event

- Event 是已经发生的事实，使用过去式，不要求消费者响应。
- Command 是执行请求，必须有 target、operation ID、expected version 和结果事件。
- 表格普通编辑只产生 `record-hub.record.updated`。
- 修改外部业务事实必须产生 owner command，不能伪造领域 event。

## 7. 契约所有权

- `approver.*` 由 Approver 仓库维护。
- `fluxion.*` 由 Fluxion 仓库维护。
- `bids.*` 由 Bids 仓库维护。
- `record-hub.*` 由本仓库维护。

生产者仓库保存权威 schema、fixture 和兼容性测试；Record Hub Registry 保存已发布副本和 hash。

## 8. Envelope v1 验证边界

- 原始消息不得超过 256 KiB，先检查字节数再解析 JSON。
- 顶层未知字段拒绝，`schemaVersion` 只接受当前支持的 `1`。
- `occurredAt` 必须是带 `Z` 的 RFC3339 UTC；允许历史事件重放，但拒绝超过当前时间五分钟的未来时间。
- JSON format 校验包含 UUID 和 date-time；尾随 JSON 和非法 UTF-8 拒绝。
- 验证错误不包含原始 payload，避免把敏感内容带入日志或 DLQ。

共享 schema、可执行 verifier 和跨语言输入 fixture 位于 `contracts/eventenvelope`。

三个低敏 summary 契约的 Record Hub 镜像位于 `contracts/summaries`，包含 Draft 2020-12
schema、canonical safe fixture 和 `manifest.json` 中的 `schemaContentHash`。生产者仓库在
M5-050 必须复制这些文件并用各自语言校验相同 hash；Record Hub handler 只接受对应的
`sourceSystem/eventType/schemaVersion` 精确组合。
