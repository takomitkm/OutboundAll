# RUST_CHAIN_DRAFT — dialhome Rust 链草案（R0，待拍板）

> 状态：**草案**，未 commit、未 push；拍板后才谈入树。
> 基线：`main @ 5f708ba`（2026-11-18，Phase 6a 在飞；当天 tip 从 f86887c 推进到 5f708ba，有并行会话在同一工作树，本文只新增文件、不动别人改动）。
> 事实核实日期：2026-11-18，全部走 GitHub REST API / crates.io API（浏览器未开）。
> 语言口径：「rust 链」= 换**我们自己写的代码**的实现语言；headscale / sing-box / Caddy / tailscaled 是钉定版本的外部二进制，语言对它们没有意义，不在"换链"范围。

---

## 0. 一句话结论（拍板点前置）

1. **SSH server**：OxiSH 现在上不了 Windows 节点（上游 #252 open，"looking for a Windows contributor"），不能当主力。工程可落地的是 **russh 内嵌 hs-node**（Eugeny/russh，0.65.2，昨天还在发版，支持 mlkem768x25519 后量子 KEX）。OxiSH 进观察名单。
2. **store**：现行 hs-controller 的存储**不是 SQLite，是 JSON 文件**（registry.json + flock + 原子写 + 时间戳备份，偏差已记录在案）。redb 的定位是「Rust 版 store 的默认选型」——省掉把 Go 版那套自研持久层在 Rust 里复刻一遍的成本，不是"换 SQLite"。
3. **权限边界**：cap-std 是**程序内路径纪律 + 防路径逃逸（CWE-22）**，不是沙箱；OS 层收紧（icacls/0600）照旧自己 做。它支持 Windows（Wasmtime WASI 的底座），但仓库 2026 年已从 bytecodealliance org 迁到 sunfishcode 个人账户——单一维护者风险要记。
4. **顺序**：建议 **P6 + P7 在 Go 上收口（语义裁决必须先落地，它决定 Rust 版的接口形状），P8 起切 Rust**。验收线 = 过同一套语义 e2e（`hs/internal/hstest` 保留当 oracle）。
5. **最大的白捡收益**：russh 内嵌 + 关掉 sing-box `ssh_server`，等于把 ADR-006 门控里那条「内嵌 fork 低于 1.104.4 安全阈值」的攻击面**整个移出暴露路径**——比等 sagernet fork 跟进更主动。

### 0.1 路线对照（本草案记录的几条路）

| 路线 | 内容 | 前提/状态 | 本草案立场 |
|---|---|---|---|
| **russh 路**（推荐主力） | hs-node 内嵌 russh 当节点 SSH server；关 `ssh_server`；:22 显式路由规则 → 127.0.0.1 | 门 R-SSH-1（ADR-005 终稿裁决）+ R-SSH-2（客户端兼容实测）；PTY=ConPTY 接线 | 主线，R6 落地；落地即解除 ADR-006 门控 |
| **OxiSH 路**（观察） | OxiSH 当节点 SSH server（Linux 节点候选，或等 Windows 支持） | 上游 #252（Windows）未支持；#338（exec+pty）/#251（sftp）/#261（forced commands）均 open | 不当主力；#252 合入后重评分工 |
| **现状路**（保底，不换 SSH） | sing-box `ssh_server` 继续；等 sagernet fork 基线 ≥1.104.4（ADR-006 解锁路径 a）或机主书面接受（路径 b） | 门控生效中（`disable_forwarding: true` + deny-by-default） | 保底姿态；与 russh 路在 :22 上互斥（ssh_server 在路由器前认领 :22），二选一 |
| **数据面 Rust 路**（远期观望） | boringtun 等替代 sing-box 数据面 | 无 Tailscale 控制协议/DERP 客户端/netmap，工程量不成比例 | 明确不在范围；社区出现成熟 Rust 原生客户端再评 |

---

## 1. 事实底座（2026-11-18 逐项实查）

### 1.1 OxiSH（djc/oxish）

