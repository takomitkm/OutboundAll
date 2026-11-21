# FEATURE_TRACEABILITY — 参考特性 → 新栈映射矩阵

> Phase 0 产出（源规格 §3）。逐行回答：参考特性在新栈由**谁**实现（原生能力 or 新代码），
> 验收测试是哪条。语义区分（源规格点名的六对概念）在 §2 单列。
> 状态值：⬜ 未开始 / 🟨 进行中 / ✅ 完成（CI 证据） / ⚠️ 文档化偏差（ADR 链接）。

## 1. 主矩阵

| # | 参考特性 | 原实现 | 新栈实现载体 | 原生 or 新代码 | 验收测试 | 状态 |
|---|---|---|---|---|---|---|
| F1 | 节点身份持久（重启/升级不变） | `keys/tunnel.key` 本机生成 | sing-box `state_directory`（用户目录，HKCU ACL） | **原生**（tailscale machine key 持久化于 state 目录） | G5（CI：重启进程→Headscale 节点数不变）；T-identity | ⬜ |
| F2 | 一次性短期登记凭证 | ed25519 凭证 + registry（24h） | 邀请服务（hs-controller）：一次性邀请码 → 换短期 single-use preauth key | **新代码**（Headscale preauth key 为底层） | T-invite-*（原子/过期/重放/撤销/并发/崩溃） | ⬜ |
| F3 | 登记幂等（重装复用身份） | helper 按公钥指纹命中既有节点 | state 目录已存 → `auth_key` 不再消费（sing-box 原生语义）+ hs-controller 以节点 ID 归并 | **原生 + 新代码**（归并逻辑） | T-enroll-idempotent | ⬜ |
| F4 | 零管理员（无 UAC/服务/驱动/TUN/路由） | rootless sshd + HKCU Run | `system_interface: false` userspace endpoint + HKCU Run 启动 hs-node | **原生**（userspace 天然无 TUN/驱动） | T-priv-*（Windows 跑者：无提升令牌、无服务、无 TUN 接口） | ⬜ |
| F5 | 登录自启动 | HKCU Run / systemd --user / LaunchAgent | 同机制指向 `hs-node run`（包装 sing-box） | **新代码**（沿用 autostart 模式） | T-autostart（真实桌面会话标记 UNVERIFIED 沙箱部分） | ⬜ |
| F6 | 远程交互 shell | 节点 rootless sshd + manager key | sing-box `ssh_server.enabled`（tailnet :22）+ 控制端官方 tailscale ssh | **原生**（G4 验 ACL 生效） | T-ssh-interactive（PTY/UTF-8/退出码） | ⬜ |
| F7 | 远程一次性命令 | `controller exec`（参数数组 + bash stdin） | `tailscale ssh <node> <cmd>` / hs-admin exec 封装 | **新代码**（薄封装） | T-ssh-exec（stdout/stderr/退出码分报） | ⬜ |
| F8 | SFTP | 节点 sshd internal-sftp | sing-box ssh_server（`disable_sftp:false`）；Windows 跑者单用户语义 | **原生** | T-ssh-sftp（有则测，无则文档化） | ⬜ |
| F9 | 仅授权方可达 | relay permitlisten + 节点 AK 只有 manager 行 | Headscale SSH ACL（policy v2/grants）deny-by-default + 逐节点 tag | **原生**（Headscale）+ **新代码**（policy 管理） | G4 + T-authz-*（未授权节点拒/逐节点隔离/撤销阻断） | ⬜ |
| F10 | 撤销节点（旧 key 失效） | relay revoke（AK 行删 + 墓碑） | Headscale node expire/delete + ACL 移除 + 邀请撤销 | **原生**（Headscale）+ 新代码编排 | T-revoke-*（重连拒/不复活/邀请同撤） | ⬜ |
| F11 | 撤销终止活动会话 | 未明确承诺（AK 删即失效；已建隧道由 sshd 兜底） | G3 实测后按事实写：{立即终止 / ≤T 秒 / 保留至重连} | **实测裁决** | G3（CI 会话存活观察） | ⬜ |
| F12 | 会话管理（逐会话终止） | 无逐会话能力 | 若原生无逐会话 API → 文档化偏差（ADR-003） | **待 G3/G4 结论** | 按 ADR | ⬜ |
| F13 | 服务转发（把节点本机服务暴露给控制器） | `-R 127.0.0.1:<port>` 通道（permitlisten 逐口精确） | G1 裁决：sing-box SSH forwarding（全局开关，无逐服务强制）或文档化偏差 | **待 G1**（ADR-005） | 按 ADR | ⬜ |
| F14 | 逐服务授权（无任意转发） | permitlisten 白名单（服务端强制） | G1 若走 SSH forwarding：无服务端逐服务强制点 → 默认 `disable_forwarding:true` + 偏差记录 | **待 G1** | 按 ADR-005 | ⬜ |
| F15 | 多通道独立守护（一条失败不杀另一条） | 每通道独立 ssh 进程 + supervisor | tailscale 内部路径选择（单连接多路复用）——**语义映射变化**，失败隔离由 WireGuard 层保证 | **原生**（语义不同点写入 ADR-004） | T-net-*（见 §3 失败分类） | ⬜ |
| F16 | 重试梯 / boot-wait / auth 重退避 | supervisor ladder（5→60s→3600s） | hs-node supervisor 监视 sing-box 进程 + 网络探针，参数复刻 | **新代码** | T-ladder-*（参数矩阵逐档断言） | ⬜ |
| F17 | 实例锁 | supervisor.lock（LockFileEx/flock） | 同机制复用 `internal/lockfile` | **新代码**（复用现有包） | T-singleton | ⬜ |
| F18 | 日志脱敏 | logging 包 REDACT 模式 | 复用 + 新增 preauth key / node key 模式 | **新代码**（复用现有包） | T-redact | ⬜ |
| F19 | 运维 CLI（invite/node/shell/exec/status/doctor） | OutboundAll controller/relay 子命令 | `hs-admin`（调 hs-controller API + tailscale ssh） | **新代码** | T-cli-*（每命令一测） | ⬜ |
| F20 | 管理面持久化 | registry.json + AK 文件 | hs-controller SQLite（nodes/invitations/audit） | **新代码** | T-store-*（事务/索引/不存明文密钥） | ⬜ |
| F21 | DERP 中继回退 | （无对应物：relay 是唯一入口） | Headscale 内嵌 DERP；多 DERP = 配置产物 | **原生** | T-derp-failover（阻断 UDP→回退 DERP） | ⬜ |
| F22 | 控制面故障恢复 | boot-wait + ladder（探 relay） | 同语义：控制面不可达→ladder；恢复自愈 | **新代码** | T-cp-down（headscale 停→节点保持/恢复） | ⬜ |
| F23 | 迁移（旧版→新版） | migrate-legacy（AK 逐字节保留） | 旧 OutboundAll 节点 → 新栈：重新登记（身份体系不同，不能逐字节迁移——**与参考迁移语义本质不同**，ADR-002 说明） | **新代码**（并排期） | T-migration（并跑验收） | ⬜ |
| F24 | 备份/回滚 | AK .bak + registry 手动 cp | headscale db 备份 + hs-controller db 备份 + 恢复演练 | **新代码**（部署产物） | T-backup-restore | ⬜ |
| F25 | 审计 | registry 墓碑 + AK 备份 | audit_events 表（邀请/兑换/撤销/授权变更） | **新代码** | T-audit | ⬜ |
| F26 | doctor 只读诊断 | OutboundAll doctor | hs-admin doctor（平台/state/config/身份/自启/连通） | **新代码** | T-doctor | ⬜ |
| F27 | 对外仅一端口 | relay sshd 一个端口 | VPS 443/TCP（控制面+DERP）+ 3478/UDP（STUN） | **部署形态** | T-ports（部署文档 + 防火墙清单） | ⬜ |
| F28 | 开放开户口（bootstrap 18443，机主拍板） | HTTP /i 无 token | **不做**——新栈登记走邀请码；如需开放模式属机主决策，留 ADR 记录 | **偏差（设计决定）** | — | ⚠️ ADR-002 |

