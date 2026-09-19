# Phase 3 Batch 4H：升级/回滚演练

- 日期：2026-09-20
- 状态：P3-408 `SKIPPED`
- 入口：`make p3-upgrade-rollback`

## 已验收的兼容性门禁

`verify-p3-upgrade-rollback.sh` 已通过 Record Hub 全量 Go 测试、command/result 与
dispatch-approval 四仓库合同镜像校验，并检查 F3 规定的 additive migration、兼容 reader、
feature flag 和旧 worker 保留窗口。

## 现场演练状态

真实 rolling upgrade/rollback 暂标记 `SKIPPED`。当前工作区没有可复现的 previous-version
worker/image、new-version worker/image、隔离 Mongo/PostgreSQL/NATS upgrade targets 或可执行的
rollback runner；不能用当前单一版本二进制冒充旧 worker，也不能对开发服务做 destructive restore。

重试时必须按 `docs/phase-3-acceptance-plan.md` 的 F3 顺序执行：先 additive migration 与兼容
reader，再关闭 feature flag 灰度 producer/consumer，至少观察一个 retention/retry window，随后
验证旧 worker 回滚、在途 operation drain/cancel、最后才允许 contract/migration 收紧。
