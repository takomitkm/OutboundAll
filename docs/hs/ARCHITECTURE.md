# ARCHITECTURE — OutboundAll × Headscale + DERP + sing-box（v1，Phase 1 冻结稿）

> 状态：设计冻结，G1–G5 决策门待 CI 实证后回填（ADR 001–006 各标注）。
> 参考实现语义清单：`analysis/REFERENCE_INVENTORY.md`；逐特性映射：`analysis/FEATURE_TRACEABILITY.md`。
> 执行正本：`PLAN.md`。

## 1. 总图

```
        Windows 节点（NAT 后，标准用户，零管理员）          Linux VPS（唯一公网面）                    运维者
┌────────────────────────────────────┐      ┌────────────────────────────────────┐
│ hs-node.exe（HKCU Run 自启）        │      │ Caddy :443 (TLS)                   │      ┌──────────────┐
│  └ sing-box.exe（userspace）       │      │  ├ /  → headscale 控制面           │      │ 官方 tailscale│
│     system_interface: false        │      │  ├ /derp → 内嵌 DERP (DERP over    │      │ (≥1.104.4)   │
│     ssh_server: {enabled}          │      │            HTTPS)                  │      │ controller   │
│     state_directory: %LOCALAPPDATA%│◄─────┤  └ :3478/udp → STUN                │      │ 身份(自曾)    │
│       \OutboundAll-hs\tailscale\      │ 出站 │                                    │      └──────┬───────┘
│  （无 TUN、无驱动、无服务、无入站） │      │ hs-controller（管理面服务）         │◄────────────┘
│                                    │      │  邀请/节点/授权/审计 (SQLite)       │   tailnet 内
└────────────────────────────────────┘      │ tailscale 客户端（controller 身份）│   tailscale ssh
                                            └────────────────────────────────────┘
```

要点：
- **数据面** = Tailscale/WireGuard 语义：节点间经 WireGuard 直连，失败回退 DERP 中继（加密转发）。节点**没有任何入站**需求：出站 WireGuard/DERP/STUN/控制面即可。
- **控制面** = Headscale：注册、身份、ACL/grants、SSH policy、节点撤销。
- **管理面** = hs-controller（新代码，最小化）：邀请、节点元数据、授权 profile、审计。**不复制** Headscale 的密码学身份存储。
- **VPS 对外端口**：443/TCP（控制面 + DERP over HTTPS）+ 3478/UDP（STUN）。无其他入站。

## 2. 角色与进程

传输组件按角色分配（ADR-007）：**受控端 sing-box（唯一 fork 依赖），其余一律上游原版。**

| 角色 | 进程 | 位置 | 权限 |
|---|---|---|---|
| Windows 节点 | `hs-node.exe`（supervisor，包装 sing-box） | `%LOCALAPPDATA%\OutboundAll\bin\` | 标准用户，无提升 |
| Windows 节点 | `sing-box.exe`（tailscale endpoint userspace + ssh_server） | 同上 | 标准用户 |
| Linux 控制面 | `headscale`（上游，钉 0.29.7） | systemd 服务 | 专用低权用户 |
| Linux 中继 | `derper`（上游 v1.102.1，CI 构建；自签 IP 证书 + DERP map 指纹 pin） | systemd 服务 | 专用低权用户 |
| Linux 管理面 | `hs-controller` | systemd 服务 | 专用低权用户 |
| 管理机 | 官方 `tailscale` 客户端（controller tailnet 身份持有者） | 本机用户级 | hs-admin 开箱即用（default socket） |
| 运维 CLI | `hs-admin` | 管理机 | 经 hs-controller API + tailscale ssh/nc |

Linux 节点（将来出现时）不用 sing-box：官方 tailscaled 自带 Tailscale SSH server，
直接 `tailscale up --login-server` 登记（ADR-007 D1 的"约束解除即换回上游"）。

## 3. 数据布局（全用户级 / 全低权）

```text
Windows 节点（%LOCALAPPDATA%\OutboundAll\）
    bin\        sing-box.exe, hs-node.exe
    config\     config.json（不含任何密钥）
    state\      tailscale\          ← 节点身份（tailscale state）；本目录即"节点本体"
    logs\       （轮转 + 脱敏）
    tmp\

Linux VPS
    /etc/headscale/             配置 + 私钥（低权用户可读）
    /var/lib/headscale/         sqlite state
    /etc/OutboundAll-hs/           hs-controller 配置
    /var/lib/OutboundAll-hs/       hs-controller SQLite
    /etc/caddy/                 Caddyfile
```

不变式：
- 节点 `config.json` **不含密钥**；登记凭据（邀请码）只经 `hs-node enroll` 一次性使用，用后即焚，不落盘。
- `state\tailscale\` 用 Windows 用户 ACL 收紧（icacls /inheritance:r /grant:user:F，沿用现行 platform.HardenFile 模式）。
- **state 目录即节点身份**：删除 state = 节点身份丢失（重新登记）；保留 state = 升级/重启身份不变（G5 验证）。

## 4. 三身份映射（语义继承自参考实现）

| 参考实现 | 新栈 | 私钥位置 | 能力 |
|---|---|---|---|
| Node Tunnel Key | tailscale 节点身份（machine/node key 对） | 节点 state 目录，不出机 | 建立 tailnet 连接；能力被 Headscale ACL 限制 |
| Controller Management Key | controller 的 tailnet 身份（官方客户端） | VPS tailscaled | 经 SSH ACL 对节点 shell 的访问权 |
| Enrollment Credential | 一次性邀请码 → 单用 preauth key | 邀请码只在发放/兑换瞬间存在 | 仅"注册一个新节点"这一个动作 |

分离断言（测试钉死）：持有邀请码 ≠ 能连 tailnet；持有节点身份 ≠ 能登其他节点；controller 身份 ≠ 节点身份。

## 5. 生命周期

### 5.1 登记（密码less，参考实现体验复刻）

```text
运维者:  hs-admin invite create --label win-lab [--profile default]
              → 一次性邀请码（256-bit 随机，短期过期，服务端只存哈希）
