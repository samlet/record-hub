# P5-300 Connector registry、SDK semver 与 compatibility window

Record Hub 新增 server-side connector registry 基线，按 `(connector, event, schemaVersion)` 精确解析；manifest 固定 owner、SDK 版本、兼容窗口、contract hash、allowed fields、生命周期和 revision。未知 connector/event/schema、disabled/deprecated connector、SDK 越界、重复注册和 stale revision 均 fail closed。

Approver 已有的 request/result contract registry 继续作为 owner-side handler boundary；Record Hub registry 不接管 Approver、Fluxion 或 Bids 的领域事实，也不允许请求体动态提供 endpoint、credential 或 action。

静态入口：`make p5-300`。它运行 connector registry 单测、检查 Approver/Bids registry boundary、manifest/hash 字段和 Phase 5 connector contract。

当前状态为 `PARTIAL`：静态检查应全部 PASS；connector admin lifecycle、真实 owner side-effect、manifest parity、禁用/回滚/reconciliation live 尚未执行，当前为 `SKIPPED`。

解除条件：提供 connector admin control plane、Approver/Fluxion/Bids owner runtime、compatibility/reconciliation harness 和 evidence root，设置 `RECORD_HUB_P5_CONNECTOR_LIVE=1` 后执行完整 negative matrix。未知 key 不得 fallback 到其他 connector。
