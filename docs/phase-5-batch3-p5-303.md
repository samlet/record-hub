# P5-303 Fluxion/Bids approval connector production hardening

P5-303 将 Fluxion 和 Bids 现有 approval connector 的生产边界固化为跨仓库静态验收门。两边仍各自拥有 Temporal/Conductor workflow、领域数据库和终态副作用；Record Hub 不直接修改 Fluxion project、Bids tender 或审批终态，也不能作为未知 connector 的 fallback。

Fluxion 的 request/result 路径已经具备稳定 external request identity、request/result/update outbox、Inbox 幂等、`decisionVersion`、generation guard、`VERSION_DRIFT` reconciliation finding，以及 tenant/workspace rollout、in-flight drain 和 local human-task fallback。Bids 的路径已经具备 tender+generation/proposal hash identity、Approver HTTP relay 的 Idempotency-Key、request/result/task-completion outbox、result Inbox 与 tender mutation 的事务边界、版本/事件冲突拒绝，以及 workflow reconciliation 和 bounded outbox retry/dead-letter 配置。

静态入口：`make p5-303`。它检查两套 owner-side effect、晚到/版本冲突对账、fallback/rollback、connector routing/isolation 和三仓库 commit boundary，并生成 `build/evidence/phase5/p5-303-*/p5-303.json`。

当前状态为 `PARTIAL`：静态检查应全部 PASS；Approver outage、重复 request/result、晚到结果、租户级禁用 drain、本地 fallback、旧 worker rollback 和 post-rollback reconciliation 需要隔离的 Approver/Fluxion/Bids/Temporal/Conductor 拓扑，当前 live 为 `SKIPPED`。

解除条件：准备 topology file、owner service credentials、故障注入与 immutable evidence root，设置 `RECORD_HUB_P5_APPROVAL_CONNECTOR_LIVE=1` 后执行完整矩阵。任何未知 connector、跨 owner 写入、scope/version 不匹配或重复领域副作用都必须 fail closed；旧 worker、local fallback 和 rollback artifact 在独立评审前继续保留。
