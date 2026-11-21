# TEST_PLAN — OutboundAll-hs 验收矩阵（终态映射）

> 对应源规格 Phase 11。每条绑定实现证据（CI run / 测试名 / 真机记录）。
> **执行场地 = GitHub Actions**（用户纪律）；真机 139.9.101.203 只做部署可用性与端到端冒烟。

## 1. Windows 权限测试（真机待做——CI Windows 跑者验证的是代码正确性，非真实桌面）

| 要求 | 证据 | 状态 |
|---|---|---|
| 无 UAC 提示 | sing-box userspace 无服务/驱动/TUN（上游语义）+ hs-node 全用户级 | ✅ 设计+代码 |
| 无提升令牌 | hide_windows CREATE_NO_WINDOW 子进程以父令牌运行（无 runas） | ✅ 代码级 |
| 无 Windows 服务安装 | autostart 只写 HKCU Run（N-as-2 断言禁 HKLM） | ✅ 单测 |
| 无 TUN 接口 | system_interface: false 结构性（singBoxDoc 恒输出 false）| ✅ 单测+渲染 |
| 无驱动安装 | userspace netstack，无 wintun | ✅ 设计 |
| 无全局路由改动 | 无 TUN 即无路由 | ✅ 设计 |
| 无公网入站 | 节点只有出站（ADR-001 §3） | ✅ 设计 |
| 无路由器端口映射 | 同上 | ✅ 设计 |
| **真实桌面复验** | **UNVERIFIED — 需真 Windows 桌面会话（已知沙箱限制，与参考实现同口径）** | ⚠️ 如实标注 |

## 2. 网络测试（九类失败映射 → ADR-004 终稿表）

| 类 | 证据 | 状态 |
|---|---|---|
| 1 直连可用 | phase1b ping 0-1ms | ✅ |
| 2/3 DERP 回退 | 原生 magicsock + 多 region 部署产物 | 📄 Phase 9 真机 |
| 4 控制面停 | H4a：15s 停机节点存活 | ✅ (87e7c3f) |
| 5 DNS 失败 | boot-wait/ladder 机制级 | ✅ |
| 6 TLS 失败 | https 强制（N-cfg-2），无静默降级 | ✅ |
| 7 VPS 停 | 同域 H4a + ladder | ✅ |
| 8 节点崩溃 | phase3 resume + supervisor 自动重启 | ✅ |
| 9 会话失败 | G3 会话独立 + 重连无重复 | ✅ |

## 3. 登记/授权/SSH/可靠性（全部 CI 实证）

| 矩阵 | 证据（run） |
|---|---|
| 有效邀请→注册→在线 | phase3 (360fe21) |
| 过期/重放/撤销邀请拒绝 | N3'/N4'/N5' 单测 + phase3 replay |
| 并发兑换原子 | TestRedeemAtomicUnderConcurrency |
| 崩溃恢复（redeem↔register 之间） | ADR-002 状态机 + resume 通道 |
| 身份持久 | G5 (phase1b + phase3 real path) |
| 控制器→授权节点 SSH | G4p (phase1b) |
| no-ssh 节点不可达 | G4 negative (74488ae) |
| 未授权节点 SSH 拒 | N8'/N9' |
| 撤销阻断重连 | N6'/N7' (phase3) |
| 转发正向/负向 | G1 gates (fad7a51) |
| 撤销后活动会话 | G3 观察（45s 存活，如实记录） |
| 并发 SSH 会话 | phase1b ssh 多次探测 |

## 4. 真机冒烟（139.9.101.203，部署验收）

1. hs-deploycheck 两健康端点 OK
2. headscale users create + preauthkeys create
3. hs-controller serve 起、API health OK
4. （可选）若防火墙允许 UDP 3478/443 直连：远端节点注册冒烟
5. 资源占用确认（<100MB 常驻）

## 5. 明确不测（记录理由）

- macOS 全部运行时：无真机（UNVERIFIED 口径同参考实现）
- 真实掉电/整机断电：CI 无法保真
- Headscale 多实例 HA：源规格明确初版单 VPS
- 大规模（>100 节点）：超出单 VPS 预算
