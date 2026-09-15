# Workflow Binding 与事务语义

## 1. Binding 模型

Workflow 节点通过 API/SDK 使用绑定，不直接访问 MongoDB：

```json
{
  "bindingId": "approval-input",
  "recordRef": "bids:TENDER:01J...",
  "schemaId": "urn:record-hub:bids:TenderApproval",
  "schemaVersion": 3,
  "readMode": "SNAPSHOT_READ",
  "expectedRecordVersion": 17,
  "snapshotHash": "sha256:...",
  "writeMode": "IDEMPOTENT_COMMAND",
  "onConflict": "MANUAL_REVIEW"
}
```

Workflow history 保存引用、版本、hash 和安全摘要，不保存完整动态记录。

## 2. 支持的事务属性

| 模式 | 保证 |
| --- | --- |
| `SNAPSHOT_READ` | 固定版本读取；后续更新不改变本次节点输入 |
| `LATEST_READ` | 读取当前投影；只适用于显式允许非确定性外部读取的 Activity/Task |
| `COMPARE_AND_SET` | 仅当 `recordVersion` 等于 expected version 时写入 |
| `LOCAL_ATOMIC` | Record Hub 内记录、审计和 Outbox 同一 MongoDB 事务 |
| `IDEMPOTENT_COMMAND` | 相同 operation ID 返回原 receipt |
| `APPEND_ONLY` | 只允许追加新版本/事件 |
| `SAGA` | 外部步骤失败时执行显式补偿 |

不支持 `CROSS_SYSTEM_ACID`。任何 API、SDK 或 UI 均不得把上述模式描述为跨系统 exactly-once transaction。

## 3. 节点执行协议

```text
1. Workflow 以稳定 operationId 请求 snapshot 或 command。
2. Record Hub 校验 tenant、schema、权限和 expected version。
3. Record Hub 本地事务写 record/receipt/audit/outbox。
4. 返回持久化 receipt。
5. 响应丢失时，Workflow 用原 operationId 重试。
6. 外部命令由 owner system Inbox 去重并执行。
7. 结果通过 event/result receipt 返回并唤醒 Workflow。
```

## 4. Temporal 适配

- 所有 Record Hub I/O 只能在 Activity/Nexus handler 中发生，不能在 Workflow deterministic code 中直接发 HTTP/NATS/Mongo 请求。
- Activity retry 使用同一个 operation ID。
- 等待外部结果时由 Inbox 落库后再向 Workflow Signal/Update；重复结果按 decision/result version 去重。
- Continue-As-New 必须携带 pending binding 和 external request ID，或确保边界前不存在待完成绑定。

## 5. Conductor 适配

- Worker task 调用 Binding API。
- HUMAN task 可以作为内部等待令牌，但用户审批由 Approver 完成。
- Approver result 先进入 Bids Inbox，再通过现有 Outbox 完成 Conductor HUMAN task。
- approved、rejected、cancelled、failed 使用显式分支，不能都映射为成功完成。

## 6. 冲突策略

支持：

- `FAIL`：立即失败并由 Workflow 决定重试；
- `REFRESH`：重新读取并再次评估；
- `MANUAL_REVIEW`：生成冲突任务；
- `COMPENSATE`：执行已登记补偿。

涉及金额、定标、签署、审批和付款的默认策略是 `MANUAL_REVIEW`，不得自动覆盖新版本。

