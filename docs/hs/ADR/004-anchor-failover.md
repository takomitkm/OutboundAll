# ADR-004 — 锚点与故障转移：映射参考语义，不造重复组件

状态：Accepted（九类失败映射的测试在 Phase 7/11 落地）

## Context

参考实现的「anchor」= **controller 侧多路径连接锚点**（用户自己的 ssh_alias + dialhome 经 relay
路径，按序 failover）——不是 relay 冗余、不是多区域 DERP。源规格 §7 要求：先确定 anchor 到底是
什么，再决定要不要自造 Anchor Manager；不要实现原生已保证的 failover。

## Decision

1. 参考的「anchor failover」在新栈**没有直接等价物也没有必要**：tailscale 协议栈内部完成
   直连→DERP 路径选择（延迟探测 + 回退），controller 与节点同 tailnet 即可达。
2. 源规格 §10 九类失败逐条映射：
   - 1（直连失败）/ 2（首选 DERP 失败）/ 3（全 DERP 失败）→ tailscale 原生路径选择 + 多 DERP 配置（Headscale DERP map）；测试在 CI 以防火墙规则模拟。
   - 4（控制面不可达）/ 5（DNS）/ 6（TLS）/ 7（VPS 不可达）→ 已建连接按 G3 事实；新连接由 hs-node ladder 重试（复刻参考重试梯参数）。
   - 8（节点进程崩溃）→ hs-node supervisor 重启 sing-box。
   - 9（既有 SSH 会话失败）→ 客户端重连；不产生重复节点（G5 关联）。
3. **不实现自定义 Anchor Manager**，除非 CI 实测证明原生路径切换不满足上述语义（触发条件写死：任一映射测试无法在原生语义下通过）。
4. 多 DERP = 部署配置产物（Headscale DERP map 多 region），初版单 VPS 单 DERP；HA 分级见 ADR-001。

## Consequences

- failover 语义交给协议栈，hs-node 只管进程级存活与重试——组件更少，行为可测。
- 若触发条件命中（原生语义不满足），再评估最小 adapter；当前无证据，不预造。
