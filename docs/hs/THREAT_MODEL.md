# THREAT_MODEL — OutboundAll × Headscale + DERP + sing-box（v1 初稿，Phase 10 终稿）

> 状态：初稿（Phase 1）；Phase 10 按"威胁清单逐条落地状态"补全并冻结。
> 方法：STRIDE-lite + 源规格 §13 威胁清单逐条覆盖；每条给：防线 / 现状（设计/实现/测试）/ 残余风险。

## 1. 资产与信任根

| 资产 | 位置 | 失守后果 |
|---|---|---|
| 节点身份私钥 | 节点 state 目录 | 该节点可被冒用（限其 ACL 能力） |
| controller 身份 | VPS tailscaled | 全部节点 shell 访问权（最高级） |
| hs-controller admin 凭据 | VPS | 可签发邀请/撤销节点/改授权 |
| Headscale 私钥与 DB | VPS | 可伪造节点身份/改策略（= 全局接管） |
| 邀请码（瞬时） | 传输/剪贴板 | 在有效期内注册一个新节点 |

信任根：Headscale 是身份与策略的唯一裁决者——**Headscale 失守 = 全部失守**（与参考实现"relay 失守但拿不到私钥"不同，见 §3 C5）。

## 2. 威胁清单（源规格 §13 全部 16 条）

| # | 威胁 | 防线 | 现状 | 残余风险 |
|---|---|---|---|---|
| T1 | 窃取邀请 | 短期+一次性+服务端存哈希+限速+审计 | 设计 | 有效期内泄露者可注册一个节点（能力受 deny-by-default 限制） |
| T2 | 重放邀请 | 原子兑换 + used 标记 + 并发测试（N5'） | 设计 | — |
| T3 | 窃取节点 state | 用户 ACL 收紧；泄露者能力=该节点 ACL | 设计 | ACL 内能力全部失守（如 SSH server 上可执行命令） |
| T4 | 窃取 controller 凭据 | 仅存 VPS；轮换流程（Phase 9 文档） | 设计 | 全部节点 shell |
| T5 | Headscale 失守 | 单点=信任根；备份/恢复；磁盘加密建议 | 设计 | 全局接管（身份伪造/策略篡改）——**与参考实现差异最大的点**，如实记录 |
| T6 | DERP 失守 | DERP 只见元数据不见载荷（WireGuard 端到端加密） | 设计 | 可中断中继（DoS），不可解密 |
| T7 | VPS 失守 | 包含 T4+T5+hs-controller 全部 | 设计 | 全失（等同参考实现的 relay 失守+管理面失守） |
| T8 | 未授权 tailnet 节点 | deny-by-default + 注册门槛（需邀请） | 设计 | — |
| T9 | 恶意已登记节点 | 逐节点授权隔离；节点间默认不可达 | 设计 | 恶意节点能力限于自身 profile |
| T10 | 跨节点横向移动 | ACL 默认无节点间通路 | 设计 | — |
| T11 | SSH 转发滥用 | `disable_forwarding: true` 默认；开启需 profile 显式（ADR-005） | 设计 | 开启后无逐服务强制点（G1 结论决定边界） |
| T12 | 意外提权执行 | Windows 权限测试（无 UAC/无提升令牌/ssh 会话=启动用户） | 设计 | — |
| T13 | Windows 用户会话终止 | 登录会话边界如实记录（pre-logon 不在范围） | 设计 | 未登录=不可达（设计接受） |
| T14 | 撤销节点重连 | node expire + ACL 移除 + 邀请作废（N6'/N7'/N11'） | 设计 | — |
| T15 | 服务暴露（转发面） | 默认关；显式 per-profile 开启；目标地址白名单（若 G1 允许） | 设计 | 同 T11 |
| T16 | 日志/备份泄漏 | 脱敏（N14'）+ 备份最小化 + 备份加密建议 | 设计 | — |

## 3. 服务器失守场景分级（源规格要求明确陈述）

| 场景 | 暴露元数据 | 扰动服务 | 改变授权 | 影响连通性 |
|---|---|---|---|---|
| C1 单个 DERP 失守 | 该中继连接元数据 | 该中继流量（回退其他 DERP） | 否 | 局部/暂时 |
| C2 全 DERP 失守 | — | 直连仍可 | 否 | 仅直连可用 |
| C3 hs-controller 失守 | 邀请/节点记录/审计 | 管理操作不可用 | **是**（若 admin 凭据失守） | 数据面不受影响 |
| C4 Caddy 失守 | TLS 终结层 | 控制面+DERP 不可达 | 否 | 新连接失败；已建连接按 G3 |
| C5 Headscale 失守 | 全部身份与策略 | 控制面全失 | **是（全局）** | 新连接失败；身份可被伪造 |

