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
- **设计注记**：hs-controller 必须提供「resume-key」通道——节点每次启动时以某种可验证身份（注册时下发的节点密钥）向 hs-controller 换取一个短期 single-use preauth key。这不破坏三身份分离（resume key ≠ 邀请码 ≠ preauth key 本体），但把「登记一次性」的语义精确化为「登记身份一次性 + 启动恢复走受控通道」。已在 ADR-002 登记，Phase 3 实现时落实。
