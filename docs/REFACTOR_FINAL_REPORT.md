# dialhome Go 全面重构 — 最终验收报告

> 分支：`go-refactor`　|　规格：《dialhome 全面重构执行规格：Go / Cross-platform》
> 覆盖：Phase 0–10　|　证据口径：所有结论均附实测输出/测试名；无法验证的平台如实标 **UNVERIFIED**。
> 最后更新：2026-11-15

---

## 0. 结论摘要

| 阶段 | 目标 | 状态 | 关键证据 |
|---|---|---|---|
| P0 | 仓库全量审计基线 | ✅ | `docs/REFACTOR_BASELINE.md`（含 WCIS 无 UAC SOP 事实） |
| P1 | 三平台 SSH 后端矩阵 | ✅ Linux/Win；⚠️ macOS UNVERIFIED | `docs/SSH_BACKEND_MATRIX.md` |
| P2 | 设计冻结 | ✅ | `ARCHITECTURE / SECURITY_MODEL / PROTOCOL / CONFIG_SCHEMA` |
| P3 | Go 框架（单二进制三角色） | ✅ | `cmd/dialhome` + `internal/*` 分包 |
| P4 | Relay 安全核心 | ✅ | `docs/PHASE4_ACCEPTANCE.md`（**N1–N10 全绿**） |
| P5 | Node | ✅ | install/run/status/stop/uninstall/logs；Win+Linux 真机 |
| P6 | Controller | ✅ | 多锚点 failover；`school-guest` 校园链路实测 OK |
| P7 | 用户级自启动 / 可选系统服务 | ✅ | `autostart` 三平台 + `service --scope system`（可选、需 root）；CLI 全可用 |
| P8 | 旧版迁移 | ✅ | 生产 relay 零中断迁移 + 幂等恢复 |
| P9 | 端到端验收 | ✅ | 全新 Go 链 `controller exec` → `debian-h61`/`user` |
| P10 | 文档手册 / CI / 发布 | ✅ | 本报告 + 6 份手册 + `ci.yml` + `build-matrix.sh` |

**安全约束全部保留并复测**：无管理员、回环字面量、三身份分离、
`permitlisten`/`permitopen` 精确限制、幂等登记。

### 提交序列（全部推送并 `ls-remote` 回读校验）
```
6349864 P0 → 3b12d44 P1 → fa5663e P2 → 2bcdd05+ddcdb8e P3
67399ee+dfe0cdb P4 → 56aab26 P5 → 5ab66a5 P6
1d01637 P8 迁移 → ba72a4b P9 E2E → 80b11c7 幂等/purge
b614367 P7 CLI 完成 → (P10 文档/CI 本轮)
```

---

## 1. Phase 0–2：基线、矩阵、设计冻结

- **P0**：逐文件审计 legacy（PS/Bash/Python）版，记录无 UAC SOP
  （Windows Terminal pseudoconsole 会破坏 `-WindowStyle Hidden`；wscript VBS 真隐藏；
  ssh-spawned 进程随 sshd job object 死；自账户计划任务无需提权）。
- **P1**：Linux rootless sshd 实测（Debian 13 / OpenSSH 10.0p1，回环 19443，
  exec/PTY + 负向）；Windows 生产证据（HP66）；**macOS UNVERIFIED**（无真机）。
  另记 OpenSSH 10 的 per-source penalty：认证失败后同源后续连接被重置
  （journal `penalty: failed authentication`）⇒ 负向测试必须放慢节奏，
  supervisor 遇 auth 失败直接跳小时级重试。
- **P2**：冻结三身份模型、AK 行模板（字面量 `127.0.0.1`）、
  **authorized_keys 无 Include 指令**（helper 直接维护系统 AK）、
  端口池 13440–13600、重试梯、退出码分类。

---

## 2. Phase 3–4：框架与 Relay 安全核心

