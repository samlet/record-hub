# Phase 6 Batch 5：P6-501 connector onboarding workflow

P6-501 固化 connector 自助接入的 contract upload、review、compatibility 和 approval 状态机。
manifest 必须以 `(connector,event,schemaVersion)` 精确匹配，包含 owner、SDK compatibility、
contract/fixture/redaction/source commit digest；提交者、独立 reviewer 和 owner approver 分离，
状态转换使用 CAS，启用前必须完成兼容、fixture、redaction 和 operator audit 检查。

机器可读 contract 位于 [`p6-501-connector-onboarding-spec.json`](../deploy/local/p6/p6-501-connector-onboarding-spec.json)，
验收结果位于 [`phase-6-connector-onboarding.json`](phase-6-connector-onboarding.json)。入口是 `make p6-501`。

当前静态 inventory 确认 registry、manifest、exact-key lifecycle 和 evidence contract 已存在；
但 scoped onboarding API 与 console review/approval 入口尚未实现，故状态为
`BLOCKED_BY_ONBOARDING_API_GAP`，不允许启用 self-service connector，也不改变 P6-000 前置门禁。
