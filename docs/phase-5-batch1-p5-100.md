# P5-100 Production OIDC 与 Service Identity

Record Hub 已增加 `RECORD_HUB_ENVIRONMENT` 生产安全边界：`production` 环境拒绝不安全 OIDC issuer、浏览器不安全 endpoint 和非 Secure cookie；local/staging 仍可使用本地 Dex 进行 contract smoke。OIDC verifier 继续校验 issuer、audience、expiry、subject，并使用 go-oidc remote JWKS 支持 signing-key rotation；machine policy 要求精确 issuer/subject/audience/scope/tenant/workspace，不接受 wildcard。

静态入口：`make p5-100`。当前 production config、OIDC verifier、exact service policy、browser boundary 和 Phase 5 contract 检查均 PASS；live rotation/revocation 为 `SKIPPED`，因为仍缺 production Dex/OIDC TLS、external secret manager、四个独立 owner principal 以及 overlap/drain/revoke evidence runner。

未完成项不能被本地环境冒充：需要设置 `RECORD_HUB_P5_IDENTITY_LIVE=1` 并接入受批准的 production identity harness 后，才能验收 rotation、in-flight drain、撤销和审计。