| 项 | 值 | 出处 |
|---|---|---|
| 语言 / 许可 | Rust / MIT + Apache-2.0 双许可 | README badge（repo API 只报 Apache-2.0） |
| 规模 | 384 星；22 open issues；建库 2025-01-08；最后推送 2026-11-17 | GitHub API |
| 自我定性 | **"currently a minimum viable product and not yet ready for production use"** | README 原文 |
| 平台 | **仅 Linux/macOS**——"Only supports Linux and macOS for now (looking for a Windows contributor)" | README 原文 |
| 认证 | **仅公钥认证** | README 原文 |
| 算法 | KEX：`mlkem768x25519-sha256`（RFC 10042）+ `curve25519-sha256`；主机钥 ed25519 / ecdsa-p256；加密 aes128-gcm@openssh.com；客户端要求 OpenSSH 9.9+ | README |
| 会话能力 | 基础 exec + PTY 已实现（#100 closed、#194 降权 closed）；**带 pty-req 的 exec 未支持（#338 open）**；**scp/sftp 无（#251 open）**；forced commands 部分落地（#286 closed，#261 仍 open） | issues 列表 |
| Windows | **#252 open**（"Add support for Windows"），另有一个简化实现提案 #292 open | issues |

定性：质量信号好（djc 是 rustls 的共同维护者——背景事实，未另行核实；算法面现代、PQ 优先、sans-I/O 结构清晰），但对 dialhome 的**主节点 Windows 是硬缺席**，MVP 阶段也不该当生产件。

### 1.2 redb（cberner/redb）

| 项 | 值 | 出处 |
|---|---|---|
| 版本 | **v4.6.0（2026-11-13 发布）**，最后推送 2026-11-17 | GitHub API |
| 规模 | 4943 星；crates.io 累计下载 16.4M；Apache-2.0 | GitHub/crates.io API |
| 形态 | 纯 Rust 嵌入式 KV：CoW B+tree、MVCC（并发读不阻塞写）、ACID、崩溃安全、单文件、无服务进程 | README（贴文描述属实） |
| 与我们不变式的交集 | **4.6.0 刚修**："Fix repair falling back to a commit slot that failed its checksum, which could open a damaged database as an empty one"——损坏库被当空库打开，正是我们「state 损坏不得静默重生成身份」那一类失败 | v4.6.0 release notes 原文 |

定性：成熟且活跃，可用。引入时**版本底线钉 ≥4.6.0**。注意 major 迭代快（1→2→3→4），升级要有说明。

### 1.3 cap-std（原 bytecodealliance/cap-std，现 sunfishcode/cap-std）

| 项 | 值 | 出处 |
|---|---|---|
| 归属 | **已从 bytecodealliance org 迁移到 sunfishcode 个人账户**（API 301 重定向到 repositories/275013363 证实）；843 星；最后推送 2026-10-19 | GitHub API |
| 形态 | 能力式 std：`cap-std::fs/net/time/rand`、`cap-tempfile`、`cap-fs-ext` 等；防 **CWE-22 路径逃逸**；是 **Wasmtime WASI 实现的底座** | README 原文 |
| 平台 | "portable std-like API which supports Windows **in addition to** Unix-like platforms"——Windows API 层面支持 | README 原文 |
| 诚实边界 | **不是沙箱**：`Dir::open_ambient_dir` 是官方留的 ambient 逃生口；进程内 `unsafe`/直调 `std` 不受限；OS 级 ACL 不归它管 | README + 贴文末段（贴文自己承认，属实） |

定性：思想值得采用，作用是**结构上让代码只能经能力句柄摸文件** + 路径逃逸防护 + 让"拒绝逃逸"的负向测试天然可写。别当沙箱卖。

### 1.4 russh（Eugeny/russh，原 warp-tech/russh）——本草案的实际 SSH 主力

| 项 | 值 | 出处 |
|---|---|---|
| 版本 / 活跃 | **0.65.2（2026-11-16 推送）**；1945 星；Apache-2.0；crates.io 下载 10.8M | crates.io / GitHub API |
| 形态 | **SSH 服务端+客户端库**（不是成品 server），纯 Rust / tokio | README |
| KEX | **`mlkem768x25519-sha256`（后量子混合）** + curve25519 系列 + dh-gex | README |
| 与 OxiSH 的分工 | russh 是**库**：session/exec/auth handler 完全自己写——"deny-by-default 写在代码里"比成品 server 的配置面更贴我们的模型；Windows 可编译（纯 Rust，CI 待实证） | 推断+待实证 |
| 归属变更注意 | warp-tech → **Eugeny**（Tabby 终端作者）。活跃，但归属变更后要盯发版节奏 | GitHub API 重定向 |

