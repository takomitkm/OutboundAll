# SECURITY_MODEL — OutboundAll × Headscale + DERP + sing-box（v1，Phase 1 冻结稿）

> 不变式来源：参考实现 `docs/SECURITY_MODEL.md`（三身份/负向矩阵）+ 源规格 §13。
> 任何实现 PR 触碰本文所列不变式时，必须重跑 §4 负向矩阵并附 CI 证据。

## 1. 三身份与信任边界

| 身份 | 生成/存放 | 撤销 | 断言（测试钉死） |
|---|---|---|---|
| 节点身份（tailscale node/machine key） | 节点本机生成，存 `state\tailscale\`，私钥不出机 | headscale node expire/delete + ACL 移除 | 撤销后重连被拒（N10'） |
| controller 身份（官方客户端，VPS） | tailscaled 持有 | 移除 controller 的 grants | controller 身份不能冒充节点 |
| 邀请码 → preauth key | hs-controller 生成（256-bit 随机，存哈希）→ 兑换时向 headscale 申请单用 preauth key | 邀请撤销 / 过期 / 一次性 | 过期/已用/撤销邀请不能注册（N9'）；邀请码不能建立 tailnet 连接（N8'） |

**分离断言**：邀请 ≠ preauth key ≠ 节点身份；三者的能力集互不包含。

## 2. 授权模型（deny-by-default，服务端强制）

- Headscale policy（policy v2 / grants）**默认全拒**；显式列出：controller→各节点 SSH、（若 G1 允许）受限转发。
- 逐节点 tag：`tag:OutboundAll-node-<name>`（或等价 per-node 映射）；授权 profile 决定该节点可被谁 SSH。
- sing-box `ssh_server` 的 forwarding 默认关闭（`disable_forwarding: true`）——等价于参考实现"节点 key 无 -L/-R" 的默认姿态；开启条件见 ADR-005。
- **授权边界由服务端裁决**（Headscale ACL 在数据面强制；SSH ACL 在连接时裁决）——客户端 UI/配置校验只是体验，不是安全边界。
- 参考实现的"permitlisten 逐口精确"在新栈**无逐服务等价物**（G1 待证）——任何文档不得宣称等价（ADR-005）。

## 3. 凭据纪律

1. 邀请码：≥256-bit 密码学随机；服务端**只存哈希**；短期过期（默认 24h，可配）；一次性；原子兑换；限速；审计。
2. preauth key：单用、短期、按兑换即时签发；用后即废；绝不出现在 hs-controller 日志/审计明文。
3. 节点身份：state 目录 ACL 收紧；升级/重启保留（G5）；**损坏→显式失败并给恢复流程，绝不静默重生成身份**（源规格 §5 要求）。
4. controller 身份：只在 VPS；泄露=最高级事件（同参考实现口径）。
5. 日志脱敏（复用并扩展 internal/logging）：`-----BEGIN`、`sk-*`、`Bearer `、公钥体、32+ hex、tailscale node key 前缀（`nodekey:`）、preauth key 前缀、邀请码形态（由 hs-admin 输出格式约束）。

## 4. 负向矩阵（N' 系列；每条绑定失效模式清单并在 CI 红绿校准）

| # | 断言 | 方法 | 状态 |
|---|---|---|---|
| N1' | 邀请码不能建立 tailnet 连接 | 只持邀请码尝试连接 → 失败 | ⬜ |
| N2' | preauth key 不能复用 | 同一 key 二次注册 → headscale 拒 | ⬜ |
| N3' | 过期邀请不能兑换 | 过期后 redeem → 403/拒绝 | ⬜ |
| N4' | 撤销邀请不能兑换 | revoke 后 redeem → 拒 | ⬜ |
| N5' | 并发兑换一次性 | 两路并发 redeem → 恰一路成功 | ⬜ |
| N6' | 撤销节点后重连被拒 | node expire 后 endpoint 重连 → 拒 | ⬜ |
| N7' | 撤销后未消费邀请不能复活节点 | revoke 后旧邀请 redeem → 拒（对应参考 N11/P1） | ⬜ |
| N8' | 未授权 tailnet 节点不能 SSH 目标节点 | deny-by-default ACL 下第三方节点 → 拒 | ⬜ |
| N9' | SSH ACL 只对 controller 放行 | 仅 controller 身份可达；其他节点全拒 | ⬜ |
| N10' | 转发默认关闭 | `disable_forwarding: true` 下 -L/-R 请求 → 拒 | ⬜ |
| N11' | 撤销节点不复活 | expire 后 preauth 再注册同身份 → 拒或归并为 revoked 态（G3/G5 关联） | ⬜ |
| N12' | state 损坏不静默重生成 | 篡改 state 文件 → hs-node 显式报错（退出码≠0），不自动重登记 | ⬜ |
| N13' | 管理面无鉴权访问被拒 | 无 token 调 hs-controller API → 401 | ⬜ |
| N14' | 邀请码不在日志/审计明文 | 全文检索日志 → 无明文邀请码 | ⬜ |

校准纪律（writing-tests Iron Law）：每条负向用例必须"红过一次"——先以放行配置证明用例能抓住失效，再收紧配置转绿；证据记 `analysis/CALIBRATION.md`。

## 5. 视界模型（源规格 §13"明确区分"的落地）

| 观察者 | 能看到 | 看不到 |
|---|---|---|
| Headscale | 节点公钥/身份、在线状态、注册元数据、ACL 匹配结果 | 端到端流量内容 |
| DERP | 连接元数据（谁在何时经它中继） | WireGuard 加密载荷 |
| WireGuard 对端（节点/控制器） | 双方 tailnet IP 的明文流量 | 第三方节点流量（除非 ACL 放行） |
| hs-controller | 它自己签发的邀请/节点记录/审计 | tailnet 流量（不在数据路径上） |
| Windows OS | 节点进程以**启动它的用户**运行（无提升） | —（不成为 SYSTEM/Administrator，N 权限测试验证） |

**不得宣称**：本系统 ≠ 参考 SSH-relay 的同一威胁模型；具体差异（DERP 可见性、Headscale 集中度）见 THREAT_MODEL.md。

## 6. 与参考实现的安全属性对照（防"组件名对上=等价"）

| 参考属性 | 新栈状态 |
|---|---|
| relay 拿不到节点私钥 | 保持（身份私钥不出节点） |
| 节点 key 无 shell（tunnel 身份） | 语义变化：新栈"隧道身份"与"SSH server"同宿主（sing-box），由 SSH ACL 强制谁能 shell——**机制不同，目标相同**；N8'/N9' 钉死 |
| 撤销立即失效 | G3 实测后如实写（不预设"立即"） |
| 零管理员 | 保持且更强（userspace 无 TUN/驱动） |
| 日志无密钥 | 保持 |
| 审计可追 | 增强（audit_events 结构化） |
