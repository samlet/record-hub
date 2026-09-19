# P4-300：Bids 招标准备发布 approval contract

- 状态：`PARTIAL`（静态 PASS，live SKIPPED）
- Approver commit：`41ae520`
- Bids commit：`8a1ff5f`

冻结 `bids.tender-publication-approval.requested` v1 与对应 result contract。Approver validator 对根字段、引用、版本、hash、workflow、requester 与安全 proposal 做 fail-closed allowlist；报价金额、sealed 数据、unknown/oversize 字段没有进入 snapshot。两仓库 schema 与 fixture 由 `make p4-batch3` mirror gate 校验。