## 2. 语义区分表（源规格 §3 点名，逐对钉死）

| 概念对 | 参考实现中的事实 | 新栈中的映射 |
|---|---|---|
| 认证 key vs 持久节点身份 | 登记凭证（短期一次性）≠ tunnel key（持久，本机生成） | 邀请码（一次性）→ preauth key（短期单用）≠ tailscale 节点身份（state 目录持久） |
| 撤销登记 vs 撤销已登记节点 | credentials revoke（只删未消费凭证）≠ relay revoke（墓碑+删 AK+撤凭证） | 邀请撤销 ≠ headscale node expire/delete + ACL 移除；两者独立实现、独立测试 |
| 断开活动会话 vs 删除设备 | 参考无逐会话 API；删 AK 行使新连接失败，已建连接由 sshd 兜底 | G3 实测裁决会话终止时机；node delete ≠ 会话终止（除非测出） |
| DERP failover vs 控制面 failover | 参考：无 DERP；anchor 是 controller 侧多路径 | 新：DERP 故障切换（数据面）与 Headscale 控制面恢复（管理面）**独立**验证、独立文档，不互相声称为对方提供冗余 |
| 本地转发 vs 远程转发 | -L（permitopen=1 全拒）vs -R（permitlisten 白名单） | sing-box ssh forwarding 语义待 G1；两者在新栈中都无逐口白名单的话，按 ADR-005 记偏差 |
| shell 许可 vs 端口转发许可 | 参考：shell 能力=manager key（node AK）；转发能力=node key permitlisten——两把不同的 key | 新：SSH ACL（谁可 shell）与 forwarding 开关（能否转发）是两个独立控制点；测试必须证明各自独立生效 |

