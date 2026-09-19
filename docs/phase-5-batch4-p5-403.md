# P5-403 Fallback/legacy removal review

P5-403 对旧 worker、旧 schema reader、Fluxion local human-task fallback 和 immutable rollback artifact 做独立保留/移除决策。当前决策明确为 `KEEP`：P4-501 全部 live gate、P5-G1～G7、单 tenant 观察窗口和 risk owner signoff 完成前，不删除旧路径、不提前撤销凭据、不关闭 fallback，也不丢弃 rollback artifact。

静态入口：`make p5-403`。它检查已有 KEEP 决策、in-flight drain、removal gate、owner boundary 和 destructive-action negative scan，并生成 `build/evidence/phase5/p5-403-*/p5-403.json`。

当前状态为 `PARTIAL`：四项静态检查 PASS；独立四仓库 inventory、在途 drain、rollback drill、retention/reconciliation window 和签字 review 尚未执行，live 为 `SKIPPED`。本任务不是删除授权，也不改变现有 fallback/rollback 行为。

解除条件：完成 P4-501/P5-G1～G7、P5-401 观察窗口和正式 risk-owner review，证明 replacement contract parity、无 in-flight request、rollback drill、retention/reconciliation 和 owner approval 后，才能单独提交 REMOVE 决策；否则继续 KEEP。
