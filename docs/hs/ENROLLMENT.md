# ENROLLMENT — OutboundAll-hs 登记指南（密码less，一次性）

> 流程语义继承参考实现（凭证一次性、短期、崩溃安全）；载体换为邀请码 + preauth key。

## 1. 三身份（ADR-002）

| 身份 | 生成/存放 | 用途 |
|---|---|---|
| 邀请码 | hs-admin 产出（256-bit 随机，服务端只存 sha256） | 人类携带，换登记包 |
| 节点身份 | 节点本机生成（tailscale node/machine key），存 `state_directory`，私钥不出机 | tailnet 连接 |
| node secret | redeem 时签发，节点 0600 落盘 | 每次启动 resume 换新 preauth key |

分离不变式：邀请码 ≠ preauth key ≠ node secret；三者能力集互不包含。

## 2. 登记流程

```text
运维:  hs-admin invite create --label win-lab [--profile default|no-ssh] [--ttl-hours H]
        → 一次性邀请码（仅显示一次；服务端只存哈希）
节点:  hs-node enroll --code <CODE> --controller https://mc.example.net [--hostname NAME]
        1. HTTPS 兑换（原子、限速 6/min/IP）
        2. 本地生成节点身份 → state 目录
        3. 拿到单用 preauth key（1h 有效）→ sing-box 注册
        4. node secret 0600 落盘（config 目录）
运行:  hs-node run
        1. 用 node secret 经 /nodes/resume 换新 preauth key（每次启动）
        2. sing-box 以新 key 注册 → 控制面记录 pending→active
```

## 3. 管理机登记（官方 tailscale 客户端，ADR-007 D2）

管理机不装 sing-box、不跑 hs-node——controller tailnet 身份直接由官方客户端持有：

```bash
# 1. 运维发一枚 tag 为 OutboundAll-controller 的 key：
hs-admin invite create --label local-admin   # 或在 VPS 上用 headscale preauthkeys create --tags tag:OutboundAll-controller
# 2. 管理机装官方 tailscale 客户端，登记到自托管控制面（控制面经 SSH 隧道可达）：
tailscale up --login-server http://127.0.0.1:18080 --authkey <KEY> --accept-dns=false
# 3. hs-admin 开箱即用（default socket 即官方客户端；Windows=命名管道）：
hs-admin doctor --api http://127.0.0.1:18800
hs-admin shell <node>
```

数据面（管理机 → 节点）与受控端无关，走 tailnet + 自托管 derper 中继（ADR-007 D3/D4）。

### Windows 管理机实装记录（2026-11-22 真机）

1. **官方客户端用 MSI 装成服务**（`tailscale-setup-1.104.7-amd64.msi`，需管理员点一次 UAC）。
   服务模式走默认命名管道 `\\.\pipe\ProtectedPrefix\Administrators\Tailscale\tailscaled`，
   `tailscale ssh` / `hs-admin` 才能开箱即用。
2. **用户态（免管理员）能跑但不是可用形态**：dev 模式下后端随最后一个客户端断开而停，
   `tailscale ssh` 在自定义命名管道上直接报 `The specified path is invalid`（1.104.7
   Windows 平台缺陷，`ssh` 子命令拼 ProxyCommand 时把管道路径转义坏）；AF_UNIX 文件套接字
   Windows 不支持（`namedpipe.Listen` 只认管道）。**结论：Windows 管理机就用 MSI 服务模式。**
3. **hs-admin 必须放在本地固定盘**（如 `%LOCALAPPDATA%\OutboundAll-admin\`）。实测从 `S:`
   盘执行同一份二进制时，其派生的 `tailscale` 子进程打服务管道报 `Access is denied`
   （受限令牌），同一命令从 `C:` 执行 rc=0。
4. **控制面隧道做成登录任务**（`OutboundAllControlTunnel`）：`ssh -N -L 18080:...:18080
   -L 18800:...:8800 root@139`，断线 1 分钟自愈。Tailscale 服务本身是 Automatic。

## 4. 崩溃安全

redeem 与注册间崩溃：邀请进入 `redeemed(未完成)` 态，同 enroll 会话可续传，超时作废
——不留歧义重复节点（N5'：并发兑换恰一路成功，CI 实证）。

## 5. 撤销

```bash
hs-admin node revoke <id>     # headscale expire + ACL 移除 + 墓碑 + 名下邀请作废
hs-admin invite revoke <id>   # 只作废未消费邀请
```
撤销后：新连接拒绝（N6'）、resume 403（N7'）、已建会话按 G3 观察如实记录
（expire 不即时杀会话——与参考实现同口径）。

## 6. 服务暴露（Phase 6）

节点 config.json 的 `services: [{name, target "127.0.0.1:PORT"}]` 声明回环服务；
headscale grant（tcp:P）+ 节点 route allowlist 双闸；未列端口 reject-all。
SSH 转发保持关闭（`disable_forwarding: true` 结构性）。