## 3. 失败分类九类 → 测试映射（源规格 §10）

| # | 失败类 | 参考实现行为 | 新栈测试 | 状态 |
|---|---|---|---|---|
| 1 | 直连 WireGuard 失败 | （参考无此层） | T-net-direct：正常场景（CI 内直连应成功） | ⬜ |
| 2 | 首选 DERP 失败 | — | T-derp-1：防火墙规则模拟主 DERP 不可达 → 次选接管 | ⬜ |
| 3 | 全 DERP 不可用 | — | T-derp-all：全部阻断 → 连接失败且按 ladder 重试（合法结局：断开+重试，非悬挂） | ⬜ |
| 4 | 控制面不可达 | boot-wait + ladder | T-cp-down：headscale 进程停 → 已建连接保持或按策略断开（G3 关联）；恢复后自愈 | ⬜ |
| 5 | DNS 失败 | — | T-dns：解析失败 → ladder（不刷风暴） | ⬜ |
| 6 | TLS 失败 | — | T-tls：证书无效 → 显式失败（不静默降级 http） | ⬜ |
| 7 | VPS 不可达 | ladder 全档位 | T-vps-down：整机不可达 → ladder + 成功清零 | ⬜ |
| 8 | 节点进程崩溃 | supervisor 自动重启 | T-crash：杀 sing-box → hs-node 重启之 → 身份不变（F1） | ⬜ |
| 9 | 既有 SSH 会话失败 | 通道级独立 | T-session-drop：会话中断 → 客户端可重连 → 不产生重复节点 | ⬜ |

## 4. 测试命名约定（writing-tests skill）

`T-<family>-<case>[-N]`，失败模式绑定见 analysis/FAILURE_MODES.md（Step 4 落地）；
每条负向用例的校准（红过一次）证据记 analysis/CALIBRATION.md。
