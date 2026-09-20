# P4-501 完整 Live 发布验收

验收入口是 `make p4-batch5`，规格为 [p4-batch5-spec.json](../deploy/local/p4/p4-batch5-spec.json)。P4-G1～G5 分别覆盖 credential/JWKS rotation、Mongo/PostgreSQL/NATS restore、mixed load/drain、rolling upgrade/rollback 和跨 owner fault matrix。

当前 native runner 已接入：`make p4-501-native` 会校验隔离 topology、operator credential contract、old/new artifact roots，并复用四 owner supervisor。最近一次运行的 topology readiness 为 `PASS`，五个 case 的 contract checks 为 `PASS`；但 live 仍为 `5/5 SKIPPED`，因为没有批准的 rotation、restore、mixed-load、rolling rollback 和 cross-owner fault immutable report。详见 [native evidence](phase-4-p4-501-native-evidence.json)。

因此 P4-501 明确保持 `BLOCKED`，不得进入 Beta 流量。只有五个 case 均为 `PASS`（无 `SKIPPED`/`PARTIAL`），才能将该任务改为 `DONE` 并重新运行 P5-002。
