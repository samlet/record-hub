# 本地 Dex

本地开发使用 Dex 2.45.1、SQLite 持久化和密码数据库，为 Approver、Fluxion、Bids、Record Hub 注册四个彼此独立的 confidential Web client。issuer 固定为 `http://127.0.0.1:5556/dex`，避免不同应用对 `localhost`、容器名或端口形成不一致的身份主键。Record Hub client 同时注册独立的 Next BFF (`:3004`) 和可选 Go BFF (`:8080`) 精确 callback；部署时只启用实际拥有 callback 的一条路径。

## 启动与验收

首次启动先生成被 `.gitignore` 排除的本地随机凭据：

```bash
make dex-env
make dex-up
make dex-smoke
```

`dex-smoke` 会验证：

- OIDC discovery 与 JWKS 可访问且 issuer 一致；
- 四个 client 均能完成 Authorization Code + S256 PKCE；
- token endpoint 拒绝缺失或错误的 PKCE verifier；
- authorization endpoint 拒绝未注册 redirect URI；
- 每个 client 只能使用自己的精确 redirect URI。

测试用户及 client secret 由 `deploy/local/dex/.env.local` 提供。该文件只用于本机、不会提交；删除后重新运行 `make dex-env` 即可轮换全部本地凭据。停止服务会保留 SQLite named volume：

```bash
make dex-down
```

生产环境不得复用本地用户、client、HTTP issuer 或 SQLite 拓扑，必须使用 TLS、正式上游 IdP、受管 secret 和高可用 storage。详细边界见 [认证方案](../../../docs/security-auth.md)。
