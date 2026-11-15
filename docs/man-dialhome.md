# DIALHOME(1) — dialhome 手册页

## 名称

**dialhome** — 为 NAT 之后、无管理员权限（rootless）的机器提供反向 SSH 接入：节点出站一条 SSH 隧道把自身回环端口对反向注册到中继（relay），由 Controller 经中继登入节点取 shell。

## 概要

按角色（role）分组的完整命令表。所有命令形式为 `dialhome <role> <subcommand> [flags]`。

**node（受控节点，装在被管机器上）**
```
dialhome node install [--mode temporary|persistent] [--credential FILE] [--relay USER@HOST:PORT] [--name NAME] [--openssh-zip FILE]
dialhome node run | status | stop | uninstall | logs [channel]
```

**controller（总控，登入节点取 shell）**
```
dialhome controller init | list | status | anchors <node> | shell <node> | exec <node> <command...>
```

**relay（Linux 中继，管理 authorized_keys 与登记）**
```
dialhome relay init [--advertise HOST:PORT] [--relay-user USER] | list | inspect <name> | revoke <name> | purge <name> | ports
dialhome relay credentials issue <name> [--manager-pub FILE] | revoke <id>
dialhome relay migrate-legacy [LEGACY-DIR]        # 幂等：仅重写 forced command，重登记存活 AK 行
```

**relay-helper（内部强制命令，由 relay 的 sshd 调起，不要手动调用）**
```
dialhome relay-helper --port <p> tunnel | --credential <id> enroll   # 旗标必须放在位置动词之前
```

**autostart（用户级自启动，无管理员、永不提权）**
```
dialhome autostart install | uninstall | status
```

**service（可选系统级服务，需 root，永不默认）**
```
dialhome service install | uninstall | status --scope system
```

**其余顶层命令**
```
dialhome migrate --from-legacy [--legacy-dir DIR]
dialhome doctor
dialhome keygen --out FILE [--comment TEXT]
dialhome version
```

## 描述

dialhome 是一个 Go 二进制（`cmd/dialhome`），通过子命令区分四种角色：**node**（受控机器）、**controller**（总控）、**relay**（Linux 中继）、以及内部使用的 **relay-helper**（被中继 sshd 的 forced command 调起）。Windows 发布两型：`dialhome.exe`（console，用于 Controller/交互）与 `dialhomed.exe`（`-H windowsgui`，Node 后台/自启入口）；Linux/macOS 单一二进制。relay 不跑常驻 dialhome 进程，只有 (a) 被 sshd 调起的 `relay-helper` 一次一进程，与 (b) Controller 经 SSH 调起的一次性 `relay <sub>` 命令。

核心不变式（所有文档的公共前提）：

- **三身份**：节点隧道 key（本机生成、私钥不出机）、Controller 管理 key（仅存 Controller）、短期一次性登记凭证。三者互不通用。
- **回环字面量**：所有端口绑定均为 `127.0.0.1:` 字面常量，由 Go 代码硬编码构造，不接受配置注入；不允许 `localhost`/`0.0.0.0` 替换。
- **无管理员**：节点全程 rootless 便携 sshd + 用户级自启动；relay 侧零新增服务（仅原版 sshd + `authorized_keys` 选项）。

## 命令

### dialhome node install

**语法**
```
dialhome node install [--mode temporary|persistent] [--credential FILE] [--relay USER@HOST:PORT] [--name NAME] [--openssh-zip FILE]
```

**参数**
- `--mode`：`persistent`（默认，注册用户级自启动，后台常驻）或 `temporary`（仅进数据目录、无自启、无备份，隧道前台运行，Ctrl+C/关窗即断）。
- `--credential FILE`：登记凭证文件；支持 `@b64` 字面量直接内联（`@` 后接 base64 编码的凭证 JSON）。必填。
- `--relay USER@HOST:PORT`：中继地址，默认 `user@10.101.54.192:18322`。若凭证内置了 `relay_address`/`relay_port`/`relay_user`，以凭证为准。
- `--name NAME`：节点名；缺省取凭证的 `node_name`，否则报错退出。
- `--openssh-zip FILE`：仅 Windows；本地 `OpenSSH-Win64.zip` 路径，跳过下载。