### 1.5 boringtun（Cloudflare）——观望名单

0.7.1（2026-05 更新），214 万下载。只有 WireGuard 加密/路由原语：**无 Tailscale 控制协议、无 DERP 客户端、无 netmap**。替代 sing-box 数据面 = 自建整套 Tailscale 客户端协议栈，工程量不成比例。**不进本草案范围**；除非社区出现成熟的 Rust 原生 Tailscale/Headscale 客户端（目前未核实到，不点名）。

### 1.6 与贴文（ChatGPT 那份）的出入对照

| 贴文说法 | 核实结果 |
|---|---|
| OxiSH「Linux/macOS only、仅公钥、无 SFTP、MVP」 | **属实**（README 原文逐条对应） |
| OxiSH 出处引 "Trifecta Tech Foundation" | README 未提 TTF，本草案不采信该出处 |
| redb 功能描述 | **属实**；但贴文配图是无关的 OpenAI 静态书图（《Efficient MySQL Performance》），忽略 |
| redb 用途示例表里的「Anchor」 | **不要带回来**——现行 PLAN D8 已明确不引入自定义 Anchor Manager，锚点语义=DERP failover+控制面 URL 两层 |
| cap-std「支持 Windows」 | 属实（API 层面）；仓库已迁到 sunfishcode 个人账户（贴文链接还是旧地址） |
| cap-std「不是完整 sandbox」 | 属实，同我们的定性 |
| 三者「不同层、可组合、互不替代」 | 同意 |
| 建议优先级「OxiSH 研究优先」 | **不采纳**：dialhome 主节点是 Windows，OxiSH 现在上不去。实际优先级 = russh（可落地）> redb（Rust 版 store 默认）> cap-std（访问层纪律） |

---

## 2. 接入点分析（现行栈哪里有钩子）

### 2.1 SSH server——ADR-006 自己留的口子

ADR-006 第 4 条原文：「不采用'禁用 SSH server 换便携 sshd'——userspace 无 TUN 时 tailnet 入站无法到达任意回环服务（**G1 待证；若 G1 证明可行，本 ADR 重开评估**）」。

Phase 6a 已经把"可行性"做出来了（现行 `hs/internal/node/config.go` 渲染逻辑 + f86887c e2e）：

- sing-box tailscale endpoint 会把 tailnet 入站拨号**重映射到 127.0.0.1** 再交路由引擎；
- 显式规则 `inbound:["ts"] + destination_port:[p] + outbound:"srv-<name>"`（direct outbound）就能把 tailnet :p 送到 **127.0.0.1:p 的回环服务**——G1-a 形状成立，且白名单可表达；
- 裸默认（无规则）= 任意 tailnet 拨号转发到**同端口回环**——这是 Phase 6a 抓出的安全缺口，现行渲染一律显式 `action: reject` 兜底 + 逐服务放行；
- `ssh_server` 的 :22 由 tsnet listener **在路由器之前**认领（e2e ssh 探针在 reject 规则生效时验证过）。

**推论**：Rust SSH server 的接入方式 = 关 `ssh_server` + 给 :22 加一条显式服务规则 → tailnet :22 → 127.0.0.1:22 → **hs-node 内嵌的 russh**。前提门 R-SSH-1（见 §5）大概率会开，但要等 ADR-005 终稿裁决后正式开。

### 2.2 store——现状是 JSON，redb 的真实定位

`hs/internal/controller/store.go` 头注释（现行实现，Phase 5 收口 `a436c35`）：

> Persistence: JSON file under a lock with atomic writes and timestamped backups … SQLite was named in PLAN.md; the repo is stdlib-only, so the JSON store is the dependency-free equivalent for a single VPS (**deviation recorded** … SQLite remains the option if scale ever demands it).

