# Phase 6 Batch 5：P6-501 connector onboarding workflow

P6-501 固化 connector 自助接入的 contract upload、review、compatibility 和 approval 状态机。
manifest 必须以 `(connector,event,schemaVersion)` 精确匹配，包含 owner、SDK compatibility、
contract/fixture/redaction/source commit digest；提交者、独立 reviewer 和 owner approver 分离，
状态转换使用 CAS，启用前必须完成兼容、fixture、redaction 和 operator audit 检查。

机器可读 contract 位于 [`p6-501-connector-onboarding-spec.json`](../deploy/local/p6/p6-501-connector-onboarding-spec.json)，
验收结果位于 [`phase-6-connector-onboarding.json`](phase-6-connector-onboarding.json)。入口是 `make p6-501`。

当前已补齐 scoped onboarding upload/list API、DRAFT→IN_REVIEW→APPROVED→ENABLED 的 CAS
状态机、独立 reviewer/owner approver、证据校验、immutable audit，以及 Web review/approval
console；`make p6-501` 静态结果为 `PASS_STATIC`。实现仍使用本地进程内 onboarding store，未
提供生产持久化/多副本协调，也未解除 P6-000、Phase 5 或 connector live enable gate，因此不
允许真实 connector 流量。
