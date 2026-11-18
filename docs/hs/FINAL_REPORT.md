# FINAL REPORT — dialhome 能力在 Headscale + DERP + sing-box 上的重实现

> 基线 `main @ ecec22b`（参考实现冻结）→ 终点 `phase5` 分支。
> 规格正本 `PLAN.md`；特性追踪 `analysis/FEATURE_TRACEABILITY.md`；台账 `IMPLEMENTATION_PROGRESS.md`。

## 1. 特性对拍（参考 → 新栈）

| 参考特性 | 参考实现 | 新栈 | 验收 |
|---|---|---|---|
| 三身份分离 | tunnel key / manager key / 一次性凭证 | 节点身份 / controller 身份 / 邀请码→preauth（+node secret） | phase3 e2e |
| 密码less 登记 | ed25519 凭证（私钥内嵌安装器） | 邀请码兑换 + 单用 preauth + node secret resume | phase3 全链 |
| 幂等登记（重装不重复） | 公钥指纹命中槽位 | state 目录身份 + headscale 归并 | G5 (phase1b+3) |
| 零管理员 | rootless sshd + HKCU Run | sing-box userspace（无 TUN/驱动）+ HKCU Run | 设计+单测 |
| 回环限定 | permitlisten 逐口 | **双闸**：headscale grant(WHO) × node route allowlist(WHAT) | G1 gates (fad7a51) |
| 远程 shell/exec | 经 relay 回环 ssh | tailscale ssh（官方客户端，SSH ACL） | G4p |
| 撤销语义分层 | AK 行删除+墓碑 | headscale expire+ACL 移除+墓碑+邀请作废 | N6'/N7' (phase3) |
| 重试梯 | 5→60s→3600s, auth 直跳 | 同参数复刻（procman ladder） | N-pm-1/2/3 |
| 服务转发 | ssh -R permitlisten 逐口 | sing-box route rules（显式 allow + reject-all） | G1 gates |
| 管理面 | registry.json + AK 文件 | hs-controller（JSON store+锁+原子写+备份） | phase3/8 |
| 审计 | 墓碑+AK 备份 | audit_events（上限 10k）+ 管理操作全记录 | phase8 |
| 运维 CLI | dialhome controller/relay 子命令 | hs-admin（API 客户端）+ hs-controller CLI | phase5 e2e |
| DERP 回退 | （无——relay 是唯一入口） | headscale 内嵌 DERP + STUN | 部署产物 |

## 2. CI 实证总览（全部 run 在 GitHub Actions）

| 证据 | run |
|---|---|
| 架构门 G2/G5/G4p | 128b5f4 |
| 登记全链（enroll/resume/revoke） | 360fe21 |
| 授权正/负 + G1 四门 | 74488ae → fad7a51 |
| 控制面中断/恢复 H4a/H4b | 87e7c3f |
| 硬化（审计上限/轮换/计数器） | 739d631 |
| hs-build artifacts | 6f691cd |
| **最终全绿 commit** | cfd08aa→fad7a51 线（6 job 含 hs-build） |

## 3. 偏差与诚实记录

| 偏差 | 理由 | 位置 |
|---|---|---|
| 新代码 hs/ 子树、文档 docs/hs/ | 参考实现保留（规格 §16 自身要求） | PLAN D1/D2 |
| ssh_server 生产门控 | 内嵌 fork < 1.104.4（ADR-006）；CI 全开、生产默认 disable_forwarding+deny-by-default | ADR-006 |
| 控制端用官方 tailscale ≥1.104.4 | 公告要求 | D5 |
| JSON store 非 SQLite | 仓库纯 stdlib 纪律；单 VPS 语义等价 | ledger |
| Windows 真实桌面复验 | 沙箱限制（同参考实现口径） | TEST_PLAN §1 |
| DERP 多 region | 部署产物（Phase 9 真机） | ADR-004 |
| 逐会话终止 | 无 API；expire 不杀已建会话（G3 如实） | ADR-003 |

## 4. 安全净发现（全部修复+测试钉住）

1. sing-box 裸默认 = 任意同端口回环暴露 → reject-all 兜底
2. token 轮换后旧 token 经 Options 快照仍有效 → 改读 store 当前 sha
3. windows lockfile 包级 mutex 死锁 → 删除
4. 凭证换半（组装/传递链）→ fromSeed 派生不可表达 + JSON 自证双层

## 5. 部署（139.9.101.203, 2c/1G）

- 产物：GH Actions hs-build（5 二进制 + SHA256SUMS），scp 上机
- deploy.sh 幂等：headscale 0.29.7 钉定 + systemd 双 unit + 只开 22/3478
- 验收：hs-deploycheck 双端点 + 资源占用（OPERATIONS.md）
- 状态：见台账（真机下载 35KB/s 限制，分步完成）

## 6. 遗留与边界（如实）

- Windows 真实桌面 HKCU Run 复验：需真桌面会话（沙箱限制）
- macOS：UNVERIFIED（无真机，同参考口径）
- DERP 多 region 阻断演练：单 VPS CI 无法模拟，部署产物+真机可加
- 控制面 TLS：生产跨网络必须 Caddy 终结（OPERATIONS §6），当前单机回环部署不含
- 备份加密：备份含 db+admin-token（0600），加密属部署方决策