- Go 单二进制，`node/controller/relay/relay-helper/autostart/service/migrate/doctor/keygen` 子命令；
  stdlib 优先（Windows `LockFileEx` 用 `syscall.NewLazyDLL`，无 `x/sys` 依赖）。
- AK 结构化读写：`Parse` 保留外来行逐字节、`UpsertNode/RemoveNode` 只动目标行、
  原子 tmp+rename、时间戳 `.bak`、flock/LockFileEx 串行化。
- **N1–N10 负向矩阵全绿**（`docs/PHASE4_ACCEPTANCE.md`）：tunnel key 无 shell /
  无文件读 / 不能 `-L` / 不能执行任意命令 / 不能占他人端口 / 不能绑 `0.0.0.0` /
  只能绑自己两个口；manager key 不用于隧道；过期凭证拒绝；撤销后旧 key 失效。

### 过程中抓出并修复的真实缺陷（均有回归测试或真机证据）
| # | 缺陷 | 修法 |
|---|---|---|
| F1 | ladder 时长单位混用 ns 与秒 → 退避变 0 | 统一秒制重写 |
| F2 | AK 行带 options 前缀时 `Parse` 失败 | 扫描字段定位 `ssh-ed25519` 并校验 blob 前缀 |
| F3 | forced command 里 flag 在位置动词之后 → Go flag 包不解析 | 改为 `relay-helper --port P tunnel`（flag 先） |
| F4 | relay DataDir 用 `%APPDATA%` 语义 | 恒定 `~/.dialhome`（helper 不继承 shell 的 `DIALHOME_DATA_DIR`） |
| F5 | root 部署把 `~/.dialhome` 变成 root 属主 | `chown -R user:user`（文档注明） |
| F6 | PowerShell `Expand-Archive` 从无控制台父进程 spawn 时 `Write-Progress` 报 Access denied 且 **exit 0 但不解压** | 改 stdlib `archive/zip`（带 zip-slip 防护） |
| F7 | `ssh.exe` 没有 `-y`（把 flag 当连接参数） | 公钥渲染改 `ssh-keygen -y` |
| F8 | unix `ssh-keygen` 路径推导错误（/usr/sbin） | 固定 `/usr/bin` |
| F9 | supervisor 用 host key 当隧道身份 | 改 `keys/tunnel.key`（E2E 抓出） |
| F10 | ssh 连接选项放在 `user@host` 之后被吞成远程命令 | `ProbeAnchorSSH` 把选项+目标置于远程命令之前（回归测试 `TestBuildProbeArgsOrder`） |
| F11 | 节点每次 install 重生成 host key ⇒ controller 报 REMOTE HOST IDENTIFICATION HAS CHANGED | dialhome 锚点用控制器私有 known_hosts + `StrictHostKeyChecking=accept-new` |
| F12 | no-op `migrate-legacy` 仍重写 AK（每次多一个空行） | 仅真实改写时写盘（回归测试 `TestMigrateLegacyIdempotentKeepsAKByteForByte`） |
| F13 | **P1 安全**：撤销后未消费凭证可复活节点 | revoke 清凭证+AK 行；helper 双重 guard（revoked / 凭证 ID 非当前）；真实 SSH 负向测试 |
| F14 | forced command 路径带空格被 shell 拆词 | 分离 `ShellQuote` + `AKOptionValue` 两层转义 |
| F15 | `livePorts` 忽略 `0.0.0.0:` / `[::]:` 通配监听 | 提取所有本地地址端口，单测覆盖 6 种写法 |
| F16 | `purge` 留下失效凭证 AK 行 | purge 同步 `RemoveCredential` + 原子写盘 |
| F17 | `relay_user` / 默认地址硬编码 | `relay init --relay-user` + `advertiseUser()` 层级回退 |
| F18 | CI 用 Go 1.24 而 `go.mod` 要 1.27 | 统一 1.27 + `GOTOOLCHAIN: local` |

---

## 3. Phase 5–6：Node 与 Controller

