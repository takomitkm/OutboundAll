# ADR-007 — 按角色分配传输组件：受控端 sing-box，其余一律上游原版

状态：Accepted（2026-11-20）

## Context

三个角色对组件的需求不同，此前文档把"整套栈"当成一个整体描述，实际上角色的约束完全不同：

1. **受控节点（校机）**：Windows × 无管理员 × 需要 SSH server。三者交集只有
   sing-box 内嵌 tailscale 协议栈（用户态 WG 无 TUN/无驱动 + 跨平台 `ssh_server`）。
   官方 tailscaled Windows 发行版是"MSI + 驱动 + 系统服务"路线，到不了终点。
2. **管理机（本机）**：持有 controller 身份，运行 hs-admin。**代码事实**：
   `hs/internal/admincli/ssh.go` 从第一天起就是按官方 tailscale CLI 设计的——
   `tailscale --socket S ssh`、`tailscale nc`、`ProxyCommand`，空 socket 直接走
   tailscale 默认端点（Windows = 官方客户端的命名管道）。管理机用 sing-box socks
   只是跨网测试期的临时测试架（.tmp/launch-ctrl.py），不是产品形态。
3. **服务端（139 VPS）**：headscale 是上游自托管控制面（无需身份，纯构件）；但
   数据面中继此前配的是 **Tailscale 公共 DERP**（deploy.sh derp.urls）——国内
   不可达 + 第三方依赖，跨网实测已证伪（见 a11d17f 的 8 条路径记录）。

跨网测试的终局结论（a11d17f："only domain+443 TLS DERP is viable"）有一个隐藏
前提没有验证：DERP 客户端只认"系统信任的 TLS 证书"。这个前提对 **headscale 内嵌
DERP** 成立（embedded DERP 的证书跟随 server_url，无法在 DERP map 里携带指纹），
但对**官方独立 derper** 不成立——见 Decision D4。

## Decision

### D1 受控节点 = sing-box（默认，唯一例外）

- hs-node 渲染 sing-box 配置、钉死 v1.14.4（sha256 pin）——维持 ADR-006 门控。
- "默认"的含义：约束（Windows/无管理员/SSH server）解除时优先换回官方 tailscaled
  （如 Linux 节点直接用官方客户端，自带 Tailscale SSH server，无需 sing-box）。

### D2 管理机 = 官方 tailscale 客户端（零代码改动）

- `tailscale up --login-server <headscale-url> --authkey <tagged key>` 登记，
  hs-admin 开箱即用（default socket 即官方客户端）。
- 收益：ADR-006 的"fork 落后安全通告"顾虑收缩到只剩受控节点一台；管理机获得
  官方 GUI/status/调试工具。

### D3 服务端中继 = 官方上游 derper（pinned v1.102.1，与 fork 基线同源）

- 弃用 Tailscale 公共 DERP（derp.urls → []），headscale 改文件式 DERP map
  （derp.paths）指向自托管 derper。
- pin 版本选 v1.102.1：与 sagernet fork 基线一致，一个版本号管两端，协议兼容性
  只需验证一个组合。
- derper 无官方发布二进制 → CI 构建（`go install tailscale.com/cmd/derper@v1.102.1`），
  随 server 包分发——构建仍在 GitHub Actions，符合"CI 和 build 都用 GH Actions"约束。

### D4 证书 = derper 自动自签 IP 证书 + DERP map 指纹 pin（推翻"必须域名"）

一手证据链（全部 v1.102.1 源码，2026-11-20 实查）：

1. `derp/derphttp/derphttp_client.go:669-675`：DERP map 节点 `CertName` 带
   `sha256-raw:` 前缀时走 `tlsdial.SetConfigExpectedCertHash`——**指纹 pin 取代
   WebPKI 验证**。
2. `net/tlsdial/tlsdial.go:312-316`（SetConfigExpectedCertHash 文档注释）：
   校验 = 恰好 1 张证书（MetaCert 不计）+ SHA256 hex 相等 + 未过期；**按 IP 直连
   （ServerName 为空）时主机名匹配整段跳过**（`dialedHost != ""` 才校验）。
3. `cmd/derper/cert.go:103-163`（NewManualCertManager）：`-certmode=manual` +
   `-hostname=<IP>` 时，证书文件不存在则**自动生成自签 IP 证书**（ECDSA P-256、
   SAN=IP、1 年期），并打印可直接粘贴的 DERPNode JSON
   （`CertName: "sha256-raw:<hex>"`）；`-hostname` 旗标文档原文："It can be an
   IP address when --certmode=manual (**to avoid SNI checks**)"。