**行为**：在本机生成 host key 与 tunnel key（ed25519，0600）；经凭证的私钥通过 forced command 一次性提交 tunnel 公钥完成登记（凭证私钥用后即删）；把 Controller 管理公钥写入本机 sshd 的 `authorized_keys`；生成 `config.json`/`identity.json`；`persistent` 模式写入用户级自启动入口。`temporary` 模式不注册自启动，安装后手动 `dialhome node run`。私钥永不出机、永不入日志。

**退出码**：0 成功；2 用法错误（`--mode` 非预期值、缺 `--credential`、缺节点名）；1 其他失败。

**示例**
```
dialhome node install --credential node-hp66.json --name hp66 --mode persistent
dialhome node install --credential @eyJjcmVk... --mode temporary
```

### dialhome node run

**语法**：`dialhome node run`

**行为**：加载配置，获取实例锁（已有 supervisor 则报错退出）；以子进程方式启动本机 sshd（`-D` 前台可观察/可杀）；拉起每通道独立的隧道 supervisor（SIGINT/TERM 拆除全部子进程）。`temporary` 在前台运行；`persistent` 由自启动入口在登录时拉起。

**退出码**：0 正常退出；1 supervisor/sshd 启动或运行失败；16 隧道被中继拒绝（`permitlisten`）；17 端口被他节点占用；12 隧道进程缺失/退出。

### dialhome node status

**语法**：`dialhome node status`

**行为**：打印节点名、模式，及每个通道的 `本地 -> 远程` 与状态（`LISTENING on relay` 或 `tunnel down`）。无配置时仅报 `CONFIG = missing` 并成功退出（0）。

### dialhome node stop

**语法**：`dialhome node stop`

**行为**：读取 `state/sshd.pid` 杀死本机 sshd；隧道 supervisor 在下一拍退出。**不删除数据目录**。

### dialhome node uninstall

**语法**：`dialhome node uninstall`

**行为**：移除用户级自启动入口；保留数据目录（含密钥），需手动删除以彻底清除密钥。

### dialhome node logs

**语法**：`dialhome node logs [channel]`

**行为**：打印 `logs/<channel>.log` 末 40 行；`channel` 默认 `node`。

### dialhome controller init

**语法**：`dialhome controller init`

**行为**：生成管理密钥对 `keys/manager.key`（私钥，仅本机）与 `keys/manager.pub`（公钥）。把 `manager.pub` 复制到 relay 的 `~/.dialhome/manager.pub`（`scp`）。已存在则跳过生成。

### dialhome controller list / status

**语法**：`dialhome controller list` ； `dialhome controller status`

**行为**：`list` 列出 `machines.json` 中已注册机器（NAME/SHELL/ANCHORS/备注）；`status` 对每个机器按 anchors 顺序 failover 探测，输出 `[UP]|[DOWN]` 及最后可用锚点。

### dialhome controller anchors

**语法**：`dialhome controller anchors <node>`

**行为**：打印该机器的锚点探测轨迹（ssh-alias / dialhome / local-port 三类锚点按序探测）。

### dialhome controller shell

**语法**：`dialhome controller shell <node>`

**行为**：经 failover 命中首个可用锚点，以 `-t` 分配 PTY 进入交互式 shell（stdin/stdout/stderr 直连当前终端）。

### dialhome controller exec

**语法**：`dialhome controller exec <node> <command...>`

**行为**：在节点上执行远端命令。`shell` 为 `bash` 时脚本经 stdin 以 `bash -s` 承载，避免多层引号嵌套；其余以远端命令串作为 ssh 尾参。命令以参数数组传递，绝不经本地 shell 二次解析。

**退出码**：0 成功；1 全部锚点失败（`ErrGeneric`）；2 用法错误（参数不足）。

### dialhome relay init

**语法**：`dialhome relay init [--advertise HOST:PORT] [--relay-user USER]`

**行为**：初始化 `registry.json`（默认）+ helper 路径 + 通告地址（默认 `10.101.54.192:18322`，`--advertise` 覆盖；`--relay-user` 设置节点拨入的 SSH 账户，默认取 `$USER`/`$USERNAME`）；要求系统 `authorized_keys`（测试可用 `DIALHOME_RELAY_DIR` / `DIALHOME_RELAY_AK` 重定向）（`~/.ssh/authorized_keys`）已存在，否则报错。所有 AK 写操作原子写 + 时间戳备份 + `flock` 串行化。

