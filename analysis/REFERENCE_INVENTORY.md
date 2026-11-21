# REFERENCE_INVENTORY — 参考实现（现行 Go 版 OutboundAll）全量清单

> Phase 0 产出（源规格 §3）。基线 `main @ ecec22b`；调研日期 2026-11-17。
> 范围：`docs/` 全部 22 份 + README + 源码树 + CI + git 历史。
> 本文只记**事实**；新栈映射见 FEATURE_TRACEABILITY.md。

---

## 1. 参考文档清单（22 份，全读）

| 文件 | 性质 | 对新栈的关键输入 |
|---|---|---|
| README.md | 总览 | 三条不变式（零管理员/回环限定/三身份分离）；命令总览；平台验证状态 |
| docs/README.md | 文档索引 | Go 版 vs legacy 存档的划分口径 |
| docs/ARCHITECTURE.md | 设计冻结 | 三角色进程模型；数据布局（用户级目录）；supervisor 状态机 |
| docs/SECURITY_MODEL.md | 设计冻结 | 三身份定义；AK 行逐字模板；威胁模型表；N1–N11 负向矩阵；日志脱敏模式 |
| docs/PROTOCOL.md | 设计冻结 | 登记协议（凭证→enroll→active）；AK 文件规范（无 Include）；端口分配器（13440–13600 成对） |
| docs/CONFIG_SCHEMA.md | 设计冻结 | config.json/identity.json/machines.json/registry.json/credential 五种结构；校验不变式 |
| docs/PORTS.md | legacy 存档 | 端口模型（对外 1 端口）；开放开户口（18443，旧机制勿搬） |
| docs/NODE_MANUAL.md | 手册 | install 19 步；supervisor 重试梯参数；自启动三平台；卸载语义（保留数据） |
| docs/CONTROLLER_MANUAL.md | 手册 | 管理私钥纪律；anchors 两类（ssh_alias/OutboundAll）；known_hosts 私有化；trail 诊断 |
| docs/RELAY_MANUAL.md | 手册 | relay init/凭证签发/revoke vs purge；AK 写规程；revoke/purge 语义分层 |
| docs/MIGRATION_GUIDE.md | 手册 | 旧→新迁移（只改 forced command，key/端口逐字节不变） |
| docs/FAST-PATH.md | legacy 存档 | 23 条坑位表（粘贴块/端口/撤销链路） |
| docs/RUNBOOK.md | legacy 存档 | 无提权机制实测矩阵（计划任务/HKCU/Startup 全免提权） |
| docs/PRIOR-ART.md | 调研 | 与 ngrok/frp/Teleport 的差异；「零管理员+受限 key+一行 enroll」组合的不可替代点 |
| docs/SSH_BACKEND_MATRIX.md | Phase 1 证据 | 三平台 rootless sshd 实测；嵌入式 Go SSH 不采用的理由；OpenSSH 10 per-source penalty |
| docs/PHASE4_ACCEPTANCE.md | 验收证据 | N1–N10 真 SSH 实测读数；F1–F5 缺陷 |
| docs/REFACTOR_BASELINE.md | 事实正本 | 旧 PS 版全量审计；必须保留的行为清单；迁移面映射 |
| docs/REFACTOR_FINAL_REPORT.md | 验收报告 | Phase 0–10 全记录；F1–F18 缺陷与修复；测试清单 |
| docs/STATUS.md | 实测记录 | 「证过的 vs 没证的」口径；legacy 历史证据 |
| docs/PITFALLS.md | 坑位库 | A–I 九组共 60+ 条真机坑（Go 版 H1–H16 + 审评 I1–I7） |
| docs/man-OutboundAll.md | 手册页 | 全命令表；退出码分类（0/1/2/3/10+） |
| docs/magpie-remote-provider-research.md | legacy 调研 | magpie provider 语义（隧道必须指 gateway 3425 等） |

## 2. 功能与确切行为

### 2.1 三角色
- **node**（被控机）：install（凭证驱动：本地生成 host key + tunnel key → 凭证私钥 enroll 提交公钥 → manager 公钥写本机 AK → config/identity → 自启动）/ run（`sshd -D` 子进程 + 每通道独立 `ssh -R` + supervisor）/ status / stop / uninstall（只去自启，保留数据）/ logs / doctor。
- **controller**（总控）：init（manager 密钥对，公钥需 scp 到 relay）/ list / status / anchors（有序 trail 带真实失败原因）/ shell（`-t` PTY）/ exec（参数数组 + bash stdin 脚本模式）。
- **relay**（Linux 中继，零常驻服务）：init（建 `~/.OutboundAll` + helper_path 烤进 AK 行）/ credentials issue|revoke / list / inspect / ports / revoke（软删墓碑）/ purge（只收 revoked）/ migrate-legacy / relay-helper（forced command：`--port P tunnel`、`--credential C enroll`，旗标前置于动词）。

