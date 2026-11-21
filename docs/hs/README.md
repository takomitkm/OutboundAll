# docs/hs — 文档索引（Headscale + DERP + sing-box 重实现）

> 参考实现（现行 Go 版）的文档在 `docs/` 根；本目录是重实现的全部文档。
> 执行正本：根 `PLAN.md`；台账：根 `IMPLEMENTATION_PROGRESS.md`。

## 按读者分组

| 我要… | 读 |
|---|---|
| 理解整体架构 | [ARCHITECTURE.md](ARCHITECTURE.md) |
| 审计安全边界 | [SECURITY_MODEL.md](SECURITY_MODEL.md)（N' 负向矩阵） |
| 评估威胁模型 | [THREAT_MODEL.md](THREAT_MODEL.md)（16 威胁 + 4 净发现） |
| 登记一台节点 | [ENROLLMENT.md](ENROLLMENT.md) |
| 部署 / 升级 VPS | [OPERATIONS.md](OPERATIONS.md) |
| 看验收矩阵 | [TEST_PLAN.md](TEST_PLAN.md) |
| 看最终对拍报告 | [FINAL_REPORT.md](FINAL_REPORT.md) |

## ADR（决策记录）

| ADR | 主题 | 状态 |
|---|---|---|
| [001](ADR/001-network-architecture.md) | 网络架构（控制面+DERP+userspace 节点） | Accepted（G2 实证） |
| [002](ADR/002-identity-and-enrollment.md) | 身份与登记（三身份、resume 通道） | Accepted（G5 实证） |
| [003](ADR/003-per-node-authorization.md) | 逐节点授权（deny-by-default、tag 语义） | Accepted（G3/G4 实证） |
| [004](ADR/004-anchor-failover.md) | 锚点/故障转移（九类失败映射） | Accepted（H4a/H4b 实证） |
| [005](ADR/005-service-forwarding.md) | 服务转发（双闸模型、reject-all） | Accepted（G1-a 实证） |
| [006](ADR/006-ssh-server-security-gate.md) | SSH server 安全门控（fork 版本缺口） | 门控生效中 |
| [007](ADR/007-role-transport-assignment.md) | 按角色分配传输组件（受控端 sing-box，其余上游原版；derper 自签+指纹 pin 取代"必须域名"） | Accepted |
| [008](ADR/008-data-plane-degradation.md) | 数据面降级阶梯（直连不可行/中继为默认/DERP-over-SSH/纯 SSH） | Accepted（A 实测不可行） |
