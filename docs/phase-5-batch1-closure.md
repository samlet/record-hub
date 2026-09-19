# Phase 5 Batch 1 收口：Production identity 与 control plane

## 结论

Batch 1 的静态 contract、配置安全边界和角色授权已经完成并通过；生产 live gate 尚未通过，因此本批次状态为 `PARTIAL`，不能作为 Production GA 依据。

| 任务 | 结果 | 提交 | 静态证据 | Live 状态 |
| --- | --- | --- | --- | --- |
| P5-100 | production OIDC 与 service identity 安全边界 | `9bf90a0` | `make p5-100`：production config、OIDC verifier、exact policy、browser boundary、Phase 5 contract 全 PASS | SKIPPED：缺 production Dex/OIDC TLS、secret manager、四个独立 owner principal、rotation/revocation harness |
| P5-101 | tenant/org/connector scope policy | `fcb78e8` | `make p5-101`：membership、machine policy、catalog scope、cross-scope、Phase 5 contract 全 PASS | SKIPPED：缺 control-plane lifecycle API/provisioner 与四 scope enable/disable/revoke evidence |
| P5-102 | Viewer/Editor/Operator/Admin 权限分离 | `2eb5c02` | `make p5-102`：role model、operator boundary、read-only console、regression、Phase 5 contract 全 PASS | SKIPPED：缺 role-bound production identity、API/UI matrix 与 audit export |

## 已落地的边界

- `OPERATOR` 是独立 workspace role；运维读取使用 `operations.read`，projection rebuild 使用 `projection.manage`。
- `projection.write` 不授予任何人工角色，只能由 worker 使用。
- production 环境拒绝不安全 OIDC issuer、浏览器 endpoint 和非 Secure cookie。
- machine binding/command policy 要求 exact issuer、subject、audience、scope、tenant、workspace 和 action，不接受 wildcard。
- approval association console 继续保持只读，跨 scope 请求 fail closed。

## 未完成与解除条件

以下事项不能用本地单节点服务或静态测试替代：

1. P4-501 live gate 仍有 `SKIPPED`，Phase 5 不能进入 GA 流量。
2. 需要由平台 owner 提供 production Dex/OIDC TLS、secret manager、role-bound identities、control-plane lifecycle API 和审计导出。
3. 完成上述准备后，按各任务文档设置对应 `RECORD_HUB_P5_*_LIVE=1`，保存不可变 evidence manifest，再重新执行 gates。

## 验证记录

- `go test ./...`：PASS（Record Hub 全部 package）。
- `make p5-100`、`make p5-101`、`make p5-102`：静态检查 PASS；live 检查按前置条件 SKIPPED。
- 当前工作树仅保留用户已有的未跟踪本地文件 `c123.db`，未纳入提交。
