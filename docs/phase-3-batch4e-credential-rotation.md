# Phase 3 Batch 4E：workload / JWKS / secret rotation

- 日期：2026-09-20
- 状态：P3-405 `SKIPPED`（contract checks PASS）
- 入口：`make p3-credential-rotation`

## 已验证

- `TestOIDCVerifierValidTokenAndRotation`：unknown `kid` 触发 JWKS refresh；已缓存 key 在 JWKS outage
  时继续验证；未知 key 在 outage 中 fail closed。
- `make p2-workload-identity-smoke`：client secret 错误返回 401，未授权 scope 返回 400；token
  audience/scope/issuer contract 通过。

## 不能宣称 live overlap 的原因

当前 `tools/workload-issuer` 是验收用 ephemeral issuer：每次进程启动生成一把 RSA key，client
credential 从环境变量一次性加载，没有双 key overlap、撤销时间窗或热 reload endpoint。P3-400
supervisor 启动的四 owner 也只在进程启动时读取各自 token/secret，尚无安全的 in-place reload
协议。因此不能把“重启 issuer”冒充 credential rotation，也不能声称旧 credential 在 overlap 后
自动过期。

证据文件会把 contract PASS 与 live SKIPPED 分开写入 `credential-rotation.json` 和
`p3-405-credential-rotation.json`。重试条件是先提供 reloadable issuer/JWKS 双 key overlap、owner
credential reload、旧 key/secret revoke deadline，再以同一四 owner topology 运行该 gate。