节点:    hs-node enroll --server https://hs.example.net --code <邀请码>
              → HTTPS 兑换（原子、限速、审计）
              → hs-controller 校验 → 向 Headscale 申请 single-use preauth key
              → sing-box 以 preauth key 注册 → 生成/保持节点身份（state）
              → hs-controller 登记 node 记录（映射 headscale node id + tag + profile）
              → 邀请码标记已用（一次性），preauth key 用后即废
运行:    hs-node run（常驻 supervisor 监视 sing-box；重试梯复刻参考实现）
```

崩溃安全：兑换（redeem）与注册（register）是两步——hs-controller 的邀请状态机为 `created → redeemed → registered → used/failed`；客户端崩溃在两步之间时，邀请进入 `redeemed(未完成)` 态，**同一邀请码允许同机续传一次**（以客户端生成的 enroll 会话 ID 关联），超时后作废——避免"歧义重复节点"（源规格 §6 明确要求）。

### 5.2 运行期

- `hs-node run`：起 sing-box（userspace endpoint + ssh_server），监视进程；崩溃→指数退避重启（5→10→20→40→60s，≥10 次→3600s，成功清零——参数与参考实现一致）；开机等待 ≤10 分钟探控制面。
- 断网/控制面不可达：sing-box 内部重连 + hs-node 层面 ladder 双保险；auth 类失败（preauth 无效/节点被撤销）直接跳小时级。
- `hs-admin shell <node>` / `exec <node> <cmd>`：官方 `tailscale ssh`（控制器 tailnet 身份）→ Headscale SSH ACL 裁决 → 节点 sing-box ssh_server。PTY、退出码、stdout/stderr 分报语义与参考 controller exec 一致。

### 5.3 撤销

```text
hs-admin node revoke <node>
  → Headscale: 移除 grants/SSH ACL 覆盖 + node expire（阻断重连）
  → hs-controller: node 记录 state=revoked（墓碑，审计保留）
  → 邀请：该节点名下未消费邀请全部作废
  → 会话终止时机 = G3 实测结论（不预设）
```

## 6. 网络面分层（源规格 §4 网络平面/管理平面分离的落地）

| 层 | 职责 | 组件 | 失败域 |
|---|---|---|---|
| 数据面 | WireGuard 直连 / DERP 中继 / STUN 打洞 | sing-box (tailscale 协议栈) / headscale 内嵌 DERP | DERP 故障 ≠ 控制面故障（多层 DERP 冗余是数据面可用性） |
| 控制面 | 注册/身份/策略/撤销 | headscale（Caddy TLS） | 控制面故障 ≠ 已建数据面连接（G3 实测） |
| 管理面 | 邀请/元数据/审计 | hs-controller | 管理面故障不影响既有连接与数据面 |

**禁止混称**：多 DERP 不提供控制面 HA；控制面冗余是独立课题（ADR-001，初版单 VPS）。

## 7. 版本钉（2026-11-17 核实，详见 ADR-006）

| 组件 | 版本 | 备注 |
|---|---|---|
| Headscale | v0.29.7 | 安全版；最低客户端 1.80.0；公告要求 SSH 用户客户端 ≥1.104.4 |
| sing-box | v1.14.4 | tailscale endpoint ≥1.12；ssh_server ≥1.14.0 |
| sing-box 内嵌 tailscale fork | v1.102.1-mod.5 | **< 1.104.4 → ADR-006 门控** |
| 官方 tailscale（控制端） | ≥1.104.4（当前 1.104.7） | 部署文档强制 |
| Go | 1.27 | 沿用 go.mod |

## 8. 包结构（hs/ 子树，与现行实现零冲突）

```text
hs/
  cmd/hs-node/          Windows 节点包装器（enroll/run/status/stop/uninstall/doctor）
  cmd/hs-controller/    管理面服务（HTTP API + SQLite + headscale 客户端）
  cmd/hs-admin/         运维 CLI（invite/node/shell/exec/status/doctor）
  internal/node/        配置生成、state 管理、自启动、supervisor（复刻重试梯）
  internal/enroll/      邀请兑换客户端 + hs-controller 侧邀请状态机
  internal/headscale/   headscale API 客户端（节点/grants/policy/preauth key）
  internal/authz/       授权 profile → headscale policy/grants 编译与下发
  internal/audit/       审计事件
  internal/store/       SQLite（nodes/invitations/audit_events）
  deploy/               headscale/caddy/systemd/防火墙/备份/恢复
docs/hs/               本文档 + SECURITY_MODEL + THREAT_MODEL + ADR/
```

现行 `cmd/OutboundAll` / `internal/*` 原样保留（参考实现继续可用，CI 不变）。

## 9. 平台矩阵（声明口径）

| 能力 | Windows | Linux 节点 | macOS |
|---|---|---|---|
| userspace endpoint + ssh_server | 目标（CI 双跑者验证） | 可选（Linux 节点可用官方 tailscaled，ssh_server 同语义） | UNVERIFIED |
| 零管理员 | 核心目标 | n/a（Linux 通常有 root） | UNVERIFIED |
| hs-controller / hs-admin | n/a | 目标 | UNVERIFIED |

诚实原则沿用参考实现：无真机平台一律标 UNVERIFIED，不虚报。