### 2.2 登记协议（幂等语义）
relay enroll → 凭证（24h、一次性、预绑定端口槽位、内嵌 manager_pubkey）→ node install 消费（用后即删）→ helper 校验（有效/未过期/未用/节点非 revoked/凭证 ID = 节点当前 ID）→ AK 原子写受限行 → registry pending→active → 节点 register 上报硬件（uuid/macs/user，只能写自己记录）。
幂等：同机重装 → 本机已有 tunnel key → 提交同公钥 → helper 按指纹命中既有节点 → 返回原槽位。

### 2.3 隧道与守护
- 每通道独立 ssh 连接（一条失败不杀另一条，坑 B4）；`-R 127.0.0.1:<remote>:127.0.0.1:<local>` 地址字面量硬编码。
- 重试梯：5→10→20→40→60s（翻倍封顶 60）；≥10 次失败→3600s；**auth 失败（Permission denied）直接跳小时级**（OpenSSH 10 per-source penalty）；成功清零。
- 开机等待：重启后 ≤10 分钟（20×30s）探 relay 再进梯；keepalive 15s×3；ConnectTimeout 10s；ExitOnForwardFailure=yes。
- 单实例锁（Windows LockFileEx / Unix flock）+ pid 记录。
- Windows 无窗口：`OutboundAlld.exe`（-H windowsgui）+ CREATE_NO_WINDOW；不经 ssh 会话树起常驻进程（job object 连坐）。

### 2.4 Controller 连接路径
- anchors 有序 failover：ssh_alias（引用用户 ~/.ssh/config，从不迁移/复制）→ OutboundAll 锚点（ProxyJump + manager key → relay 回环 `127.0.0.1:<remote_port>` → 节点 sshd）。
- 同机部署（via_relay 以 127.0.0.1 开头）自动跳过跳板。
- 控制器私有 known_hosts + StrictHostKeyChecking=accept-new（节点重装换 host key 是预期）。
- exec：命令串只作 ssh 尾参，绝不经本地 shell；bash 走 `bash -s` stdin 脚本；连接失败（rc=255 + 连接类 stderr）与远端失败分报。

## 3. 安全不变式（全部继承自 SECURITY_MODEL，逐条列出）

1. **三身份分离**：Node Tunnel Key（本机生成、私钥不出机）；Controller Management Key（只在控制器，泄露=最高级事件）；Enrollment Credential（一次性短期，只够提交一把公钥，不能登任何机器）。
2. **回环字面量**：所有绑定 `127.0.0.1:`；拒绝 localhost/0.0.0.0（permitlisten 精确匹配强制）。
3. **节点 key 能力上限**：仅 `-R` 自己的两个回环口；无 shell、无文件读、无 -L（permitopen=127.0.0.1:1）、不能占他人端口。
4. **AK 写规程**：唯一入口 = helper/relay 子命令；flock 串行化；结构化解析（外来行逐字节保留）；原子写 tmp+rename；时间戳备份；写后重读校验；解析失败中止不清空。
5. **撤销分层**：revoke（墓碑 + 删 AK 行 + 删全部凭证 + 清 CredentialID；helper 双 guard：revoked 拒绝 + 非 当前凭证 ID 拒绝）；purge（只收 revoked，防手滑删 active）。
6. **私钥纪律**：私钥不进临时目录长期遗留、不进日志（logging 统一脱敏 `-----BEGIN`/`sk-*`/`Bearer `/公钥体/32+hex）。
7. **管理 API 面**：无（relay 只有 sshd + forced command + CLI）。
8. **MITM**：installer 无私钥；注册只传公钥与配置；registry 绑定槽位。
9. **端口分配**：registry ∪ `ss -ltn` 实时监听（含通配/IPv6 全形式）；全程持锁；先写 registry 再写 AK。

## 4. 协议假设

- relay sshd 为系统 OpenSSH（10.0p1 实测）；authorized_keys 无 Include（实测 F1）→ helper 直接维护系统 AK。
- Go flag 包遇位置参数即停 → forced command 旗标前置（F3/H10）。
- authorized_keys options 值需两层转义：ShellQuote（POSIX 单引号）+ AKOptionValue（`\"`/`\\`，拒 CR/LF/NUL）（F14/I2）。
- OpenSSH 10 per-source penalty：失败认证的源 IP 被惩罚 → 负向测试放慢节奏；supervisor auth 失败重度退避。
- `permitlisten`/`permitopen` 是**精确匹配**：`-R` 必须写全 `127.0.0.1:N:...`（B1）。

## 5. 生命周期

### 5.1 客户端（node）
install（19 步）→ run（boot-wait → ladder → connected）→ 崩溃自动重启对应子进程 → stop（杀 sshd，supervisor 下轮退出）→ uninstall（去自启留数据）→ 升级保留身份（tunnel key 不变）→ 状态损坏 → 检测并给安全恢复流程（不静默重生成）。

### 5.2 服务端（relay）
init（一次性）→ credentials issue（幂等：同名复用端口对）→ 节点 enroll（pending→active）→ 运行期零常驻（helper 一次一进程）→ revoke/purge → 备份（AK .bak-<ts>；registry 手动 cp）→ 迁移（migrate-legacy 幂等，AK 逐字节不变）。

## 6. 错误处理

