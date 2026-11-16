# ADR-001 — 网络架构：Headscale 控制面 + 内嵌 DERP + Caddy TLS + userspace 节点

状态：Accepted（G2 证据待 CI 回填）

## Context

源规格要求：自托管 Tailscale 控制面（Headscale）、DERP 回退中继、Windows 节点 userspace 运行、
单 VPS 起步、复现部署。参考实现（现行 Go 版）用 OpenSSH 反向隧道，其"回环限定"不变式在
新栈中由"无公网暴露面 + tailnet ACL"取代。

## Decision

1. 控制面 = **Headscale v0.29.7**，经 Caddy TLS 反代在 443 对外；`/derp` 路由承载内嵌 DERP（DERP over HTTPS）；STUN 用 3478/UDP。
2. Windows 节点 = sing-box tailscale endpoint，`system_interface: false`（userspace，无 TUN/驱动）。
3. 节点只有出站连接：控制面 HTTPS、DERP（443/TCP）、STUN（3478/UDP）、WireGuard 直连（UDP 打洞）。
4. 控制面 URL 稳定；控制面 HA 是独立课题（本 ADR 只声明：多 DERP ≠ 控制面 HA）。
5. CI/测试环境允许 `http://` 控制面 URL（G2 实测可行性；生产恒 TLS）。

## Consequences

- 对外端口面收敛为 443/TCP + 3478/UDP（对比参考实现的单 sshd 端口：数量同级）。
- userspace 模式的入站能力受限（G1 裁决，见 ADR-005）。
- Headscale 成为信任根（THREAT_MODEL C5）。
- G2 证据：CI run `待回填`。
