# M7-070 权限边界负向矩阵验收

M7-070 已完成。Record Hub 的 workflow binding、projection summary 和记录存储边界均有
可重复的负向测试，拒绝结果不把敏感字段回显给调用方。

## 覆盖矩阵

| 边界 | 负向案例 | 预期 |
| --- | --- | --- |
| 用户 → binding create | 跨 tenant、跨 workspace | `identity.ErrForbidden` |
| 用户 → binding get | 已存在 snapshot 的跨 tenant、跨 workspace | `identity.ErrForbidden`，不暴露 snapshot |
| machine → binding create | 跨 tenant、跨 workspace、purpose、resource type、audience、identity | `ErrMachinePolicyDenied` |
| binding row | 请求 `recordRef` 与 adapter 返回的稳定行引用不一致 | `ErrRecordNotFound` |
| summary fields | Approver、Fluxion、Bids 的 contact/phone/报价/quotation/file URL 等字段 | deterministic reject，错误不包含字段值 |

实现测试：

- `server/internal/modules/binding/m7_authorization_matrix_test.go`
- `server/internal/modules/projection/summary_test.go` 中的 `TestM7SummaryFieldBoundaryRejectsSensitiveFieldsForEveryProducer`

## 运行验收

```bash
go test ./server/internal/modules/identity ./server/internal/modules/binding ./server/internal/modules/projection
go test ./...
```

## 边界说明

binding 只接受 `system:type:id` 的稳定引用和已经由 projection/record owner 过滤后的 JSON
对象；它不会把任意数据库查询或完整业务模型暴露给 workflow。字段级 allowlist 由三个
summary schema 的 `additionalProperties: false` 合同和 handler 执行，新增字段必须先发布
新的契约版本。Mongo record/source 查询始终同时带 tenant、workspace 和 source identity
条件。
