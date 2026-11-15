# Controller 运维手册（总控侧）

> 适用对象：持有管理私钥、负责把分散在 NAT 后各节点的 shell/命令集中拨入的运维人员。
> 本手册只讲 **Controller** 侧（`dialhome controller ...`）。受控机侧见 [NODE_MANUAL.md](./NODE_MANUAL.md)；
> 安全与身份模型见 [SECURITY_MODEL.md](./SECURITY_MODEL.md)；架构见 [ARCHITECTURE.md](./ARCHITECTURE.md)；
> 中继侧见 [RELAY_MANUAL.md](./RELAY_MANUAL.md)。所有命令/字段取自 `internal/cli/controller.go`、
> `internal/controller/registry.go`，以代码为准。

---

## 1. 这是什么

Controller 是**唯一持有管理私钥**的角色：它不建立隧道，而是经 Relay 的回环口，用 **Manager Key** 登录到各个 Node 的本地 sshd，提供 `shell`/`exec`。

三身份之 B —— **Controller Management Key**：

- 私钥只在 Controller：`~/.config/dialhome/keys/manager.key`（Windows：`%APPDATA%\dialhome\keys\manager.key`）；
- 公钥写进**每个 Node** 的 `authorized_keys`（由 Node 侧 `node install` 从凭证里的 `manager_pubkey` 写入），可取该 Node 当前用户的 shell；
- 私钥**绝不**进安装包、绝不进 Node、绝不进任何 HTTP 响应体；泄露即最高级事件（见 SECURITY_MODEL §4）。

> 回环不变式：Controller 经 dialhome 锚点到达的是 Relay 上的 `127.0.0.1:<节点端口>`，**不是**公网口。详见 §5。

---

## 2. 前置条件

- 一份 `dialhome` 二进制（与 Node 同一份；Controller 用 console 子命令交互）。
- 已 `controller init` 生成管理密钥对，且把 `manager.pub` **scp 到 Relay** 的 `~/.dialhome/manager.pub`（否则凭证里不会带管理公钥，Node 侧 authorized_keys 也就没有 Controller 的登录能力）。
- 数据目录（用户级）：`~/.config/dialhome/`（Windows：`%APPDATA%\dialhome\`），含 `machines.json`、`keys/`、`known_hosts`。
- 要拨入的节点已通过 [NODE_MANUAL.md](./NODE_MANUAL.md) 完成 `node install` 并处于连线状态。

---

## 3. 初始化管理密钥

```powershell
# Windows PowerShell
.\dialhome.exe controller init

