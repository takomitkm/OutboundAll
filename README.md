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

## 前置条件（这是"还没一键部署"的如实交代）

本工具**不是部署器**，它假设以下已经在 relay 上就位。缺哪样，`start` 就起不来：

1. relay 上跑着 OpenSSH **sshd**，且允许这台 host 通过 `-R` 开反向端口
   （默认配置即可；`AllowTcpForwarding` 不能是 `no`）。
2. relay 上有一个**专用账号**（例子里 `RELAY_USER`，如 `tunnel`），
   它的 `~/.ssh/authorized_keys` 里有 host 那把**公钥**（对应 host 的 `KEY` 私钥）。
3. host 上有一把**私钥**（例子里 `RELAY_KEY`），host 用它出站认证到 relay
   （`BatchMode=yes`，所以私钥必须无口令或已有 agent）。
4. host 的 sshd 监听在 `127.0.0.1:22`（或你想暴露的本地端口）。
5. relay 能连到 host（网络通、host 出站 22 不被墙）。

> 要真正"一键"：把 1–4 的检查与安装包进一个部署脚本是下一步的事，
> 本工具先只交付守护本身。

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

## 与原仓库的关系

新分支 `sshtunnel-selfheal` 从 pwsh 分支 tip 切出，**只新增** `tools/sshtunnel-selfheal.sh`
这一个文件，其余内容与 pwsh 分支一致。pwsh 分支本身是存档分支
（"仅作历史参考，新部署请勿使用"，见 main 分支 README）。