即：SQLite 本来就是"留着以后上"的选项，且「store 不存明文 token，只存 SHA-256 哈希」「邀请状态机 created→redeemed→registered→used/failed」这些不变式已经实测。redb 进场 = Rust 版重写 store 时直接用真事务库，**少复刻一套自研持久层**（Go 版那套锁+原子写+备份已写已测，Rust 再抄一遍是纯成本）。

### 2.3 权限边界——cap-std 能给什么、不能给什么

| 能给 | 不能给 |
|---|---|
| 程序内路径纪律：state/config/logs/tmp 各持一个 `Dir` 句柄，代码物理上摸不到句柄外的东西 | OS 级 ACL——icacls / 0600 收紧照旧自己做（Go 版 `platform.HardenFile` 语义平移） |
| 防路径逃逸（`..`、绝对路径、越界 symlink → 拒绝） | 阻止 `unsafe`/`std` 直调——不是沙箱 |
| 负向测试好写：「逃逸路径被拒」成为天然断言，接 FAILURE_MODES「失控写入/状态损坏」类 | 网络层强制点——hs-node 只发控制面 HTTPS，`cap-std::net` Pool 收益有限（可选） |

### 2.4 语言无关件（不换）

headscale v0.29.7 / sing-box v1.14.4（内嵌 fork 门控照旧跟踪）/ Caddy 2.x / VPS 官方 tailscale ≥1.104.4——全部保持钉版本+sha256。它们是外部二进制，「换 Rust 链」不动它们。

---

## 3. 目标组件矩阵

| 组件 | 现状 | Rust 链去向 | 依据 |
|---|---|---|---|
| `hs-node`（Windows 节点包装器） | Go | **Rust 重写**（R2） | 我们的代码 |
| `hs-controller`（管理面服务） | Go（JSON store） | **Rust 重写，store 用 redb**（R4/R5） | §2.2 |
| `hs-admin`（运维 CLI） | Go | **Rust 重写**（R5） | 我们的代码 |
| 节点 SSH server | sing-box `ssh_server`（ADR-006 门控中） | **russh 内嵌 hs-node**，经显式路由规则接 :22（R6，挂 R-SSH-1 门）；OxiSH 观望 | §2.1 |
| hs-admin 的 SSH 客户端路径 | 官方 `tailscale ssh` | **普通 ssh 语义**：russh client 库内嵌（首选）或 shell 出系统 ssh（备选）——见 §4.1 | §4.1 |
| headscale / sing-box / Caddy / tailscaled | 外部二进制 | **不变** | §2.4 |
| e2e harness（`hs/internal/hstest`） | Go | **保留 Go 当 oracle**（驱动外部进程，与被测语言无关）；Rust 版二进制以路径参数注入 | §5 |
| 数据面 Rust 化（boringtun 等） | — | **不在范围**，观望 | §1.5 |

---

## 4. 关键语义映射（换件后语义不丢）

### 4.1 SSH 链路——这是语义变化最大的一处，逐条写清

**Tailscale SSH 的身份模型**：`tailscale ssh` 不用传统 SSH 密钥，认证=传输层 tailnet 身份 + Headscale SSH 规则裁决，服务端 ssh_server 里再做一遍会话授权。
**换成 russh 后**：`tailscale ssh` 客户端对普通 SSH server 不适用，链路变成传统 SSH：

