# ADR-008 — 数据面降级阶梯（校机出站被逐级收紧时还能怎么走）

状态：Accepted（2026-11-22；A 级实测不可行，B 级为当前默认，C/D 为可选降级，未实装）

## Context

校机在校园网，受控端只允许出站。跨网实测（2026-11-21/22）得到三条硬事实：

1. **直连（UDP 打洞）在"受控端跑 sing-box"前提下不可行**：`tailscale status --json` 里
   peer 的 `Endpoints` 恒为空、`CurAddr` 为空，`tailscale ping` 恒 `via DERP(OutboundAll)` +
   `direct connection not established`。sing-box 的 tailscale endpoint 不上报 UDP 端点，
   对端没有候选地址可打；本机侧 STUN 再多也只是自己学到了映射（ADR-007 Verification #7）。
2. **两端都在 NAT 后**，任何"隧道"都必须落到一台有公网入口的机器（本架构里就是 139）。
   所以除直连外的所有层级，流量都过 139，只是封装不同。
3. **便携 OpenSSH 免管理员**：`internal/openssh/windows.go`（v1 实现，commit 941e157）
   下载 `OpenSSH-Win64.zip` 解到用户目录直接跑 `sshd.exe`，不装服务、不提权。
   因此"节点上装 SSH 服务端"不是禁止项，D 级（纯 SSH）在免管理员前提下成立。

## Decision — 五级阶梯

| 级 | 前提（校机出站） | 数据面 | 过 139 | 保留 tailnet 语义 | 实装状态 |
|---|---|---|---|---|---|
| **A 直连** | 双方 UDP + 对端上报端点 | WireGuard 点对点 | 仅控制面心跳 | 是 | **不可行**（sing-box 不上报端点；除非受控端改官方 tailscaled，需管理员） |
| **B DERP 中继** | SSH + TCP 443 | DERP/TLS → 139 | 全部 | 是 | **当前默认**（137 实机验证） |
| **C DERP 塞 SSH** | 仅 SSH | SSH 隧道里跑 DERP（map 指向 `127.0.0.1:18443`，靠 `CertName` 指纹 pin 免主机名校验） | 全部 | 是 | 未实装；纯配置（map 三行 + 两端 `-L`） |
| **D 纯 SSH 转发** | 仅 SSH | 便携 `sshd.exe`（用户态）+ 隧道，或直转本地端口 | 全部 | 否（丢掉 tailnet/策略/ssh_server） | 未实装；免管理员可行（v1 已证） |
| **E 全封** | 无 | — | — | — | 无远程能力 |

控制面在所有级别里都走 SSH 隧道（`-L 18080/18800`），不需要 443、不需要入站。

### 选型口径

- **能直连就直连**（省 139 带宽），但当前架构下不可达，不投入。
- **默认 B**：唯一被真机全链验证过的形态，功能完整（ssh_server、策略、服务转发）。
- **C 的定位是"保功能"**：443 被封时用，多两层封装（TCP 里跑 TLS 里跑 WireGuard），
  损失性能换 tailnet 语义完整。**不是性能方案**，纯 SSH 能做的事它更重。
- **D 的定位是"保连通"**：最轻一层，但节点侧需要明文监听端口与自己的密钥/授权，
  等于把 v1 的 SSH 反向隧道形态接回来；丢 tailnet 语义。
- C 与 D **都不改变"流量过 139"**——这是 NAT 拓扑决定的，不是设计选择。

## Consequences

- 现状：**数据面只有 B，没有 SSH 兜底**；443 一旦被封，只剩控制面存活（可用于改配置、
  发邀请，但 shell/传文件全断）。要给这个场景兜底，C 是零架构改动的选项，D 需要新代码。
- 若将来做 C：改 `deploy.sh` 生成两份 DERP map 变体（直连/回环）并按环境变量选择，
  两端隧道命令写进 OPERATIONS 应急手册。
- 若将来做 D：把 v1 的 `internal/openssh`（pin 的 OpenSSH-Win64、sha256 校验、解压）
  移植进 `hs/`，由 hs-node 监管 `sshd.exe`，密钥走一次性凭据分发。