- 退出码分类：0 成功 / 1 一般失败 / 2 用法错误 / 3 未实现 / 10+ 领域码（doctor）。
- supervisor 状态机：boot-wait/backoff/connected，状态可从 `node status` 读出（LISTENING on relay / tunnel down）。
- controller 探测 trail：每条锚点带真实失败原因，绝不静默吞。
- 日志脱敏统一在 logging 包；**密钥永不入日志**。

## 7. 认证与撤销行为（负向矩阵 N1–N11，全部真 SSH 实测）

| # | 断言 |
|---|---|
| N1/N2/N4 | tunnel key 无 shell / 无文件读 / 无任意命令 |
| N3 | tunnel key 不能 -L 进 LAN（permitopen=1） |
| N5 | 不能占他人端口 |
| N6 | 不能绑 0.0.0.0 |
| N7 | 只能绑自己两个口 |
| N8 | manager key 不用于隧道 |
| N9 | 过期/已用凭证不能再登记 |
| N10 | 撤销后旧 key 失效（Permission denied） |
| N11 | 撤销后未消费凭证不能复活节点（P1 修复，双 guard + 真实 SSH 负向） |

## 8. Failover 行为

- **客户端隧道层**：每通道独立连接 + 重试梯（见 §2.3）；auth 失败跳小时级；开机等待。
- **controller 层**：machines.json anchors 有序探测，第一个可用者胜，trail 记录全过程；ssh_alias 与 OutboundAll 锚点混用。
- **relay 层**：无 HA（单机设计）；AK/registry 有备份与幂等恢复（Phase 8 实测：误删 registry 后重跑 migrate 恢复）。
- 注意：参考实现没有「多 relay/多区域 relay failover」；「anchor」是 **controller 侧的多路径连接锚点**，不是 relay 冗余。

## 9. 平台特定限制

| 平台 | 状态 | 限制 |
|---|---|---|
| Linux（Debian 13 / OpenSSH 10.0p1） | ✅ 全链实测 | 全新无 sshd 的 Linux 上 `/run/sshd` privsep 目录行为未测（记录为待办） |
| Windows（Win11 / Win32-OpenSSH 10.0.0.0p2） | ✅ 实测 | 用户级自启动需真实桌面会话复验（沙箱注册表写入被拒=环境限制）；便携 sshd 需固定版本+sha256 下载 |
| macOS | ❌ UNVERIFIED | 代码路径存在（LaunchAgent），无真机，不声明支持 |
| 通用 | — | 用户级自启只在登录会话后活；开机即用需可选系统服务（root，永不默认） |

## 10. 既有测试覆盖

- 单元测试 12 个包（cli/config/controller/crypto/lockfile/logging/openssh/platform/relay/supervisor/...），全绿（ubuntu CI）。
- 关键回归测试：`TestBuildProbeArgsOrder`（ssh 参数顺序）、`TestMigrateLegacyIdempotentKeepsAKByteForByte`（迁移幂等不改 AK）、`TestPurgeRefusesActiveNode`、supervisor 重试梯/实例锁/`127.0.0.1:13441:127.0.0.1:19022` 精确断言、AK 解析无损、logging 脱敏、platform harden。
- **真 SSH 负向验收**（N1–N11）在 relay 102 真机跑过（PHASE4_ACCEPTANCE），非字符串配置测试。
- CI：ubuntu 绿；**windows 自 ci.yml 诞生起从未绿过**（2026-11-17 发现：runner `core.autocrlf=true` → CRLF → gofmt 全红；修复 commit e0388c1）。全 9 次历史 run 0 success——即「windows 双跑者全绿」从未成立过，此前文档口径有误，本文如实更正。

## 11. 源码树（Go 版，全部保留不动）

```
cmd/OutboundAll/main.go          入口（子命令路由，~1.2k 行）
internal/cli/                 node/controller/relay/autostart/service/migrate/doctor/sshd_run/hide
internal/relay/               akfile.go（结构化 AK 读写）registry.go（registry+端口分配）helper.go（forced command）
internal/supervisor/          ladder.go（重试梯）runner.go（通道守护）dial.go hide_*
internal/{config,crypto,identity,openssh,platform,logging,lockfile,errs,controller}/
scripts/build-matrix.sh       6 目标交叉编译 + SHA256SUMS
.github/workflows/ci.yml      vet+test(ubuntu,win) + build-matrix + smoke-linux
```

## 12. 未写入文档但代码里的行为（Phase 0 补充发现）

- `relay-helper tunnel` 还服务 `check`（JSON 探活）与 `get openssh`（白名单文件下发，Phase 5 接入分发）。
- controller exec 对 `shell=bash` 走 stdin 脚本模式，其他 shell 直接尾参。
- `node logs <channel>` 读 `logs/<channel>.log` 末尾 40 行；通道名 `sshd`/`tunnel-ssh`/`tunnel-<name>`。
- Windows `hide_windows.go`/`hide_other.go` 构建标签分离（CREATE_NO_WINDOW 只在 win32 求值，POSIX 恒 0——审评 I 组已验证）。
- 测试逃逸口：`OUTBOUNDALL_RELAY_DIR`/`OUTBOUNDALL_RELAY_AK`（仅测试，正常运维不碰）。
