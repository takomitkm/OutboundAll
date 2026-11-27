# sshtunnel-selfheal

**自愈式反向 SSH 隧道守护**：把一台 NAT 后的机器（*host*）的 sshd 反向锚到一台
可达的 relay 上，relay 上开一个回环端口回拨进去。隧道断了自动重连，不需要人看着。

从校机桌面（一台 NAT 后的机器）提取、脱敏后收录。它和 pwsh 分支自带的
`node/TunnelManager.ps1` + `relay/magpie-tunnel-check.sh` **是两套不同的架构**：

| | 本工具（`tools/sshtunnel-selfheal.sh`） | pwsh 分支的隧道管理 |
|---|---|---|
| 形态 | 单文件 bash 守护，`while :; do ssh -R; sleep 5` | `TunnelManager.ps1` 生命周期 + `magpie-tunnel-check.sh` 探测 |
| 自愈判据 | **进程级**：ssh 进程退出（rc≠0）→ 5s 后重连 | **服务级**：经隧道真访问到服务（forced-command 探针）才算在线 |
| 适用 | 只关心"隧道在不在"的轻量场景 | 需要"隧道里的服务真的活着"的严格场景 |

> 本工具**只做隧道存活**，不做健康探测、不做服务拉起。要"隧道里跑的 magpie
> 网关挂了也自动拉起"，用 pwsh 分支那套（`magpie-tunnel-check.sh` 探测）。

---

## 从零重建（推荐路径）

两步：先在 relay 上布置，再在 host 上启动。

### 第 1 步：relay 上跑部署脚本（一次性，幂等可重跑）

```bash
bash deploy-sshtunnel-selfheal.sh \
       --relay-host RELAY_IP_OR_NAME \
       [--relay-port 22] \
       [--tunnel-user sshtunnel] \
       [--tunnel-port 13390] \
       [--key-dir /root/sshtunnel-selfheal/keys]
```

它自动做完下面"前置条件"的 1–3 条：检查 sshd 与 `AllowTcpForwarding`、建专用
账号（nologin，只能持有授权行，不能登录）、生成 host 密钥对（已有则复用）、
把**受限授权行**写进账号的 `authorized_keys`（幂等，重复跑不重复写）。
跑完打印 host 侧要做的全部命令，照着抄即可。

### 第 2 步：host 上放脚本 + 配置 + start

按部署脚本打印的指令做，本质就是三件事：

1. 把 relay 上生成的私钥拷到 host；
2. 把 `tools/sshtunnel-selfheal.sh` 放到 host，改顶部配置块 5 个变量；
3. `./sshtunnel-selfheal.sh start`，然后按"验证"节核对。

---

## 前置条件（手动布置时的清单）

本工具**不是部署器**。上面的部署脚本会自动布置 1–3 条；若手动做，缺哪样
`start` 就起不来：

1. relay 上跑着 OpenSSH **sshd**，且允许这台 host 通过 `-R` 开反向端口
   （默认配置即可；`AllowTcpForwarding` 不能是 `no`）。
2. relay 上有一个**专用账号**（例子里 `RELAY_USER`，如 `tunnel`），
   它的 `~/.ssh/authorized_keys` 里有 host 那把**公钥**（对应 host 的 `KEY` 私钥）。
3. host 上有一把**私钥**（例子里 `RELAY_KEY`），host 用它出站认证到 relay
   （`BatchMode=yes`，所以私钥必须无口令或已有 agent）。
4. host 的 sshd 监听在 `127.0.0.1:22`（或你想暴露的本地端口）。
5. relay 能连到 host（网络通、host 出站 22 不被墙）。

---

## 配置

文件顶部一个配置块，改 5 个变量即可：

```bash
KEY=/path/to/RELAY_KEY        # host 出站连 relay 用的私钥
TARGET=RELAY_USER@RELAY_HOST  # relay 上的账号@主机（host 连这个）
PORT=13390                    # relay 上开的回环端口
PIDFILE=$HOME/.cache/sshtunnel-selfheal.pid
LOGFILE=$HOME/.cache/sshtunnel-selfheal.log
```

- `KEY`：**host 侧**的私钥路径（不是 relay 的）。
- `TARGET`：host 出站连到谁。账号 `RELAY_USER` 是 relay 上为隧道建的专用账号。
- `PORT`：relay 上 `127.0.0.1:PORT`，回拨入口。

## 用法

```bash
./sshtunnel-selfheal            # 前台跑（跟着你的 ssh 会话，Ctrl+C 停）
./sshtunnel-selfheal start      # 脱离终端跑，断线自动重连
./sshtunnel-selfheal status     # 守护进程活着吗？
./sshtunnel-selfheal stop       # 停掉守护
```

`start` 的行为：

- 已跑着 → 幂等退出（不重复起）。
- 第一次起 → `setsid nohup` 脱离控制终端（ssh 登出不 SIGHUP），
  pid 写 `PIDFILE`，日志追加 `LOGFILE`（超 200KB 自动截断）。
- 守护内 `while :; do ssh -R …; wait; sleep 5; done`：ssh 一退出（网络断、relay 重启、
  服务器踢掉），5 秒后重连。**没有退避上限**——这是有意的，保证离线期一恢复就回。
- `stop`：kill 守护 pid + `pkill -f 'sshtunnel-selfheal daemon'`（清掉可能的孤儿）。

## 验证

在 relay 上：

```bash
ss -ltn | grep :$PORT          # 期待恰好一条 127.0.0.1:$PORT 监听
ssh -p $PORT $USER@127.0.0.1 'hostname'   # 经隧道回拨到 host
```

在 host 上：

```bash
./sshtunnel-selfheal status     # RUNNING (pid …)
tail -5 $LOGFILE                # 应看到 "daemon up" 与重连记录
```

## Windows host 怎么办

本分支的脚本是 **bash**，只适用 **Linux host**（Windows 10/11 不自带 bash）。
Windows host 的"自带最优"是 PowerShell 5.1 + 内置 OpenSSH 客户端 + 用户级
自启（计划任务 / HKCU Run / 启动文件夹，全程零管理员）——这套完整实现
**不必重写，直接用 pwsh 分支的 node/ 体系**：

- `node/connector.ps1` — 连接器主循环（心跳监督、阶梯退避 5→60s、日志脱敏+轮转、
  命名互斥体防多开）
- `node/TunnelManager.ps1` — ssh 进程生命周期（`Start-Tunnel`/`Stop-Tunnel`/
  `Test-TunnelOnline` 服务级探测）
- `node/sshserver.ps1` — 便携 rootless sshd（Win32-OpenSSH zip 解压即用，零管理员）

两套架构怎么选：只关心"隧道在不在"用本分支（轻量、进程级自愈）；要求
"隧道里的服务真的活着"（服务级探测、Windows 零管理员全套装配）用 pwsh 分支。

## 与原仓库的关系

新分支 `sshtunnel-selfheal` 从 pwsh 分支 tip 切出，只新增 `tools/` 下两个文件
（`sshtunnel-selfheal.sh` 自愈守护 + `deploy-sshtunnel-selfheal.sh` relay 侧
从零部署），其余内容与 pwsh 分支一致。pwsh 分支本身是存档分支
（"仅作历史参考，新部署请勿使用"，见 main 分支 README）。