| 闸门 | 现栈（ssh_server） | Rust 版（russh） | 语义是否保住 |
|---|---|---|---|
| WHO（谁能连到 :22） | tailnet 包过滤（node 侧 netstack 执行 grants）——G4 负向已证 | **不变**：包过滤仍在 node 的 netstack 执行，russh 绑 127.0.0.1，只有 sing-box 能碰它 | ✅ |
| 会话授权（ssh 规则段） | ssh_server 内执行 | **降级为客户端侧检查**——服务端执行点消失，作为**偏差**记入 FEATURE_TRACEABILITY（对齐 ADR-005 的诚实口径） | ⚠️ 偏差如实记 |
| SSH 层认证 | Tailscale 身份（无传统密钥） | 传统公钥：controller 管理公钥（登记时随凭证下发到节点，参考实现三身份表的「Controller Management Key」回归实体密钥形态） | 换模型，等效强度；测试钉死「无钥拒绝」 |
| 加密算法面 | fork 内嵌栈（<1.104.4 阈值，ADR-006 门控） | russh：**mlkem768x25519（PQ）**，且 fork 的 SSH 栈整个移出暴露路径——**ADR-006 门控就此解除**（比等 fork 跟进更主动） | ✅ 且变强 |
| shell/exec | ssh_server + tailscale ssh | russh session handler：exec=进程参数数组纪律；交互 shell=**ConPTY 接线（Windows 主要工程量，候选 wezterm/portable-pty 0.9.0，29.7M 下载；2025-02 后未发版，标注待核）** | ✅ 但 R6 要实测 PTY/退出码/stdout/stderr 分报 |
| 客户端兼容 | 官方客户端 ≥1.104.4 | hs-admin 内嵌 russh client；两边 KEX 有 curve25519 兜底 + mlkem768x25519 共有——**连接性必须实测，不预设**（门 R-SSH-2） | 待实测 |

**必须写进 ADR 的偏差**：Headscale ssh 规则段失去服务端执行点（只剩客户端检查）；「撤销语义」的 G3 结论要在 russh 形态下**重测**（会话终止时机可能不同于 ssh_server 形态）。

### 4.2 store（redb）

- 表：`nodes`（key=node id）、`invitations`（key=invite id）、`audit_events`（自增序号 key，append-only）、`enroll_sessions`（续传状态机）。value=serde JSON，schema_version 字段进 value。
- 版本底线 **≥4.6.0**（§1.2 的损坏库修复）。
- 不变式承接（逐条对照现行 store.go）：只存哈希不存明文 token；单写者（redb 一次一个写事务，等价现行 flock 语义）；打开时损坏检测=报错不重建（G5/身份不变式同款纪律）；JSON 时间戳备份语义保留（定期导出快照文件，回滚成本低）。
- 何时不用 redb：若 Rust 版坚持零依赖，可复刻 JSON+原子写方案（已知可测）。**推荐 redb**，理由=少一套自研持久层（Go 版那套是历史代码，不是负担转嫁）。

### 4.3 节点权限（cap-std）

- `Dir` 划分：`config\` 只读句柄、`state\tailscale\`（节点身份，唯一写者）、`logs\`、`tmp\`=cap-tempfile。
- 负向矩阵新增（接 FAILURE_MODES）：`..`/绝对路径/越界 symlink 拒绝断言；state 句柄外写拒绝断言。
- 平台纪律平移（AGENTS.md 既有教训在 Rust 的落点）：
  - 子进程无窗口：Windows creation flags + **显式透传 stdout/stderr**（2026-11-14 静默失败教训——Rust 里 `CommandExt::creation_flags(CREATE_NO_WINDOW)` 同样必须配 `.stdout/.stderr` 继承，且 POSIX 侧用 cfg 分支，三元只求值选中分支）；
  - 锁：**不加包级 mutex**（2026-11-17 windows lockfile 死锁教训，根治就是删 mutex；Rust 直接 LockFileEx/flock 单层）；
  - 平台专属代码放 `#[cfg]` 分支，跨平台行为逐字对拍（提交前自查第 3 问）。

---

## 5. 顺序方案（A/B 对比 + 推荐）与阶段表

**方案 A（先收后切）**：P6–P12 全按现行 PLAN 在 Go 收口，再起 Rust 重写。
代价：P8–P12 的大头（store/CLI/审计/部署产物/文档/验收矩阵）在 Go 做一遍、Rust 再做一遍 = 双倍成本。
**方案 B'（推荐）**：P6 + P7 在 Go 收口（**语义裁决必须先出**——ADR-005 转发形状、DERP failover 九类失败映射，它们决定 Rust 版接口），**P8 起切 Rust**；P9–P12 直接对 Rust 版做；Go 版 CI 保持绿到 Rust 版过同语义 e2e 为止（PLAN §8「现行 Go 版仍然可用」条目不破坏——止步于"登记/授权/SSH 全链可用 + P6/P7 已裁决"）。

