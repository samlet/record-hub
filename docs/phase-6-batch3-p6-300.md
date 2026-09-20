# Phase 6 Batch 3：P6-300 Approver safe projection

P6-300 固化 Approver Application/Process 投影的安全字段、tenant/workspace scope、稳定 typed ref、
版本单调性和 gap/conflict/duplicate reconciliation。当前只验证双方已有 contract 和 Record Hub
投影边界；没有 native owner connector 的 immutable live evidence，因此标记 `BLOCKED_BY_LIVE_CONNECTOR`。

机器可读 contract 位于 [`p6-300-approver-projection-spec.json`](../deploy/local/p6/p6-300-approver-projection-spec.json)，
盘点结果位于 [`phase-6-approver-projection.json`](phase-6-approver-projection.json)。入口是 `make p6-300`。