因此 a11d17f 的"only domain+443 TLS"结论被推翻为：

> **需要的是一个允许客户端指纹 pin 的 TLS 中继**。headscale 内嵌 DERP 给不了
> （map 不可定制），官方 derper 原生支持。域名不需要，Let's Encrypt 不需要，
> 续期基建不需要（1 年一换，deploy.sh 重跑即刷新 map）。

### D5 准入与滥用面

- derper 不验证客户端（headscale 未实现 tailcfg.DERPAdmitClientRequest 准入端点，
  `-verify-clients` 需要本地 tailscaled 亦不适用）→ 139 上的 derper 是**公开中继**：
  任何能连上 443 的人都能借道转发 WireGuard 密文。缓解：
  - `-accept-connection-limit / -accept-connection-burst` 限流；
  - 中继只搬运端到端加密数据（看不了内容），风险上限 = 带宽滥用/元数据暴露；
  - 安全组是最后闸门（443 未开前数据面物理不可达）。
  已记入 THREAT_MODEL（T-17）。

## Consequences

- 组件矩阵：受控端 sing-box（fork，钉死）；管理机/服务端全部上游原版
  （tailscale CLI / headscale / derper，仅 headscale 与 derper 有版本 pin）。
- 控制面不变：plain http + SSH 隧道（校机 -L 18080→8080；管理机同法），headscale
  listen_addr 收紧为 127.0.0.1。
- 数据面首次可用条件：安全组放行 443/tcp（derper）；3478/udp 可选（STUN）。
- 1 年一次证书轮换：derper 删证书重生成或 deploy.sh 重跑，指纹自动刷新进 map。
- 实机自检可在安全组开放前完成：139 本机起 tailscaled userspace 自测节点，
  走 https://139.9.101.203:443 验证 pin 全链（loopback 到公网 IP 不经安全组）。

## Verification（2026-11-21 真机，SG 443 开放后）

1. **外网 TLS + pin**：本机裸 socket 到 139.9.101.203:443，TLSv1.3 握手成功，
   peer cert sha256 = `8d6220c3…0735`，与 DERP map 的 `CertName: sha256-raw:` 逐字一致。
2. **受控节点上中继**：校机 sing-box 到 139:443 的连接连续采样均为 `Established`。
3. **tailnet 数据面**：本机 sing-box SOCKS5 → `100.64.0.2:22` CONNECT 成功，
   收到 `SSH-2.0-sing-box` banner（sing-box ssh_server）。
4. **官方客户端全链**：139 上官方 tailscale 1.104.7（vps-admin，100.64.0.7）
   `netcheck` 显示 `Nearest DERP: OutboundAll-vps (3ms)`；
   `tailscale ssh YiZhidi@win11-hv1` 在 Windows 校机上执行成功。
5. **控制面单端口化**：headscale 直接监听 18080 后，管理机注册一次成功；
   旧 nc 单连接桥导致"注册回退 https 卡死"（OPERATIONS §5 教训）。
7. **直连（打洞）实测：不可行，中继是设计内的常态**（2026-11-22，SG 已开 3478/udp）：
   - 本机 netcheck（STUN 生效后）：`UDP: true`，公网映射 `183.36.189.192:46863`；
     校机 UDP 出站实测可收发（STUN 到公网服务器有回包）——**两边 UDP 都不是硬阻**。
   - 但 `tailscale status --json` 里 peer 的 `Endpoints` 恒为空、`CurAddr` 为空、
     `Relay=OutboundAll`：**sing-box 的 tailscale endpoint 从不向控制面上报 UDP 端点**，
     对端没有候选地址可打 → 打洞无从发生，`tailscale ping` 恒为
     `via DERP(OutboundAll)` + `direct connection not established`。
   - 结论：**在"受控端跑 sing-box"这一前提下，数据面注定走中继**（这是放弃官方
     tailscaled 换取免管理员的代价）。想要直连只有两条路：(a) 受控端改跑官方
     tailscaled（要管理员，违背本架构出发点）；(b) 换一个会上报端点的客户端实现。
     中继模式下 WireGuard 端到端加密不受影响，只是带宽/时延受 VPS 限制。
   - STUN 仍保留（3478/udp）：对将来会上报端点的客户端有用，且无副作用。

6. **执行环境注记**：校机 ssh_server 远端 shell 是 PowerShell 7.7，
   `hostname`/`whoami` 裸名解析不到（PATH 有 system32 但解析失败），
   `hs-admin exec` 请用 cmdlet 或全路径（如 `$env:COMPUTERNAME`、
   `[System.Net.Dns]::GetHostName()`）。