# Linux / macOS bash
./dialhome controller init
```

行为（`internal/cli/controller.go`）：

- 若 `keys/manager.key` 已存在，打印已有路径，不覆盖；
- 否则生成 ed25519 管理密钥对：私钥 `keys/manager.key`（`0600`），公钥 `keys/manager.pub`（`0644`）；
- 输出提示：**把 `manager.pub` 复制到 Relay 的 `~/.dialhome/manager.pub`**（用 `scp`）。

```
MANAGER-KEY  = <data>/keys/manager.key
MANAGER-PUB  = <data>/keys/manager.pub
copy manager.pub to the relay as ~/.dialhome/manager.pub (scp)
```

> 只有管理公钥落到 Relay 并被 Relay 在签发凭证时烤进 `manager_pubkey`，Node 才会信任 Controller 的登录。顺序：**先 `controller init` → scp `manager.pub` → 再发凭证给 Node 安装**。

---

## 4. 命令一览

| 命令 | 作用 |
|---|---|
| `dialhome controller init` | 生成管理密钥对（私钥留本机，公钥需 scp 到 Relay） |
| `dialhome controller list` | 列出 `machines.json` 中已注册节点（名称/shell/锚点数/备注） |
| `dialhome controller status` | 逐个节点做 failover 探测，打印 `UP`/`DOWN` 及最后命中的锚点 |
| `dialhome controller anchors <node>` | 打印该节点**有序探测 trail**（每条锚点的真实成败） |
| `dialhome controller shell <node>` | 交互式 shell（`-t` 分配 PTY），经首个可达锚点登录 |
| `dialhome controller exec <node> <command...>` | 在节点上执行命令并回显输出 |

`list`/`status`/`anchors` 只读，安全可常跑。`shell`/`exec` 会真正在远端执行。

---

## 5. 锚点（anchors）与多路径 failover

Controller 通过 `machines.json` 里每个节点的 **anchors 数组**按顺序尝试，第一个可达者胜出。两类锚点（`internal/controller/registry.go` 的 `Anchor` 结构，JSON tag 如下）：

### 5.1 ssh-alias 锚点（沿用你自己的 `~/.ssh/config`）

```json
{ "alias": "school-guest" }
```

- 直接引用你 `~/.ssh/config` 里已存在的 `Host school-guest`。**Controller 从不迁移、从不复制你的 SSH 配置**，只引用。
- 适用于你本来就有的跳板/直连别名。

### 5.2 dialhome 锚点（经 Relay 的 ProxyJump）

```json
{
  "node": "school",
  "via_relay": "10.101.54.192:18322",
  "relay_port": 18322,
  "relay_user": "user",
  "remote_port": 13441
}
```

| 字段 | 含义 |
|---|---|
| `node` | dialhome 节点名（对应 Node 侧 install 的 `name`） |
| `via_relay` | Relay 的 `host:port`，用于构造 ProxyJump 跳板（**端口在此处**） |
| `relay_port` | Relay ssh 端口的镜像字段（参考用，跳板以 `via_relay` 为准） |
| `relay_user` | 登录 Relay 跳板的用户名（默认 `user`） |
| `remote_port` | 节点在 Relay 回环上的 sshd 端口（`-p` 目标） |
| `local_port` | 可选：预建的本地 `-L` 回环口，存在时先 TCP 探它 |
| `note` | 备注 |

**连接方式（`controller.go` 的 `anchorArgs`）：**

1. 构造 ProxyJump：`-J <relay_user>@<via_relay 的 host:port>`（用 `via_relay` 的 host:port）；
2. 最终目标：`-i <manager.key> -p <remote_port> <relay_user>@127.0.0.1`，并带 `StrictHostKeyChecking=accept-new` + 控制器私有 `known_hosts`（见 §7）；
3. `-i` 作用于**最终主机**（Node），跳板 hop 用默认身份/agent，故保持默认 `IdentitiesOnly` 关闭。

**Controller 与 Relay 同机时自动跳过跳板**：若 `via_relay` 以 `127.0.0.1` 开头，则 `jump=""`，直接 `ssh -i manager.key -p <remote_port> <relay_user>@127.0.0.1` 回连本机回环（证据：2026-11-15 E2E——向 `127.0.0.1:22` 跳板会失败，因为 relay 用户在自己 authorized_keys 里没有自指 key）。当前部署（relay 102 与 Controller 同机）即走这条直连路径。

> 注意：目标用户是 `relay_user`，落在 Relay 回环 `127.0.0.1:<remote_port>`，而该端口后面是 Node 的 sshd（`AllowUsers <当前用户>`）。因此 `relay_user` 必须与 Node 的当前用户名一致（默认 `user`）才能登入。

---

## 6. 注册表 `machines.json`

路径：`~/.config/dialhome/machines.json`（Windows：`%APPDATA%\dialhome\machines.json`）。结构（与 `Registry` 结构一致）：

```json
{
  "config_version": 1,
  "machines": {
    "school": {
      "display": "校机 Win11-HV1",
      "shell": "cmd",
      "note": "无 UAC；D 盘 1.8TB",
      "anchors": [
        { "alias": "school-guest" },
        { "node": "school", "via_relay": "10.101.54.192:18322", "relay_port": 18322, "relay_user": "user", "remote_port": 13441 }
      ]
    },
    "e2e-node": {
      "display": "Debian 13 @ relay102",
      "shell": "bash",
      "anchors": [
        { "node": "e2e-node", "via_relay": "127.0.0.1:18322", "relay_user": "user", "remote_port": 13441 }
      ]
    }
  },
  "defaults": { "connect_timeout_seconds": 10 }
}
```

| 字段 | 含义 |
|---|---|
| `machines.<name>.display` | 展示名（list 显示） |
| `machines.<name>.shell` | shell 提示：`cmd` / `bash` / `powershell`（影响 `exec` 走脚本模式，见 §8） |
| `machines.<name>.note` | 备注 |
| `machines.<name>.anchors` | 锚点数组，按数组顺序 failover |
| `defaults.connect_timeout_seconds` | 探测超时（默认 10） |

`machines.json` 不存在时 `Load` 返回空注册表（不报错）；可直接新建/编辑该文件增删节点。

---

## 7. known_hosts 与主机密钥

- Controller 用**自己私有**的 `known_hosts`（`~/.config/dialhome/known_hosts`），并在连接时 `UserKnownHostsFile` 指向它，**不污染你共享的 `~/.ssh/known_hosts`**。
- Node 侧 `node install` 每次重建 host key，所以节点回环隧道的主机密钥会变化；用 `StrictHostKeyChecking=accept-new` 接受新 key 到 Controller 私有 known_hosts（证据：2026-11-15 E2E 节点每次安装重生成 host key）。这是有意的、安全的——管理密钥认证发生在私有回环通道上。

---

## 8. shell 与 exec 的真实行为

### 8.1 `controller shell <node>`

```powershell
.\dialhome.exe controller shell school
```

- 先 failover 选出可用锚点，失败则打印完整 trail 并退出；
- 加 `-t` 分配 PTY，把本地 stdio 直接接上远端 ssh（交互式）。

### 8.2 `controller exec <node> <command...>`

```powershell
# Windows / Linux 同形
.\dialhome.exe controller exec e2e-node 'hostname && whoami'
```

- 命令串作为 ssh 的**最后一个参数**传入（`exec.Command` 参数数组，绝不经本地 shell 二次解析）；
- 若节点 `shell == "bash"`：走 **stdin 脚本模式**——`ssh ... bash -s`，把命令经 stdin 喂给远端 bash，避免「命令→ssh→bash」三层引号嵌套（旧实现基线 §4.3 的坑）；
- 非 bash shell 则直接作为远端命令参数执行。

**已验证 E2E（relay 102，Debian 13）：**

```
$ dialhome controller exec e2e-node 'hostname && whoami'
debian-h61
user
```

---

## 9. 失败诊断：探测 trail

当某节点所有锚点都不可达，`anchors`/`status`/`shell`/`exec` 会打印**有序探测 trail**，每条锚点带真实失败原因：

```
machine school anchor trail:
  1. [ssh-alias] school-guest -> fail (Connection timed out)
  2. [dialhome] school via 10.101.54.192:18322 -> fail (Permission denied (publickey))