### dialhome relay list / inspect / ports

**语法**：
```
dialhome relay list
dialhome relay inspect <name>
dialhome relay ports
```

**行为**：`list` 列出节点表（NODE/STATE/APP/SSH/TUNNEL-FP/HW）；`inspect` 输出该节点记录的 JSON；`ports` 列出已用回环端口。

### dialhome relay revoke / purge

**语法**：
```
dialhome relay revoke <name>
dialhome relay purge <name>
```

**行为**：`revoke` 将节点置为 `revoked` 并删除其 AK 行（端口立即从 registry 释放，监听需等 sshd 重键或超时消失）。`purge` **仅**删除 `revoked` 状态的节点墓碑及其绑定凭证、释放端口对；对 `active`/`pending` 节点拒绝（防误删生产节点）。

### dialhome relay credentials issue / revoke

**语法**：
```
dialhome relay credentials issue <name> [--manager-pub FILE]
dialhome relay credentials revoke <id>
```

**行为**：`issue` 生成 ed25519 登记凭证，写入 AK 行与 `registry.json`，输出 `CREDENTIAL-B64`（交给 `dialhome node install --credential` 消费）；有效期 24 小时；`--manager-pub` 指定管理公钥，缺省读取 `~/.dialhome/manager.pub`。`revoke` 删除指定 `cred-xxxx` 凭证及其 AK 行。另有同义的 `dialhome relay enroll <name>` 形式（spec §5.2 亦使用）。

### dialhome relay migrate-legacy

**语法**：`dialhome relay migrate-legacy [LEGACY-DIR]`

**行为**：幂等地把旧 PS 体系（`magpie-node-*`/`magpie-tunnel-node-*`）AK 行与旧 `machines.json` 导入新 `registry.json`，**只重写 forced command**（保留原 key 与原端口，不扰动正在运行的隧道）。`LEGACY-DIR` 默认 `~/.magpie-connector`。已迁移的行逐字节保留，可重复运行。

### dialhome relay-helper

**语法**：
```
dialhome relay-helper --port <p> tunnel
dialhome relay-helper --credential <id> enroll
```

**行为**：内部 forced command 入口，由 relay sshd 经 `command=` 调起，读取 `SSH_ORIGINAL_COMMAND` 分派。`--port <p>` 对应隧道 forced command；`--credential <id>` 对应登记 forced command。**旗标必须放在位置动词之前**（`flag` 包遇首个位置参数即停）。另有 `--ak`（系统 AK 路径）、`--base`（relay 数据目录，测试用）。正常不应手动调用。

### dialhome autostart install / uninstall / status

**语法**：
```
dialhome autostart install
dialhome autostart uninstall
dialhome autostart status
```

**行为**：用户级（无管理员）自启动。Windows 写 HKCU Run 指向 `dialhome(d).exe node run`；Linux 写 systemd `--user` 单元（无 user bus 时回退 XDG autostart）；macOS 写 `~/Library/LaunchAgents/dialhome-node.plist`。`status` 报告当前存在的用户级自启动项。

### dialhome service install / uninstall / status

**语法**：`dialhome service install | uninstall | status --scope system`

**行为**：**可选**系统级服务，需 root，绝不默认安装。`--scope` 仅支持 `system`（用户级请用 `autostart`）。仅 Linux（systemd）实现；Windows/macOS 直接拒绝。用户级 `autostart` 永远是零管理员的默认方案。

### dialhome migrate

**语法**：`dialhome migrate --from-legacy [--legacy-dir DIR]`

**行为**：必须带 `--from-legacy`；委托与 `relay migrate-legacy` 相同的幂等导入器（唯一迁移路径）。`--legacy-dir` 默认 `~/.magpie-connector`。保留 host key / tunnel key / 端口 / AK，不重分配身份。

### dialhome doctor

**语法**：`dialhome doctor`

**行为**：只读诊断——打印平台、节点数据目录、配置、identity、tunnel key、manager key（若 Controller 同机）、自启动状态。**不改任何状态**。

### dialhome keygen

**语法**：`dialhome keygen --out FILE [--comment TEXT]`

