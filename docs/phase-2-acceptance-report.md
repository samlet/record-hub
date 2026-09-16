# Integration Beta P2-0 基线验收报告

日期：2026-09-17  
仓库：`github.com/samlet/record-hub`  
验收 commit：`d8de1f5134ff3f83e36d96a1e27e919347d66877`

## 结论

P2-0 的身份契约和隔离基础设施已通过；引擎基础已通过，但四业务进程接入尚未完成。因此
P2-0-006 保持 `PARTIAL`，浏览器角色、双引擎业务 Binding 和四系统故障矩阵按规则记为
`SKIPPED`，不能把本报告当作整套业务拓扑的 Beta 准入。

## 环境版本

| 组件 | 版本 |
| --- | --- |
| Go | `go1.27.1 darwin/arm64` |
| Node.js | `v26.8.2` |
| MongoDB | `v8.0.32` |
| NATS Server | `v2.14.6` |
| Dex | `2.45.1` |
| Temporal CLI/Server/UI | `1.7.0 / 1.31.0 / 2.49.1` |
| Conductor CLI | `v0.1.9` |

## PASS 证据

以下命令在验收 commit 上实际运行并通过。每个隔离脚本只使用表中专用端口和临时目录，
成功后清理自身进程与数据，不停止共享服务。

| 命令 | 结果 |
| --- | --- |
| `make p2-workload-identity-smoke` | PASS；Fluxion/Bids 独立 client，audience/scope/TTL 和错误凭据拒绝通过 |
| `make p2-isolated-core` | PASS；Mongo `37018`、NATS `14223`、human Dex `15566`、workload issuer `15557`、Record Hub API `18081` |
| `make p2-engine-foundation` | PASS；Temporal gRPC/UI `17233/18233`、Conductor HTTP `18080` 启动、健康探测和清理通过 |
| `make check` | PASS；gofmt/vet、Web typecheck/test/build、OpenAPI lint、Go tests、build |

详细隔离说明见 [deploy/local/isolated/README.md](../deploy/local/isolated/README.md)。身份边界
见 [ADR-0003](adr/0003-separate-human-and-workload-identity.md)。

## SKIPPED 与重试条件

### P2-0-007：Dex 浏览器角色矩阵

Dex discovery/JWKS、四个 confidential client 的 Authorization Code + PKCE 和 redirect/PKCE
负向已由 `dex-smoke` 覆盖；本批没有在真实 Chrome/Playwright 中执行 login、callback、logout，
也没有针对 OWNER/EDITOR/VIEWER 的撤权和跨租户浏览器矩阵。原因是 Record Hub 尚未提供可独立
启动的 membership provisioning 与三角色测试用户拓扑。

重试条件：完成角色/membership 测试数据 provisioning，使用隔离 Dex/API/Web 启动脚本，在真实
Chrome/Playwright 中执行三角色允许/拒绝、撤权立即失效和跨租户负向，并保存 trace/截图摘要。

### P2-0-008：双引擎 Binding live/restart

工作负载 token 合约与两个引擎本身已通过，但 Approver、Fluxion、Bids 和 Record Hub 尚未组成
可独立监督运行的业务拓扑，故未运行真实 workflow snapshot/replay/restart，也未验证 history
只含 ref/hash。

重试条件：完成 P2-0-006b，为四业务进程绑定隔离 Mongo/NATS、独立 workload client 和
Temporal/Conductor 地址；再执行真实 workflow 触发、Binding、重放和进程重启矩阵。

### P2-0-009：四系统 outage/restart/rotation

当前没有安全的四进程隔离启动方式，未对 producer backlog/recovery、全进程重启、JWKS outage、
client secret overlap/过期做 live 宣称。

重试条件：在 006b 拓扑上加入可控 outage 注入、双 key overlap/撤销、JetStream backlog 与
有界 drain，保存每次 run 的版本、指标和有界日志。

## 遗留风险与下一门

- P2-0-006 为 `PARTIAL`，P2-0-006b 是进入 P2-1 前的业务拓扑门槛。
- 本地 workload issuer 仅用于 local/CI；Beta/生产必须替换为正式 Authorization Server 或
  平台 workload identity，不能把临时 RSA key 当生产凭据。
- 本地 Mongo/NATS/Temporal/Conductor 均是开发/验收实例，不构成 HA、备份或 RPO/RTO 证据。
- 下一批应先冻结 membership provisioning、四业务进程连接契约和首个低风险 command owner，
  然后再实现 P2-1 Operable Data。