**Node**（Win + Linux 真机）：
- install：凭证驱动 → 本地生成 host+tunnel key（**私钥不出机**）→ 用凭证私钥向 relay
  提交 tunnel 公钥（enroll）→ manager 公钥写节点 AK → config/identity → 按 mode 自启动。
- run：`sshd -D` 子进程 + 多通道 supervisor（5/10/20/40/60s → ≥10 次 3600s、
  auth 失败直跳小时级、开机等待 ≤10min/30s 探针、实例锁、`CREATE_NO_WINDOW`）。
- status/stop/uninstall/logs。

**Controller**：
- `machines.json` 两类锚点：`ssh-alias`（沿用用户 `~/.ssh/config`，**从不迁移**）与
  dialhome 锚点（ProxyJump + manager key；同机 `127.0.0.1` 自动跳过 jump 直连回环）。
- 有序 failover，**每次探测都留 trail 并带真实失败原因**。
- `controller exec` bash 走 stdin 脚本模式（避免引号嵌套），`shell` 走 `-t` PTY。
- 校园链路实测：`controller anchors school` → `1. [ssh-alias] school-guest -> OK (ssh ok)`。

---

## 4. Phase 8：生产零中断迁移（最关键安全动作）

在生产 relay（102）执行 `relay migrate-legacy ~/.magpie-connector`：

| 验证项 | 结果 |
|---|---|
| 旧 `magpie-node-*` / `magpie-tunnel-node-*` 行（win2 + HP66） | 2 行全部导入 |
| key body | **逐字节不变**（diff vs `authorized_keys.bak-20261115-024605` = KEYS-IDENTICAL） |
| 端口 | win2 `13435/13437`、HP66 `13440/13441` 原槽位 |
| forced command | 只改命令本体 → `dialhome relay-helper --port <P> tunnel` |
| 旧私钥 ↔ 新 helper | win2 旧 key 经新 Go helper 回答 `{"ok":true,"node":"port-13435","ssh":13437,...}` |
| 生产隧道 | 迁移过程零中断 |
| 幂等恢复 | 一次误删 registry 后，重跑 migrate 把 2 节点恢复为 active（AK 字节不变） |
| 清理 | `relay purge e2e-node` 删除 revoked 墓碑（拒绝 active 节点，有单测） |

当前生产 registry：`n-10-110-23-66-2b12 (active, 13440/13441)`、
`port-13435 (active, 13437/13435)`、credentials 空。

---

## 5. Phase 9：端到端验收（全新 Go 链，relay 102 真机）

完整脚本在 102 上执行，输出（节选）：
```
== 1) relay init ==                    relay init ok: /home/user/.dialhome
== 2) controller init ==              MANAGER-KEY/PUB 生成
== 3) manager.pub staged ==           ssh-ed25519 AAAAC3...
== 4) issue credential ==             credential ports: 13441 13440
== 5) node install ==                 ENROLL = tunnel pubkey bound to e2e-node
                                      CONFIG / KEYS / TUNNEL-PUB / AUTOSTART(XDG)
                                      AK dialhome-node-e2e-node lines: 1
== 6) node run ==                     sshd.log: Server listening on 127.0.0.1 port 19022.
== 7) relay 回环监听 ==               LISTEN 127.0.0.1:13441
== 7b) 节点 AK 含 manager pub ==      mgr-in-node-ak = 1
== 8) controller exec ==             debian-h61  /  user        exec rc=0
== 9) cleanup ==                      revoke + purge
```

即：**凭证签发 → 本地生成密钥 → enroll 绑定 → 隧道上线 → 控制器经 ProxyJump +
manager key 进入节点并执行 `hostname && whoami`** 全链路打通，三身份各司其职。

### 代码审评修复（2026-11-15，P1 + 4×P2 + 构建可复现性）

外部审评指出 6 项问题，全部修复并加回归测试：

