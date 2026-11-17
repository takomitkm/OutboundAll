# ADR-002 — 身份与登记：state 目录即节点身份；邀请码 → 单用 preauth key

状态：Accepted（G5 已实证，含一条设计注记）

## Context

源规格 §3/§6：必须区分"认证 key vs 持久节点身份"；登记密码less；崩溃在兑换与注册之间不留歧义重复节点。
参考实现用 ed25519 凭证 + registry 槽位（端口预分配）。新栈没有端口槽位概念，身份由 tailscale
协议的 node/machine key 对承载。

## Decision

1. **节点身份 = sing-box `state_directory` 内容**。节点本机生成身份；保留 state 重启/升级身份不变（G5 验证）；删除 state = 身份作废（重新登记）。
2. **登记凭证链**：`hs-admin invite create` 产出一次性邀请码（≥256-bit 随机，服务端只存哈希，默认 24h 过期）→ `hs-node enroll --code` 经 HTTPS 兑换 → hs-controller 校验后向 Headscale 申请**单用、短期 preauth key** → sing-box 用其注册 → hs-controller 记录 headscale node id ↔ 内部 node 记录映射。
3. 邀请码 ≠ preauth key ≠ 节点身份：三者的能力集互不包含（SECURITY_MODEL §1）。
4. **崩溃安全**：邀请状态机 `created → redeemed → registered → used|failed`；redeemed(未完成) 态允许同 enroll 会话 ID 续传一次，超时作废——不留歧义重复节点。
5. 参考实现的"开放开户口（无 token bootstrap）"**不迁移**：新栈登记必须持邀请码（偏差记录：设计决定，机主如需开放模式另开 ADR）。
6. 迁移语义变化（对参考 MIGRATION_GUIDE）：旧节点身份体系（OpenSSH key 对）与新栈（tailscale 身份）**不可逐字节迁移**——迁移 = 旧节点并排运行期间重新登记为节点，验证后下线旧隧道。这是身份模型变更的本质结果，如实记录。

## Consequences

- preauth key 短命且单用，泄露窗口极小（N2'）。
- 节点身份持久化依赖 state 目录完整性 → N12'（损坏显式失败，不静默重生成）。
- G5 证据（run 128b5f4）：**身份持久成立**——重启后同一 node id（无重复节点）；但 sing-box/tsnet **每次启动都需要 auth_key 才能恢复登录**（无 key 时进入 Waiting-for-authentication，state 目录里 tailscaled.state 明明存在）。
- **设计注记（已实现，run 74cb225 实证）**：hs-controller 提供了「resume-key」通道——`POST /api/v1/nodes/resume {hostname, node_secret}` 每次启动换一个短期 single-use preauth key。node secret 是登记时下发、节点侧 0600 落盘的机器凭据（服务端只存哈希）；能力只有「给自己换 key」一条。三身份分离保持：邀请码 ≠ node secret ≠ preauth key。**G5 real path PASS**：hs-node run 经 resume 通道重启后同一 headscale node id（无重复）；撤销后 resume 403（N6'/N7' PASS）。
