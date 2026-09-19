# P4-402 审批关联与 operator console

Record Hub 控制台增加“审批关联”只读页，按当前 tenant/workspace 查询 Tender↔Approver Application 安全投影，展示状态、generation、decision version、projection state 和更新时间，不展示 sealed bid 内容、金额、文件或完整流程快照。

Viewer 和 Operator 只能通过 GET 查看；Editor 仍不能从该页修改审批终态。投影 worker 是唯一写入方，HTTP 对 POST/PATCH/DELETE 返回 `405`，跨 workspace 返回 `403` 且不返回资源数据。前端构建和后端回归测试由 `P4-402-association-ui` 执行。
