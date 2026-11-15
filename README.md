# dialhome

把**没有公网 IP、不能开入站端口、不给管理员**的机器拨回家。

一台中转机（*relay*）只需要原版 OpenSSH + `authorized_keys`；每台被控机主动**出站**连上去，
在 relay 的**回环口**上开一对反向端口；你在工作站经 relay 回拨进去。
外部扫描永远看不到节点，邮箱/路由器只映射 relay 一个端口。

Go 单二进制，三个角色同仓：`node` / `controller` / `relay`。

> **分支说明**
> - `main` — 当前版本：Go 重构版（本文件所述）。
> - `pwsh` — **存档分支**：旧 PowerShell + Bash + Python 版（原 `main`，末提交 `46b3468`，
>   另打不可变 tag `pwsh-legacy-final`）。仅作历史参考，新部署请勿使用。
> - `go-refactor` — 重构工作分支，内容与 `main` 相同，保留以备追溯。

```
   节点（NAT 后的 Windows / Linux）        relay（Linux，唯一有公网口）        你的工作站
 ┌──────────────────────────────┐      ┌──────────────────────────────┐
 │ rootless sshd  127.0.0.1:19xxx│◄────┤ 127.0.0.1:<node的SSH端口>      │── controller ──► 你
 │ 任意本地服务   127.0.0.1:xxxx  │◄────┤ 127.0.0.1:<node的APP端口>      │
 │                              │  -R  │                              │
 │ dialhome node run ──出站──────┼─────►│ sshd（唯一对外端口）           │
 └──────────────────────────────┘      └──────────────────────────────┘
```

---

## 三条不变式

| 不变式 | 含义 |
|---|---|
| **零管理员** | 不写系统配置、不装系统服务、不需 UAC/sudo、不动防火墙。默认自启动是**用户级**（Windows `HKCU\Run` / Linux `systemd --user` / macOS LaunchAgent）。 |
| **回环限定** | 所有通道地址是**字面量** `127.0.0.1:<port>`。代码里拒绝 `localhost` / `0.0.0.0`，relay 侧用 `permitlisten` 精确匹配。 |
| **三身份分离** | Node Tunnel Key（**本机生成，私钥不出机**）、Controller Management Key（只在控制器）、一次性 Enrollment Credential（只够提交一把公钥）。 |

详见 [docs/SECURITY_MODEL.md](docs/SECURITY_MODEL.md)（含 N1–N10 负向测试矩阵）。

---

## 五分钟上手

### 1) relay：一次性初始化（在 relay 上，普通用户即可）

```bash
dialhome relay init                      # 建 ~/.dialhome，管理系统 authorized_keys
dialhome relay init --advertise 127.0.0.1:22   # 同机部署：节点该拨的本机 sshd 端口
```

> relay 数据目录**恒定** `~/.dialhome`。节点侧 `DIALHOME_DATA_DIR` 覆盖只影响 node/controller。

### 2) relay：签发一张一次性凭证

```bash
# 控制器公钥先放到 relay（同机则直接指定路径）
dialhome relay credentials issue my-node --manager-pub ~/.dialhome/manager.pub
# 输出 CREDENTIAL-B64 = ... ；把它（或解码后的 JSON 文件）交给节点
```

凭证**一次性、短期**，能力仅为提交一把 tunnel 公钥，绑死预分配槽位。

### 3) 节点：装一次（普通用户，无需管理员）

```powershell
# Windows（把凭证 json 放好；--openssh-zip 可指定本地固定版本包）
dialhome node install --mode persistent --credential C:\path\cred.json

# Linux
dialhome node install --mode persistent --credential /path/cred.json
```

安装会：本地生成 host key + tunnel key（**私钥不出机**）→ 用凭证私钥向 relay 提交 tunnel 公钥
→ 写 `config.json` / `identity.json` / 节点自己的 `authorized_keys` → 按 `--mode` 决定是否注册用户级自启动。

### 4) 节点：跑起来

```powershell
dialhome node run      # 前台：rootless sshd 子进程 + 多通道 supervisor + 实例锁
dialhome node status   # 通道与 relay 监听状态
```

`--mode persistent` 下登录即自启；`temporary` 不写任何自启动项。
重试梯：5 → 10 → 20 → 40 → 60s，≥10 次失败转 3600s；**认证失败立即跳小时级**；成功清零。
开机早于网络时先探测 relay ≤10 分钟（30s 一次）。

### 5) 控制器：连进去

```bash
dialhome controller init          # 生成 manager 密钥对（私钥留本机；公钥发到 relay 的 manager.pub）
dialhome controller list          # 已注册机器
dialhome controller anchors my-node   # 逐条打印锚点探测结果（失败给真实原因）
dialhome controller exec my-node 'hostname && whoami'
dialhome controller shell my-node     # 交互式 PTY
```

`machines.json` 支持两类锚点：你**自己的 `~/.ssh/config` 别名**（从不迁移），
或 dialhome 锚点（经 relay 的 ProxyJump + manager key 落节点回环口）。

**真机验收**（relay 102 / Debian 13）：`controller exec e2e-node 'hostname && whoami'` → `debian-h61` / `user`。

---

## 平台与验证状态

