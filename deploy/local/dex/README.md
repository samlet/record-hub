# 本地 Dex

本目录后续保存不含真实 secret 的 Dex 本地开发配置。

要求：

- issuer 在所有应用中完全一致；
- 四个 Web 应用使用独立 OIDC client；
- 每个服务调用方向使用独立 confidential client；
- 启用本地 client credentials 时设置 `DEX_CLIENT_CREDENTIAL_GRANT_ENABLED_BY_DEFAULT=true`；
- secret 只放 `.env` 或本地 secret 文件，不提交仓库；
- 在自动化测试中验证 discovery、JWKS、用户 code flow 和机器 token claims。

详细设计见 [认证方案](../../../docs/security-auth.md)。