machine school: all anchors failed (see trail above)
```

| 现象 | 含义 / 处理 |
|---|---|
| `[ssh-alias] ... -> fail (Connection timed out)` | 你的 `~/.ssh/config` 别名连不上；检查该别名本身 |
| `[dialhome] ... -> fail (Permission denied (publickey))` | 管理公钥未落到 Relay / 未烤进 Node AK；重做 §3 scp `manager.pub` 并重新 `node install` |
| `[dialhome] ... -> fail (Connection refused)` | Relay 回环口没在听；Node 隧道未起，去看 Node 侧 `node status`/`node logs tunnel-ssh` |
| trail 全绿但 `exec` 无输出 | 检查 `shell` 字段；bash 走 stdin 脚本模式 |

---

## 10. 故障排查表

| 现象 | 原因 | 处理 |
|---|---|---|
| `machine "x" not registered` | `machines.json` 无该节点 | 编辑 `machines.json` 加入节点与锚点 |
| 所有锚点 `Permission denied (publickey)` | Relay 上 `manager.pub` 缺失 / Node AK 无管理公钥 | 确认 §3 scp 到位；重发凭证、重装 Node |
| `all anchors failed` 但 Node 侧 `status` LISTENING | 锚点 `remote_port`/`relay_user` 写错 | 核对 `remote_port` 是 Node 在 Relay 的回环口、`relay_user` 与 Node 当前用户一致 |
| 同机部署仍尝试跳板失败 | `via_relay` 未用 `127.0.0.1` 开头 | 同机场景把 `via_relay` 设为 `127.0.0.1:<port>`，会跳过跳板直连回环 |
| `exec` 命令引号乱套 | 直接嵌套引号 | 用 `exec` 的 bash stdin 脚本模式（§8.2），把它当成整段脚本 |
| macOS 侧行为不确定 | 本轮回环未真机验证 | 以真机实测为准，不要假设与 Linux 完全一致 |

---

## 11. 安全须知

- **管理私钥只在 Controller**：`keys/manager.key` 永不出本机、永不进安装包/HTTP 响应；泄露即最高级事件，需轮换（见 SECURITY_MODEL §4）。
- **公钥落点正确**：`manager.pub` 必须 scp 到 Relay 的 `~/.dialhome/manager.pub`，并由 Relay 在凭证中烤进 Node；缺这一步 Controller 无法登录任何节点。
- **私有 known_hosts**：控制器用自有的 `known_hosts`，不污染共享 `~/.ssh/known_hosts`；节点每次重装会换主机密钥，属预期。
- **命令经参数数组传入**：`exec`/`shell` 的命令是 ssh 的最后一个参数，`exec.Command` 参数数组，无本地 shell 二次解析，避免注入。
- **回环收敛**：dialhome 锚点最终落点是 Relay 回环 `127.0.0.1:<remote_port>`，不是公网口；隧道能力受 Relay `permitlisten` 精确约束（详见 SECURITY_MODEL §1）。

---

## 12. 最小可用流程（与 Node 侧对照）

1. **Controller**：`dialhome controller init` → `scp keys/manager.pub relay:~/.dialhome/manager.pub`
2. **Relay**：签发凭证（写入 `manager_pubkey`）并交给运维
3. **Node**：`dialhome node install --mode persistent --credential cred.json`（见 NODE_MANUAL §3）
4. **Controller**：编辑 `machines.json` 加入该节点与其锚点（见 §6）
5. **验证**：`dialhome controller list` → `dialhome controller status` → `dialhome controller exec <node> 'hostname && whoami'`

> 2026-11-15 审评修复：撤销节点现在同时撤销未消费的登记凭证，Controller 侧无需改动；详见 SECURITY_MODEL N11。
