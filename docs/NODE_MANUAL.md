# Node 运维手册（受控机侧）

> 适用对象：需要在「无管理员、NAT 后、无公网 IP」的机器上安装并维持一条回连隧道的运维/用户。
> 本手册只讲 **Node** 侧（`dialhome node ...`）。Controller 侧见 [CONTROLLER_MANUAL.md](./CONTROLLER_MANUAL.md)；
> 安全与身份模型见 [SECURITY_MODEL.md](./SECURITY_MODEL.md)；配置字段见 [CONFIG_SCHEMA.md](./CONFIG_SCHEMA.md)；
> 架构与进程模型见 [ARCHITECTURE.md](./ARCHITECTURE.md)。
> 凡命令、字段均取自 `internal/cli/node.go`、`internal/config/config.go`、`internal/supervisor/*`，以代码为准。

---

## 1. 这是什么

Node 是被远程管控的那台机器。安装后它会：

- 在**本机**起一个仅监听回环的 `sshd`（`ssh.exe -D` 子进程，Windows 用 `CREATE_NO_WINDOW` 无窗口）；
- 用一条**反向隧道**（每个通道一条独立 `ssh -R 127.0.0.1:<relay口>:127.0.0.1:<本机口>`）把本机服务暴露到 Relay 的回环口；
- 由 **supervisor** 守护这些子进程：崩溃自动重连、单实例互斥、开机等待、指数退避重试。

三个身份（详见 SECURITY_MODEL §1）：

| 身份 | 私钥落在哪 | 用途 |
|---|---|---|
| **Node Tunnel Key** | **只在本机** `keys/tunnel.key`（本地生成，私钥不过网） | 建立到 Relay 的反向隧道 |
| Controller Management Key | 只在 Controller（`keys/manager.key`） | 取本机 shell/执行命令 |
| Enrollment Credential | 一次性、短期，安装即用后即焚 | 把 Node 公钥登记到 Relay |

> **回环不变式**：所有通道地址都是字面量 `127.0.0.1:<端口>`。**绝不能**写成 `localhost` 或 `0.0.0.0`——Relay 的 `permitlisten` 精确匹配 `127.0.0.1`，否则隧道建立会被拒（坑位 B1）。

---

## 2. 前置条件

- 已拿到一个 **Enrollment Credential** 文件（JSON，由 Controller/Relay 产出，见 §4）。
- 目标机**不需要管理员/UAC**：本机 sshd 跑在高位回环端口、仅密钥、仅当前用户，不动系统 SSH、不动防火墙。
- Windows：需要能联网（首次 install 会下载/校验固定版本的 `OpenSSH-Win64.zip`，可用 `--openssh-zip` 离线指定本地包）。
- Linux/macOS：使用系统自带 `ssh`/`sshd`（路径回退 `/usr/bin/ssh`、`/usr/sbin/sshd` 等）。
- 数据目录（用户级，无需提权）：
  - Windows：`%LOCALAPPDATA%\dialhome\`
  - Linux：`~/.local/share/dialhome/`
  - macOS：`~/Library/Application Support/dialhome/`

---

## 3. 安装（两种模式）

### 3.1 命令

```powershell
# Windows PowerShell（temporary 模式，前台跑，Ctrl+C 即断）
.\dialhome.exe node install --mode temporary --credential .\cred.json --name hp66

# Windows（persistent 模式，登录后自启）
.\dialhome.exe node install --mode persistent --credential .\cred.json --name hp66

# Linux / macOS bash
./dialhome node install --mode persistent --credential ./cred.json --name hp66
```

完整签名（`internal/cli/node.go`）：

```
dialhome node install [--mode temporary|persistent] [--credential FILE]
                      [--relay USER@HOST:PORT] [--name NAME] [--openssh-zip FILE]