对照参考实现：参考 relay 失守 = 拿到公钥与 registry（无有用性）；新栈 Headscale 失守 = 身份体系接管。**这是架构升级换来的代价（自托管控制面）**，不是等价替换——本文件不粉饰。

## 4. 权限模型声明（Windows）

- ssh_server 会话 = 启动 sing-box 的用户（标准用户）；Windows CLI 拒绝其他本地账户（sing-box 文档明示）→ 无跨账户访问。
- 无 UAC 提升路径；无服务安装；无 TUN；无系统路由修改——全部由权限测试（Phase 11 T-priv-*）在真实 Windows 跑者断言。
- pre-logon 访问不在范围（源规格 §2）。

## 5. 终稿：威胁清单逐条落地状态（Phase 10，run 链见 ledger）

| # | 威胁 | 现状 | 证据 |
|---|---|---|---|
| T1 | 窃取邀请 | 实现+测试：短期（默认24h TTL）+一次性+服务端存哈希+限速 6/min/IP+审计 | TestInvitationLifecycle, TestRedeemRateLimited, N14'-analog TestStoreNeverHoldsPlaintextSecrets |
| T2 | 重放邀请 | 实现+测试：原子兑换（并发竞速恰一路成功） | TestRedeemAtomicUnderConcurrency (N5') |
| T3 | 窃取节点 state | 实现：state 目录 0700/icacls 加固 + node secret 0600；跨平台单测 | N-cfg 系列, phase3 e2e |
| T4 | 窃取 controller 凭据 | 实现：sha256-only 存储 + token-rotate 即时失效（CI 抓出并修复 Options 快照漏洞）+ CLI 三级解析 | TestHealthAndRotate (739d631) |
| T5 | Headscale 失守 | **如实记录为架构代价**：自托管控制面=身份信任根（ADR-001/THREAT_MODEL §3 C5）；备份=OPERATIONS §4 | 设计文档 |
| T6 | DERP 失守 | 原生 WireGuard 端到端加密，DERP 仅见元数据 | 协议保证 |
| T7 | VPS 失守 | =T4+T5+管理面全失；管理面 HTTP 只绑回环（OPERATIONS §5 端口清单） | deploy.sh |
| T8 | 未授权 tailnet 节点 | 实现+测试：注册需邀请→preauth 单用；deny-by-default policy | TestPhase4Chain (N8'/N9') |
| T9 | 恶意已登记节点 | 实现+测试：逐 profile 隔离（no-ssh 节点对控制器黑洞）| G4 negative (run 74488ae) |
| T10 | 跨节点横向移动 | 实现+测试：grants 无节点间通路；节点对节点默认零规则 | policy 渲染器（grants 空数组基线） |
| T11 | SSH 转发滥用 | 实现+测试：ssh_server disable_forwarding=true 默认（结构性配对校验拒绝矛盾配置）| N-cfg-3/N-cfg-4 |
| T12 | 意外提权执行 | 设计：userspace 无 TUN/驱动；sing-box 拒绝其他本地账户（上游语义）；Phase 11 真机权限测试待做 | sing-box 上游文档 |
| T13 | Windows 会话终止 | 设计声明：登录会话内活，pre-logon 不在范围 | ARCHITECTURE §9 |
| T14 | 撤销节点重连 | 实现+测试：headscale expire + ACL 移除 + 邀请作废 + resume 403 | N6'/N7' (phase3 e2e) |
| T15 | 服务暴露（转发面） | 实现+测试：**双闸**——headscale grant(WHO) × node route allowlist(WHAT, reject-all 兜底)；未列端口黑洞 | G1 gates (run fad7a51) |
| T16 | 日志/备份泄漏 | 实现：logging 脱敏（N14'）；**安全发现**：控制器 API 401/403 响应等无密钥输出；备份含 db+token，权限 0600（OPERATIONS §4）| TestRedact 系列 |
| T17 | derper 公开中继滥用（ADR-007 新增） | 设计：derper 不验证客户端（headscale 无 DERPAdmitClientRequest 准入端点，`-verify-clients` 需本地 tailscaled）→ 443 开放后任何人都可借道转发密文；缓解 = `-accept-connection-limit/burst` 限流 + 安全组闸门 + 中继只见元数据（同 T6 协议保证） | ADR-007 D5；OPERATIONS §7 |

**净安全发现（本重构期间 CI 抓出并已修）**：
1. sing-box 裸默认把 tailnet 入站转发到同端口回环（任意回环暴露）→ reject-all 兜底（ADR-005）
2. token 轮换后旧 token 经 Options 快照仍有效 → adminAuth 改读 store 当前 sha（739d631）
3. windows lockfile 包级 mutex 使阻塞 Acquire 死锁 → 删 mutex（潜藏在 autocrlf 修复后首跑）
4. 凭证容器/JSON 双层自证（fromSeed 派生 + public_key_openssh 对拍）——传递链换半在花掉一次性凭证前本地拦截
