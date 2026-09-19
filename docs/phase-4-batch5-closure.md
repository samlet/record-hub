# Phase 4 Batch 5 Closure

- 日期：2026-09-20
- 范围：P4-500 ～ P4-504
- RC：`rc-cbaa0a357110`
- RC manifest：`docs/phase-4-release-candidate-manifest.json`
- P4-501 预检：`make p4-batch5`

| 任务 | 状态 | 结论 | commit |
| --- | --- | --- | --- |
| P4-500 | DONE | 四仓库 commit/digest、6 个 artifact SHA-256 和 migration/contract/config 摘要已冻结 | `05dda33` |
| P4-501 | SKIPPED | P4-G1～G5 静态 PASS，live 全部缺隔离 topology/harness | `8ac70ce` |
| P4-502 | SKIPPED | 单 tenant 灰度窗口规格已固化，未启动流量 | `389be75` |
| P4-503 | SKIPPED | Beta 报告模板已固化，未签字 | `2e11af6` |
| P4-504 | DONE | 独立评审决策 KEEP，保留旧 worker/schema reader/local fallback | `176ec6b` |

P4-501 是 Batch 5 发布硬门槛，不能用本机服务或静态检查替代 live rotation、restore、mixed-load、rolling rollback。下一次继续时应先提供隔离四 owner topology、Viewer/Editor/Operator 凭据、old/new artifacts 和专用故障/负载 harness，再重跑 `RECORD_HUB_P4_BATCH5_LIVE=1 make p4-batch5`。