**行为**：生成 ed25519 密钥对，私钥以 0600 + 平台加固写入 `--out`，公钥行打印到 stdout。`--comment` 默认 `dialhome-node`。`node install` 内部与测试均使用它。

### dialhome version

**语法**：`dialhome version`

**行为**：打印 `dialhome <version>`（release 构建由 `-ldflags` 注入；开发默认 `dev`）。

## 安全模型

三类身份（详见 `docs/SECURITY_MODEL.md`）：

- **A. 节点隧道 key（每节点一把）**：ed25519 私钥在节点本机生成，只落 `keys/tunnel.key`（0600，仅本用户）；公钥经登记凭证提交 relay，绑定该节点端口槽位。relay 侧 AK 行（逐字模板，`127.0.0.1` 不可替换）：
  ```
  restrict,command="<helper> --port <SSH_PORT>",port-forwarding,permitlisten="127.0.0.1:<SSH_PORT>",permitlisten="127.0.0.1:<APP_PORT>",permitopen="127.0.0.1:1" ssh-ed25519 <pub> dialhome-node-<name>
  ```
  能力上限：仅 `-R 127.0.0.1:<两个指定回环口>`；无 shell、无文件读、无 `-L`（`permitopen=127.0.0.1:1` 把 `-L` 全废）；不能占他人端口（`permitlisten` 精确匹配）。
- **B. Controller 管理 key**：私钥只在 Controller（`keys/manager.key`）；公钥写入**每个节点**的 `authorized_keys` 取其 shell。绝不进安装包、绝不进 HTTP 响应体。
- **C. 登记凭证（短期一次性）**：ed25519 专用对，relay 侧 AK 行 `restrict,command="<helper> enroll",no-port-forwarding,no-pty,no-user-rc` + registry 记录。能力仅 `enroll` 原语——提交节点公钥并绑定**凭证预分配**的节点槽位，不能建隧道、不能登任何机器。一次性、过期作废、可 `relay credentials revoke`。

**回环字面量**：所有通道地址硬编码为 `127.0.0.1:` 字面量，由 Go 构造，不接受配置注入；`-R 0.0.0.0` / `-R <port>`（裸端口默认绑 localhost）均被 `permitlisten` 拒绝（见 `docs/PITFALLS.md` B1/B8）。

## 文件

各角色数据目录由 `internal/platform.DataDir(role)` 决定。relay **恒定**为 `~/.dialhome`（三平台统一，贴近 ssh 生态）；node / controller 为平台 DataDir。

| 角色 | Windows | Linux | macOS |
|---|---|---|---|
| **relay** | `~/.dialhome`（恒定） | `~/.dialhome`（恒定） | `~/.dialhome`（恒定） |
| **node** | `%LOCALAPPDATA%\dialhome`（`C:\Users\<用户>\AppData\Local\dialhome`） | `$XDG_DATA_HOME/dialhome`（默认 `~/.local/share/dialhome`） | `~/Library/Application Support/dialhome` |
| **controller** | `%APPDATA%\dialhome`（`C:\Users\<用户>\AppData\Roaming\dialhome`） | `$XDG_CONFIG_HOME/dialhome`（默认 `~/.config/dialhome`） | `~/.config/dialhome` |

- **relay 内容**：`~/.dialhome/registry.json`、`registry.lock`、`manager.pub`、`backups/`；被管理的系统 `authorized_keys` 为 `~/.ssh/authorized_keys`（helper 直接维护，无 Include）。
- **node 内容**：`config.json`、`identity.json`、`keys/`（host_ed25519、tunnel.key、authorized_keys）、`openssh/`、`logs/`、`state/`。
- **controller 内容**：`machines.json`、`keys/`（manager.key、manager.pub）、`known_hosts`。

**`DIALHOME_DATA_DIR` 覆盖**：适用于 node / controller（测试与自定义部署），**不适用 relay**——relay helper 运行在 sshd 子进程里，其环境与调用者 shell 不一致，若套用该覆盖会导致凭证"未知"。relay 的测试覆盖请用 `DIALHOME_RELAY_DIR`（或 `relay-helper --base`）。

## 退出码

来自 `internal/errs/errs.go`：