| 阶段 | 内容 | 验收线 | 前置 |
|---|---|---|---|
| R0 | 本草案拍板 | 用户点头 | — |
| R1 | Rust 骨架：workspace、`hs-node`/`hs-controller`/`hs-admin` 三 crate、rust-toolchain 钉定、CI（GH Actions 双跑者，clippy+fmt+cargo audit 进门禁；本机不编译——纪律不变） | CI 全绿 | R0 |
| R2 | `hs-node` Rust 版：config/state/autostart（HKCU Run）/supervisor 重试梯（5→10→20→40→60s、≥10 次 3600s、auth 失败跳小时级）/日志轮转+脱敏/锁 | 单测全绿 + **与 Go 版渲染的 sing-box config 逐字节对拍** | R1 |
| R3 | 登记链 Rust 版：邀请兑换/续传/撤销/幂等 | **跑通现有 Go e2e harness 同语义用例**（harness 参数化指向 Rust 二进制） | R2 |
| R4 | store：redb 四表 + 审计 | 崩溃安全用例（写事务中途 kill → 重开无损/报错不重建）；哈希不存明文断言 | R1（可与 R2/R3 并行裁剪） |
| R5 | authz + `hs-controller`/`hs-admin` Rust 版收口（API 鉴权、健康端点） | Phase 4/5 同款正负对照 e2e 全过 | R3+R4 |
| R6 | SSH server 切换：关 `ssh_server`、:22 显式规则、russh 内嵌、ADR-006 改写为"已解除（原因：暴露路径移除）"、G3 撤销语义在 russh 形态重测、FEATURE_TRACEABILITY 偏差更新 | R-SSH-1 + R-SSH-2 双门开 + ssh 全链 e2e（PTY/退出码/分报） | ADR-005 终稿 |

流程纪律照旧：每阶段一个 commit、CI 全绿才进下一步、**push 前先问用户**（2026-11-15 新规）、负向红绿记 `analysis/CALIBRATION.md`、`git add` 显式路径。

---

## 6. 风险与观望名单

| 事项 | 现状信号 | 触发动作 |
|---|---|---|
| OxiSH Windows 支持 | #252 open（另有 #292 简化实现提案）；#338 exec+pty、#251 sftp、#261 forced commands 均 open | #252 合入后重评"Linux 节点用 OxiSH / Windows 用 russh"的分工 |
| sagernet/tailscale fork ≥1.104.4 | ADR-006 解锁路径 (a) | 即使 R6 不做也先解除门控；R6 做了则 ADR-006 直接改写为"暴露路径移除" |
| russh 归属变更 | warp-tech → Eugeny，活跃（0.65.2，2026-11-16） | 盯发版节奏；cargo audit 门禁兜底 |
| cap-std 单人维护 | bytecodealliance → sunfishcode，843 星，2026-10-19 推 | 用在"纪律层"定位，可替换（它不是安全边界本身）；关注是否有 org 回迁 |
| redb major 迭代快 | 1→2→3→4 | 版本钉 ≥4.6.0 + 升级说明进 OPERATIONS |
| portable-pty 停更 | 0.9.0，2025-02 后未发版（29.7M 下载，wezterm 自用） | R6 开工前重核；备选自写 ConPTY 接线（工程量大，最后手段） |
| Rust 原生 Tailscale 客户端出现 | 目前未核实到成熟项目（boringtun 只有 WireGuard 原语） | 出现再评数据面 Rust 化，当前明确不做 |

---

## 7. 待拍板清单（逐条可答）

1. **顺序**：方案 B'（P6+P7 Go 收口 → P8 起切 Rust）还是方案 A（全部收完再切）？
2. **SSH 方向**：russh 内嵌立为方向（挂 R-SSH-1/R-SSH-2 门）？还是先观望 fork 跟进、R6 缓行？
3. **store**：redb 默认 vs 复刻 JSON 方案？（推荐 redb）
4. **cap-std 深度**：只在 hs-node 的状态/配置访问层用，还是全 crate 统一？（推荐前者，controller 侧收益低）
5. **e2e harness**：保留 Go 当 oracle（推荐）还是随迁 Rust？
6. **本文档**：已入 **orphan 分支 `riir`**（单文件分支，只有 `analysis/RUST_CHAIN_DRAFT.md`，不并 main）；是否并入 main / 推 origin 待拍板。
