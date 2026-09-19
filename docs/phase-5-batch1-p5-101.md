# P5-101 Tenant / Organization / Connector Policy Lifecycle

Record Hub 已通过静态 gate 固化 scope fail-closed 边界：human API 只接受 ACTIVE 且 tenant/workspace/identity 完全匹配的 membership；machine binding/command policy 要求精确 issuer、subject、audience、scope、tenant、workspace 和 action，不接受 wildcard 或重复条目；catalog source/mapping 只能使用 allowlisted tenant resolution；association/operations 跨 scope 请求返回拒绝且不泄露资源。

静态入口：`make p5-101`。当前 membership、machine policy、catalog scope、cross-scope tests 和 Phase 5 contract 检查均 PASS。

P5-101 的 live lifecycle 仍 `SKIPPED`：当前 Record Hub 没有独立的 production admin control-plane API/provisioner 来完成 tenant/org/connector enable、disable、revoke、审计和凭据轮换。必须先确定 control-plane owner 与部署方式，再执行四 scope live matrix。
