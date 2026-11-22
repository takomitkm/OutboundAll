# ADR-003 — 逐节点授权：deny-by-default policy + 逐节点 tag + SSH ACL

状态：Accepted（G3/G4/G4p 已实证；tag 选择器语义经 CI 对照实验钉死）

## Context

源规格 §4/§7：参考实现的授权语义不是全局 allowlist——每节点独立记录、独立 profile、独立撤销。
Headscale 的 ACL/grants/SSH policy 是授权载体。参考实现的"permitlisten 逐口白名单"是**服务端强制**
的逐服务授权；新栈的转发面没有直接等价物（G1 → ADR-005）。

## Decision

1. **Headscale policy 全局 deny-by-default**。不依赖"省略 ACL 段 = 全通"的隐式默认——policy 文件显式写拒绝基线。
2. 逐节点身份标识 = tag 体系（注册时由 hs-controller 按 profile 下发：默认 `tag:outboundall-node`+`tag:outboundall-node-ssh`，no-ssh profile 仅前者）；controller 身份 = `tag:outboundall-controller`（**必须真持 tag**——见 4a）。
2a. **CI 对照实验钉死的 headscale v0.29.7 语义**（run 74488ae 及前身）：
   - peer 邻接（数据面可见性）= grant src/dst 对**节点实际持有的身份**（IP/tag）做匹配；grants 的 src 是 tag 而节点未持该 tag ⇒ 节点不进对方 netmap（`Peer: null`），SSH 规则写得再对也无用——**SSH 需要双闸：grants 承载数据面 tcp:22，ssh 段只管会话授权**；
   - grants 的 ProtocolPort 语法是**冒号** `tcp:22`（斜杠 `tcp/22` 是 Tailscale-ACL dst 语法，解析报 "invalid first integer"）；
   - policy v2 的 tagOwners 值需尾缀 `@` 的用户引用（`ci@`，resolveUser 剥后缀）；ssh 规则 src 不接受 IP Prefix；
   - `headscale policy check --file` 可离线校验渲染产物（e2e 已前移断言）；
   - 曾疑为上游 tagged-netmap bug（#2375/#2389），对照实验证明是上述身份匹配语义 + syncPolicy 时序问题，**不构成上游缺陷报告**。
3. 授权 profile（hs-controller 内部记录）决定：哪个 controller/成员可 SSH 该节点；映射到 Headscale SSH ACL（`action: accept, src: [controller], dst: [<node-tag>:22]`）。
4. **默认姿态**：仅 controller→节点 SSH；节点间零通路；转发关闭（ADR-005）。
5. 撤销分层：
   - 撤销邀请（revoke invitation）：只作废未消费邀请；
   - 撤销节点（revoke node）：ACL 覆盖移除 + headscale node expire + 墓碑 + 名下邀请作废；
   - 会话终止时机 = **G3 实测裁决**，文档按事实写，不预设"立即踢下线"。
6. 逐会话终止：若 Headscale/客户端无逐会话 API → 文档化偏差（能力缺口如实记录，不宣称）。

## Consequences

- 授权变更（新 profile / 撤销）是控制面操作，立即影响新连接（已建会话由 G3 定）。
- 逐节点隔离可测：N8'/N9' 证明"未授权节点连不了"与"只有 controller 连得了"。
- G3 证据（run 128b5f4）：**node expire 不终止已建立的 SSH 会话**——会话在 expire 后 45 秒仍然存活。撤销语义如实记录为：新连接被拒（N6'），既有会话不立即终止（与参考实现「删 AK 行后 sshd 兜底」行为一致）。逐会话终止 = 明确的文档化偏差。
- G4p 证据（run 128b5f4）：**SSH ACL 生效**——官方 tailscale 客户端经 Headscale SSH ACL 成功登入 sing-box ssh_server，会话以 sing-box 进程用户运行（`whoami` = 启动用户，Windows CLI 拒绝其他本地账户的语义成立）。
- G4 证据（run 74488ae，最终形态）：deny-by-default 服务端强制成立——node-b（no-ssh profile，仅 tag:outboundall-node）对 ctrl 不可达（nc 被 ACL 黑洞，exit 1）；node-c（default profile，含 tag:outboundall-node-ssh）可被 ctrl 到达 tcp:22（tagged grant 生效）；策略文件始终保有 tag-scoped 形状（node-b 永不出现在 grant dst）。deny 基线 = grants 空数组（无 ssh 节点时）/ tag-scoped 单条（有 ssh 节点时），"省略段 = allow-all" 的隐式默认从未被依赖。
