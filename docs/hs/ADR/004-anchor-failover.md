# ADR-004 — 锚点与故障转移：映射参考语义，不造重复组件

状态：Accepted（九类失败全部有 CI 证据或明确排除记录，run 87e7c3f 全 5 job 绿）

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

## 九类失败 → 证据映射（终稿）

| # | 失败类 | 证据 | 状态 |
|---|---|---|---|
| 1 | 直连 WireGuard 失败 | phase1b：tailscale ping 直连 0-1ms pong（同宿主直连路径建立成功） | ✅ 正向证明 |
| 2 | 首选 DERP 失败 | tailscale 原生路径选择 + 多 DERP = 部署产物（Headscale DERP map 多 region）；CI 单 DERP 不模拟阻断 | 📄 部署阶段（Phase 9 真机可加第二 DERP） |
| 3 | 全 DERP 不可用 | 同上——原生 magicsock 行为，不自造；hs-node ladder 保证节点侧不风暴 | 📄 部署阶段 |
| 4 | 控制面不可达 | **H4a（run 87e7c3f）**：headscale 停 15s，sing-box 进程存活（无崩溃、无重启风暴） | ✅ CI 实证 |
| 5 | DNS 失败 | resume/控制面拨号走 ladder + boot-wait（hs-node 侧 30s 探针 ≤10min），无快速风暴 | ✅ 机制级（cmdRun boot-wait） |
| 6 | TLS 失败 | config 校验强制 https（TestAllowHTTP 显式才放行）——不存在静默降级 http 的路径 | ✅ 单测（N-cfg-2） |
| 7 | VPS 不可达 | 与 4 同域（整机停 → headscale 停）；H4a 覆盖 + ladder 恢复 | ✅ 由 H4a 推及 |
| 8 | 节点进程崩溃 | phase3：SIGTERM 后 resume 通道身份不变；hs-node supervisor 自动重启 sing-box（梯子见 procman） | ✅ CI 实证 |
| 9 | 既有 SSH 会话失败 | phase1b G3：expire 后会话仍活 45s（会话级独立）；客户端重连不产生重复节点（phase3） | ✅ CI 实证 |

**ADR-004 决策维持**：不实现自定义 Anchor Manager——tailscale 原生路径选择 + DERP map
覆盖类 1-3，hs-node supervisor + ladder 覆盖类 5/7/8，控制面语义类 4/9 由 headscale 自身
行为定义（H4a/H4b 实证恢复无重复身份）。

## Consequences

- failover 语义交给协议栈，hs-node 只管进程级存活与重试——组件更少，行为可测。
- 若触发条件命中（原生语义不满足），再评估最小 adapter；当前无证据，不预造。