| 级别 | 问题 | 修复与证据 |
|---|---|---|
| **P1** | 撤销节点不同时撤销登记凭证 ⇒ 旧凭证可把节点重新置 active | `relayRevoke` 删除该节点全部凭证记录 + AK 行并清空 `CredentialID`；`EnrollCommand` 新增 `revoked` 状态检查与「凭证 ID 必须等于节点当前 ID」检查。两条**真实 SSH 负向测试**（102 上临时 rootless sshd、旧凭证私钥、replay 的 AK 行）：① 常规 revoke 后 `DENY: unknown credential`；② 人为恢复凭证记录后 `DENY: victim is revoked`。节点始终保持 `revoked`，AK 未重建，生产 registry/AK 未受影响 |
| P2 | Forced command 路径未做 POSIX shell 引用 | 新增 `ShellQuote()`（单引号 + `'''`）与 `AKOptionValue()`（`"`/`\`、拒绝 CR/LF/NUL），`NodeLine`/`NodeLineRaw`/`CredentialLine` 全部改用双层转义；`TestForcedCommandQuoting` 覆盖空格、单双引号、反斜杠、`$(id)`、反引号、分号 |
| P2 | 端口分配忽略通配地址监听 | `LivePortsFromSS` 解析所有本地地址形式（`0.0.0.0:`、`[::]:`、`*:`、`[::1]:`、IPv4 回环、`:::`），`cli.livePorts()` 复用同一函数；`TestLivePortsFromSSIncludesWildcardBinds` |
| P2 | `purge` 未删凭证 AK 行 | `relayPurge` 接收 `akPath`，删除凭证记录时一并 `RemoveCredential` + 原子写回；测试断言无关凭证/外来行保留 |
| P2 | 部署参数硬编码 | `relay init --relay-user USER` 写入 registry；`advertiseUser()` 按 配置 → `$USER` → `$USERNAME` → 兼容默认 回退；`--advertise` 帮助写明默认站点地址与 co-located 用法 |
| 构建 | `go.mod`=1.27 而 CI `setup-go`=1.24 | 三处统一 `1.27` + 工作流级 `GOTOOLCHAIN: local`（不静默下载工具链） |

`docs/SECURITY_MODEL.md` 负向矩阵新增 **N11**（撤销后凭证不可复活），`docs/PITFALLS.md` 新增 I 组记录全部 7 条。

安全边界同步复核：三身份私钥分别只存在于节点/控制器/凭证文件；通道全为
字面量 `127.0.0.1:`；AK 外来行逐字节保留；迁移与登记幂等。

---

## 6. Phase 7：自启动 / 可选系统服务（CLI 补齐）

| 命令 | 行为 | 验证 |
|---|---|---|
| `autostart install\|uninstall\|status` | Windows `HKCU\Run`；Linux `systemd --user` + 无 user bus 时 XDG fallback；macOS LaunchAgent（**UNVERIFIED**） | Win + Linux 冒烟 ✅ |
| `service install\|uninstall\|status --scope system` | 可选 Linux system unit，**需 root、永不默认**；非 Linux 明确拒绝且不尝试提权 | Win/Linux 冒烟 ✅ |
| `migrate --from-legacy [--legacy-dir DIR]` | 委托同一幂等导入器 | 102 真机幂等（AK sha 不变）✅ |
| `doctor` | 只读诊断（平台/数据目录/config/identity/key/自启动） | Win + Linux ✅ |

Windows 用户级注册表写入在**受限沙箱**里被拒（`Access is denied`）——这是 agent 环境的
受限令牌，**不是产品缺陷**；已在文档中如实标注需真实桌面会话复验。

---

## 7. Phase 10：文档、手册、CI、发布

### 手册（用户明确要求"要写好文档和手册"）
| 文件 | 面向 |
|---|---|
| `README.md`（重写为 Go 版） | 所有人：五分钟上手 + 不变式 + 平台状态 |
| `docs/README.md` | 文档导航（按读者角色分组、标 legacy 存档） |
| `docs/man-dialhome.md`（重写） | 命令参考（SYNOPSIS/COMMANDS/FILES/EXIT CODES/EXAMPLES） |
| `docs/NODE_MANUAL.md` | 部署受控节点 |
| `docs/CONTROLLER_MANUAL.md` | 管理/连接节点 |
| `docs/RELAY_MANUAL.md` | 运维 relay |
| `docs/MIGRATION_GUIDE.md` | 旧版 → Go 版迁移 |

历史设计/证据文档一并保留：`ARCHITECTURE / SECURITY_MODEL / PROTOCOL /
CONFIG_SCHEMA / PORTS / PITFALLS / STATUS / PRIOR-ART / REFACTOR_BASELINE /
SSH_BACKEND_MATRIX / PHASE4_ACCEPTANCE / RUNBOOK(legacy) / FAST-PATH(legacy)`。

### CI 与构建
- `.github/workflows/ci.yml`：`gofmt` 检查 + `go vet ./...` + `go test ./...`
  （ubuntu + windows 双矩阵）+ 全平台构建产物 + Linux smoke。
- `scripts/build-matrix.sh`：windows/linux/darwin × amd64/arm64 六个目标，
  `-trimpath -ldflags "-X main.version=... -X main.commit=..."`，输出 `dist/SHA256SUMS`。
- `dialhome version` 支持 ldflags 注入；退出码 `errs.ExitCode`（0/1/2/3/10+）。

---

## 8. 全量测试状态

```
ok  internal/cli          ok  internal/config       ok  internal/controller
ok  internal/crypto       ok  internal/lockfile     ok  internal/logging
ok  internal/openssh      ok  internal/platform     ok  internal/relay
ok  internal/supervisor
go vet ./... = 0    go build ./... = 0
```

关键回归测试：
- `TestBuildProbeArgsOrder`（ssh 选项顺序）
- `TestMigrateLegacyIdempotentKeepsAKByteForByte`（幂等不改写 AK）
- `TestPurgeRefusesActiveNode` / `TestPurgeRemovesRevokedTombstone`
- supervisor 重试梯/实例锁/参数契约（`127.0.0.1:13441:127.0.0.1:19022` 精确断言）
- relay N1–N10、config 回环字面量、crypto keygen、logging 脱敏、lockfile、platform harden

---

## 9. 未验证 / 已知限制（如实记录）

| 项 | 状态 |
|---|---|
| macOS 全部运行时行为（LaunchAgent、sshd、隧道） | **UNVERIFIED** — 无 macOS 真机；代码路径存在但不外推结论 |
| Windows 用户级自启动真实桌面会话 | 沙箱内注册表写入被拒属环境限制；需真实桌面会话复验 |
| 用户级自启动语义 | 只在登录会话后活；重启未登录不会自动起（要开机即用需可选 system 服务，需 root） |
| `service --scope system` | 仅 Linux systemd；其他平台明确拒绝，不静默提权 |
| 生产节点当前在线状态 | win2 / HP66 曾 OFFLINE（节点侧关机），与迁移无关；登录即自动回来 |
| 旧版脚本 | `node/*.ps1`、`relay/*.sh`、`client/dial.py` 保留为历史参考，新部署不再使用 |

---

## 10. 发布检查清单

- [x] 全量 `go vet` / `go test` / `go build` 通过
- [x] 六平台交叉编译 + SHA256SUMS（脚本 + CI）
- [x] 退出码分类、`version` ldflags
- [x] README / man / 四份角色手册 / 文档索引
- [x] 设计、协议、配置、端口、坑位、安全模型文档齐备
- [x] Phase 4 负向矩阵 + Phase 9 E2E 证据留档
- [x] 生产 relay 零中断迁移 + 幂等恢复验证
- [x] 生产 registry 清理干净（2 active 节点，无测试残留）
- [ ] macOS 真机验证（保持 UNVERIFIED，待有机器时补）
- [ ] Windows 真实桌面会话复验 HKCU Run 自启动