```

| 参数 | 必填 | 说明 |
|---|---|---|
| `--mode` | 否 | `persistent`（默认）或 `temporary` |
| `--credential` | **是** | 登记凭证文件；也支持 `@<base64>` 字面量 |
| `--name` | 否 | 节点名；缺省用凭证里的 `node_name` |
| `--relay` | 否 | `user@host:port`，默认 `user@10.101.54.192:18322`；**凭证里的 `relay_address/relay_port/relay_user` 会覆盖它** |
| `--openssh-zip` | 否 | 仅 Windows：指定本地 `OpenSSH-Win64.zip`，跳过下载 |

### 3.2 install 真实做了什么（按代码顺序）

1. 建数据目录（`data/ keys/ logs/ state/ openssh/`，权限 `0700`）。
2. **Windows 取便携 OpenSSH**：下载（或读 `--openssh-zip`）→ 校验固定 sha256 → 用 **Go 标准库 `archive/zip`** 解压（见 §9 坑位）。
3. **本机生成** host key 与 tunnel key（ed25519，`0600`，私钥只落本机 `keys/`，不过网）。
4. **登记（enroll）**：用凭证私钥经 Relay 的强制命令 `enroll <tunnel_pubkey>` 把本机隧道**公钥**绑定到该节点预分配的端口槽位；凭证私钥用完即删。
5. 把 **Controller 管理公钥**写入本机 `keys/authorized_keys`（`no-agent-forwarding,no-X11-forwarding`，前缀限制）。
6. 写 `config.json`（通道端口取自凭证 `ports_ssh/ports_app`）、写 `identity.json`（硬件指纹）。
7. 按模式收尾：
   - `persistent` → 注册**用户级**自启动（§6），install 进程退出；
   - `temporary` → 不注册自启，提示用 `dialhome node run` 前台运行。

安装输出示例（`ENROLL`、`CONFIG`、`SSH-EXE`、`TUNNEL-PUB`、`AUTOSTART` 等行），可用于确认每一步成功。

---

## 4. 登记凭证（credential）字段

`node install` 消费的凭证 JSON（`credentialDoc`，与代码 `json` tag 一致）：

```json
{
  "credential_version": 1,
  "node_name": "hp66",
  "private_key_openssh_b64": "<OpenSSH 格式 ed25519 私钥的 base64>",
  "manager_pubkey": "ssh-ed25519 AAAA... controller",
  "expires_at": "2026-11-13T15:00:00Z",
  "ports_ssh": 13441,
  "ports_app": 13440,
  "relay_address": "10.101.54.192",
  "relay_port": 18322,
  "relay_user": "user"
}
```

| 字段 | 说明 |
|---|---|
| `credential_version` | 凭证格式版本（int） |
| `node_name` | 节点名；install 的 `--name` 缺省取它 |
| `private_key_openssh_b64` | **凭证私钥**（OpenSSH 格式，base64）；仅用于 enroll 一次性登录，用完删除 |
| `manager_pubkey` | Controller 管理公钥，写入本机 authorized_keys（给 Controller 取 shell） |
| `expires_at` | 过期时间；过期/已用凭证无法再登记 |
| `ports_ssh` / `ports_app` | Relay 分配的回环端口（填入通道 `remote`） |
| `relay_address` / `relay_port` / `relay_user` | Relay 连接信息，覆盖 `--relay` |

> 凭证是**短期、一次性**的：登记完成即标记已用，泄露者无法登 Relay、无法登 Node。妥善保管，install 后即可删除。

---

## 5. 运行、状态、停止、卸载、日志

### 5.1 启动

```powershell
# temporary 模式前台跑（Ctrl+C / 关窗即全链拆除）
.\dialhome.exe node run
```

`persistent` 模式由自启动在登录后拉起；你也可以手动 `dialhome node run`（前台）。该命令会：
- 抢占**实例锁**（`state/supervisor.lock`）——已有 supervisor 在跑则报错退出；
- 起本机 `sshd -D` 子进程（Windows 无窗口）；
- 按 `config.json` 每通道起独立 `ssh -R` 隧道，由 supervisor 守护。

### 5.2 状态

```powershell
.\dialhome.exe node status
```

逐通道打印 `CHANNEL  name  local -> remote  [state]`，状态为 `LISTENING on relay`（探测到 Relay 回环口在听）或 `tunnel down`。

### 5.3 停止

```powershell
.\dialhome.exe node stop
```

杀掉本机 `sshd` 进程（依据 `state/sshd.pid`）；隧道 supervisor 在下一轮 tick 退出。**不会**移除自启或删除数据。

### 5.4 卸载

```powershell
.\dialhome.exe node uninstall
```

只移除**自启动入口**，**保留数据目录**（`config.json`、`keys/` 等仍在）。要彻底清除密钥，手动删除数据目录即可。

### 5.5 日志

```powershell
.\dialhome.exe node logs            # 默认 node 日志（末尾 40 行）
.\dialhome.exe node logs sshd       # 本机 sshd 输出 → logs/sshd.log
.\dialhome.exe node logs tunnel-ssh # ssh 通道子进程错误 → logs/tunnel-ssh.log
```

读取 `logs/<channel>.log` 的末尾 40 行。可用通道：`sshd`、`tunnel-ssh`、`tunnel-application`（启用时）。

### 5.6 只读诊断

```powershell
.\dialhome.exe doctor
```

只读打印平台、数据目录、config、identity、tunnel key、manager key、自启动状态。**不改任何状态**，适合排障时先跑一次。

---

## 6. 自启动（用户级，无需管理员）

| 平台 | 机制 | 备注 |
|---|---|---|
| Windows | **HKCU Run** 项 `dialhome-node`（指向 `dialhome.exe node run`） | 用户级、无管理员 |
| Linux | `systemd --user` 单元；若无 user bus（无桌面的 ssh 会话）→ 回退 **XDG autostart** `~/.config/autostart/dialhome-node.desktop` | 用户级、无管理员 |
| macOS | `~/Library/LaunchAgents/dialhome-node.plist` | **本轮回环未真机验证（无 macOS 设备）** |

手动管理：

```powershell
.\dialhome.exe autostart install    # 注册用户级自启
.\dialhome.exe autostart status     # 查看当前自启入口
.\dialhome.exe autostart uninstall  # 移除
```

> **已知限制（重要）**：用户级自启只在**登录会话之后**才活。重启机器但**未登录**不会自动起；要「开机即用」需显式安装系统服务（见 §7）。

---

## 7. 可选：开机即起（系统服务，需 root，永不默认）

```powershell
# 仅 Linux systemd；需 root
sudo dialhome service install --scope system
sudo dialhome service status --scope system
sudo dialhome service uninstall --scope system
```

- 这是**可选、需提权、永不默认**的。install 默认只做 §6 的用户级自启。
- 非 Linux（Windows/macOS）调用会明确报错拒绝，不会偷偷提权。
- 卸载只删 systemd 单元；Linux 下若退回 XDG autostart 文件，需手动删 `~/.config/autostart/dialhome-node.desktop`。

---

## 8. 重试与开机等待（supervisor ladder）

每通道独立重试（`internal/supervisor/ladder.go` + `runner.go`）：

- **开机等待**：重启后先每 30s 探一次 Relay，最多 **≤10 分钟**（约 20×30s）；超时后直接进重试梯。
- **重试梯**：`5 → 10 → 20 → 40 → 60s`（翻倍封顶 60）；**连续失败 ≥10 次**跳到 **3600s**（小时级）。
- **鉴权失败**（日志含 `Permission denied`，OpenSSH 每源 10 次惩罚）直接跳小时级，不快速重拨。
- **成功即清零**失败计数。
- 保活 `ServerAliveInterval=15 CountMax=3`；单连接超时 `10s`；`ExitOnForwardFailure=yes`。
- 一条通道失败**不影响**其它通道。

如需调整，改 `config.json` 的 `supervisor` 段（载入时校验，见 CONFIG_SCHEMA §1）：

```json
"supervisor": {
  "boot_wait_max_seconds": 600,
  "retry": { "base_seconds": 5, "max_seconds": 60, "fails_to_hourly": 10, "hourly_seconds": 3600 },
  "keepalive": { "interval_seconds": 15, "count_max": 3 },
  "connect_timeout_seconds": 10
}
```

---

## 9. 故障排查表

| 现象 | 可能原因 | 处理 |
|---|---|---|
| `another supervisor owns this node` | 已有 supervisor 在跑（实例锁） | 先 `node stop`，或确认没重复自启 |
| `CHANNEL ... tunnel down` | 隧道未建；看 `node logs tunnel-ssh` | 检查 Relay 可达、`tunnel.key` 存在、凭证未过期 |
| 日志里 `Permission denied` | 隧道公钥未登记 / 凭证失效 / 通道跳到 3600s 重试 | 重新发凭证 `node install`；确认 enroll 成功 |
| 开机不自动起 | 用户级自启需登录会话 | 登录后起；或用 §7 系统服务（需 root） |
| Windows `node install` 解压失败/exit 0 但无文件 | 误用 `Expand-Archive`（无控制台父进程 spawn 时报错且不解压） | 不用 PowerShell 解压；让 install 用自带 stdlib zip，或用 `--openssh-zip` 预置包 |
| `node status` 显示 LISTENING 但连不上 | 本机 sshd 未起 / authorized_keys 缺管理公钥 | `node logs sshd`；确认 `manager_pubkey` 已写入本机 AK |
| 改了 config 后 install 报校验错 | 通道地址非 `127.0.0.1:` 开头 / 缺 `ssh` 通道 / mode 非法 | 见 §10 不变式 |

---

## 10. 安全须知

- **私钥不出机**：tunnel key 本机生成、只落 `keys/tunnel.key`；manager key 只在 Controller（见 CONTROLLER_MANUAL）；凭证私钥只在 enroll 时用一次即删。
- **本机 sshd 受控**：仅 `127.0.0.1:<高位>`、`PasswordAuthentication no`、`AllowUsers <当前用户>`，HostKey/AK/log/pid 全在数据目录，**不改系统 SSH 配置**。
- **回环不变式**：通道地址必须字面量 `127.0.0.1:<端口>`，绝不 `localhost`/`0.0.0.0`（B1 坑位）。
- **日志脱敏**：logging 包对私钥体/`sk-*`/`Bearer`/公钥体统一替换为 `[REDACTED]`，密钥不会进日志。
- **卸载保留密钥**：`node uninstall` 只去自启，不清数据；彻底清密钥需手动删数据目录。

---

## 11. 配置示例（真实 `config.json`）

`node install` 写入的默认配置（与 `internal/config/config.go` 一致；`remote` 端口由凭证覆盖）：

```json
{
  "config_version": 1,
  "node": {
    "name": "hp66",
    "relay": { "address": "10.101.54.192", "port": 18322, "user": "user" },
    "sshd": {
      "listen": "127.0.0.1:19022",
      "openssh_dir": "openssh",
      "host_key": "keys/host_ed25519",
      "authorized_keys": "keys/authorized_keys",
      "log": "logs/sshd.log",
      "pid_file": "state/sshd.pid"
    },
    "channels": [
      { "name": "ssh",         "local": "127.0.0.1:19022", "remote": "127.0.0.1:13441", "enabled": true },
      { "name": "application", "local": "127.0.0.1:3425",  "remote": "127.0.0.1:13440", "enabled": false }
    ],
    "mode": "persistent",
    "register": { "enabled": true, "interval_minutes": 30 }
  },
  "supervisor": {
    "boot_wait_max_seconds": 600,
    "retry": { "base_seconds": 5, "max_seconds": 60, "fails_to_hourly": 10, "hourly_seconds": 3600 },
    "keepalive": { "interval_seconds": 15, "count_max": 3 },
    "connect_timeout_seconds": 10
  }
}
```

载入强制的不变式：通道 `local/remote` 必须以 `127.0.0.1:` 开头；至少有一个名为 `ssh` 的通道；`mode ∈ {persistent, temporary}`；重试参数自洽。

> 2026-11-15 审评修复：凭证里的 `relay_user` 由 relay 侧 `--relay-user` 配置（不再写死 `user`）；部署参数见 RELAY_MANUAL。