| 码 | 含义 |
|---|---|
| `0` | 成功（`ExitOK`） |
| `1` | 通用域失败（`ErrGeneric`） |
| `2` | 用法错误（`ErrUsage`） |
| `3` | 尚未实现（`ErrNotImplemented`，如 `relay reconcile/verify` 落到 Phase 6/9） |
| `10` | `ExitSSHDNotFound` — doctor：便携 sshd 不存在 |
| `11` | `ExitSSHDDown` — doctor：sshd 已配置但未运行 |
| `12` | `ExitTunnelDown` — doctor/supervisor：隧道进程缺失或退出 |
| `13` | `ExitRelayUnreach` — doctor：relay 端口不可达 |
| `14` | `ExitHostKeyMismatch` — doctor：relay 侧 ssh host key 变更 |
| `15` | `ExitAuthFailed` — doctor：公钥认证被拒 |
| `16` | `ExitListenRefused` — doctor/relay：relay 拒绝反向监听（`permitlisten`） |
| `17` | `ExitPortTaken` — doctor/relay：端口被他节点占用 |
| `18` | `ExitManagerKeyBad` — doctor：Controller 管理 key 被节点拒绝 |
| `19` | `ExitShellUnusable` — doctor：节点用户 shell 为 nologin/缺失 |

## 示例

```
# 1. 中继上发登记凭证（含管理公钥），交给节点安装消费
dialhome relay credentials issue hp66 --manager-pub ~/.dialhome/manager.pub
#    -> 输出 CREDENTIAL-B64，复制给节点

# 2. 节点安装并持久常驻
dialhome node install --credential @<CREDENTIAL-B64> --name hp66 --mode persistent

# 3. 前台临时排查
dialhome node install --credential node.json --mode temporary
dialhome node run

# 4. 总控登入节点 shell / 执行命令
dialhome controller init
dialhome controller shell hp66
dialhome controller exec hp66 'systeminfo | head'

# 5. 排障
dialhome doctor
dialhome node logs
dialhome relay list
```

## 环境变量

| 变量 | 作用 |
|---|---|
| `DIALHOME_DATA_DIR` | 覆盖 node / controller 数据目录（测试与自定义部署）。**relay 不使用**（见上文）。 |
| `DIALHOME_RELAY_DIR` | 覆盖 relay 数据目录（relay helper 测试用）。 |
| `SSH_ORIGINAL_COMMAND` | relay-helper 用于接收 forced command 载荷（由 sshd 注入）。 |

## 另见

`docs/README.md`（文档索引）、`docs/ARCHITECTURE.md`、`docs/SECURITY_MODEL.md`、`docs/CONFIG_SCHEMA.md`、`docs/PROTOCOL.md`、`docs/PITFALLS.md`、`docs/PORTS.md`、`docs/STATUS.md`、`docs/SSH_BACKEND_MATRIX.md`、`docs/REFACTOR_BASELINE.md`、`docs/PHASE4_ACCEPTANCE.md`、`docs/PRIOR-ART.md`、`docs/NODE_MANUAL.md`、`docs/CONTROLLER_MANUAL.md`、`docs/RELAY_MANUAL.md`、`docs/MIGRATION_GUIDE.md`。

## 已知限制

- **macOS 标记为 UNVERIFIED**：当前无任何 macOS 实机/VM，未做真机验证。Windows 与 Linux 均已真机验证（Windows 生产 + 复测；Linux 中继 Debian 13/OpenSSH 10 生产 sshd）。
- **可选系统服务**：代码与单元测试已就位，但实机验证标记为 UNVERIFIED，且**永不默认安装**；用户级 `autostart` 才是零管理员默认方案。Windows/macOS 上 `service` 直接拒绝。
- **`relay reconcile/verify`** 落在 Phase 6/9（需节点通道在线），当前返回退出码 3（未实现）。
- 端口分配器的 `ss -ltn` 实时监听探测在 Windows 开发机上不可用 → 降级为仅 registry 分配；生产 relay 为 Linux 不受影响。
- OpenSSH 10 对认证失败的来源 IP 有惩罚性拒绝（per-source penalty）；supervisor 在认证失败时须重度退避，不可按网络失败节奏重试。
- 迁移（`migrate` / `relay migrate-legacy`）保留 host key / tunnel key / 端口 / AK，绝不重分配身份。
