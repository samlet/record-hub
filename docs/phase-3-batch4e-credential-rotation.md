# Phase 3 Batch 4E：workload / JWKS / secret rotation

- 日期：2026-09-20
- 状态：P3-405 `SKIPPED`（issuer/contract checks PASS；owner live overlap 未完成）
- 入口：`make p3-credential-rotation`

## 已验证

- `TestOIDCVerifierValidTokenAndRotation`：unknown `kid` 触发 JWKS refresh；已缓存 key 在 JWKS outage
  时继续验证；未知 key 在 outage 中 fail closed。
- `make p2-workload-identity-smoke`：client secret 错误返回 401，未授权 scope 返回 400；token
  audience/scope/issuer contract 通过。

## 不能宣称 live overlap 的原因

当前 `tools/workload-issuer` 是验收用 ephemeral issuer：支持 `SIGHUP` 生成新 RSA key、在
`RECORD_HUB_WORKLOAD_ISSUER_KEY_OVERLAP` 窗口暴露新旧 JWKS，并从
`RECORD_HUB_WORKLOAD_CLIENTS_FILE` 热加载 client credentials；旧 key 到期后从 JWKS 移除，
被删除的 client 立即 fail closed。P3-400 supervisor 启动的四 owner 仍只在进程启动时读取
各自 token/secret，尚无安全的 in-place reload 协议。因此 issuer contract PASS 不能冒充四
owner credential rotation。

证据文件会把 issuer/contract PASS 与 owner live SKIPPED 分开写入 `credential-rotation.json`
和 `p3-405-credential-rotation.json`。重试条件是先提供 owner credential reload、旧
key/secret revoke deadline，再以同一四 owner topology 运行该 gate。