| 平台 | 状态 |
|---|---|
| Linux（Debian 13 / OpenSSH 10.0p1） | **已实测**：rootless sshd、隧道、enroll、controller exec、systemd --user + XDG 自启动 |
| Windows（Win11 / Win32-OpenSSH 10.0.0.0p2 Preview） | **已实测**：便携 sshd、隧道、enroll、install/uninstall；用户级自启动需真实桌面会话复验（沙箱内注册表写入被拒，属环境限制） |
| macOS | **UNVERIFIED**：代码有 LaunchAgent 路径，本重构无 macOS 真机 |

---

## 命令总览

```
dialhome node install|run|status|stop|uninstall|logs
dialhome controller init|list|status|anchors|shell|exec
dialhome relay init|list|inspect|revoke|purge|ports|enroll|migrate-legacy|credentials issue|revoke
dialhome relay-helper --port <p> tunnel | --credential <id> enroll    # 内部 forced command
dialhome autostart install|uninstall|status            # 用户级，无管理员
dialhome service install|uninstall|status --scope system   # 可选，需 root，永不默认
dialhome migrate --from-legacy [--legacy-dir DIR]
dialhome doctor
dialhome keygen --out FILE [--comment TEXT]
dialhome version
```

退出码分类见 [internal/errs/errs.go](internal/errs/errs.go)：`0` 成功、`1` 一般失败、`2` 用法错误、`3` 未实现、`10+` 领域码（doctor 用）。

---

## 从旧版迁移

旧版（`main` 分支的 PS/Bash/Python 版）状态全在 relay：系统 `authorized_keys` 里的
`magpie-node-*` / `magpie-tunnel-node-*` 行 + `~/.magpie-connector/machines.json`。

```bash
dialhome relay migrate-legacy ~/.magpie-connector     # 或 dialhome migrate --from-legacy
```

迁移**只改 forced command**，key body 与端口**逐字节不变**，先写备份，生产隧道零中断。
命令**幂等**：已迁移的行只补登 registry，**完全不改 authorized_keys 字节**（有回归测试锁定）。
生产实测：2 个旧节点导入后，旧私钥经新 Go helper 仍可用，AK 与备份 KEYS-IDENTICAL。

详见 [docs/MIGRATION_GUIDE.md](docs/MIGRATION_GUIDE.md)。

---

## 文档

| 我要… | 读这个 |
|---|---|
| 部署一台节点 | [docs/NODE_MANUAL.md](docs/NODE_MANUAL.md) |
| 管理/连接节点 | [docs/CONTROLLER_MANUAL.md](docs/CONTROLLER_MANUAL.md) |
| 运维 relay | [docs/RELAY_MANUAL.md](docs/RELAY_MANUAL.md) |
| 从旧版迁移 | [docs/MIGRATION_GUIDE.md](docs/MIGRATION_GUIDE.md) |
| 理解设计/安全边界 | [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) · [docs/SECURITY_MODEL.md](docs/SECURITY_MODEL.md) |
| 看协议/配置/端口 | [docs/PROTOCOL.md](docs/PROTOCOL.md) · [docs/CONFIG_SCHEMA.md](docs/CONFIG_SCHEMA.md) · [docs/PORTS.md](docs/PORTS.md) |
| 排障 | [docs/PITFALLS.md](docs/PITFALLS.md) · [docs/SSH_BACKEND_MATRIX.md](docs/SSH_BACKEND_MATRIX.md) |
| 命令手册 | [docs/man-dialhome.md](docs/man-dialhome.md) |
| 看验收证据 | [docs/REFACTOR_FINAL_REPORT.md](docs/REFACTOR_FINAL_REPORT.md) · [docs/PHASE4_ACCEPTANCE.md](docs/PHASE4_ACCEPTANCE.md) |

全部文档索引：[docs/README.md](docs/README.md)。

---

## 构建与发布

```bash
# 本机
go build ./cmd/dialhome

# 全平台 + 校验和（见 scripts/build-matrix.sh）
./scripts/build-matrix.sh            # windows/linux/darwin × amd64/arm64 -> dist/ + SHA256SUMS
```

CI：[.github/workflows/ci.yml](.github/workflows/ci.yml)（vet + test + 跨平台构建 + 校验和）。

---

## 仓库结构

```
dialhome/
  cmd/dialhome/         单二进制入口（三角色子命令）
  internal/
    cli/                node / controller / relay / autostart / service / migrate / doctor
    relay/              authorized_keys 结构化读写 + registry + forced-command helper
    supervisor/         多通道隧道守护（重试梯 / 开机等待 / 实例锁）
    config/ crypto/ identity/ openssh/ platform/ logging/ lockfile/ errs/ controller/
  docs/                 ★ 手册、设计、验收证据（见 docs/README.md）
  scripts/build-matrix.sh   跨平台构建 + SHA256SUMS
```

旧版脚本（`node/*.ps1`、`relay/*.sh`、`client/dial.py`）保留在仓库作历史参考，
**新部署不要再用**；它们的行为已被 Go 版覆盖并有测试与真机证据。

**不进仓库**（`.gitignore`）：私钥、`*.zip`、`*.b64`、日志、`dist/`。
