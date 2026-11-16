# PLAN — dialhome 能力在 Headscale + DERP + sing-box 上的重实现（终版执行计划）

> 源规格：用户《Engineering Task: Reimplement dialhome capabilities on Headscale + DERP + sing-box》（2026-11-17）。
> 本文件 = 该规格经 Phase 0 全量文档调研 + 依赖版本核实后的**修改终版**，是本工作的唯一执行正本。
> 与源规格冲突处，以本文「与源规格的偏差」一节为准；未列出的内容原样继承源规格。
> 基线：`main @ ecec22b`（2026-11-17，本地 = origin/main，0/0）。

---

## 0. 调研结论（为什么这样改）

### 0.1 参考实现（现行 Go 版）语义盘点

已通读 `docs/` 全部 22 份文档 + README + CI + 源码树。现行版的核心语义（新栈必须逐条映射）：

| 参考语义 | 出处 | 新栈映射目标 |
|---|---|---|
| 三身份分离：Node Tunnel Key / Controller Management Key / 一次性 Enrollment Credential | SECURITY_MODEL §1 | Tailscale 节点身份（本机 state，私钥不出机）/ Controller 自己的 tailnet 身份 + Headscale admin（只存 VPS）/ 一次性邀请码 + 短期 preauth key（≠ 节点身份，≠ 管理凭据） |
| 零管理员：无 UAC/服务/驱动/TUN/系统路由/入站端口 | 规格 §2 + REFACTOR_BASELINE §4 | sing-box `system_interface: false`（userspace，**天然无 TUN/驱动**）+ HKCU Run 自启动 + 全部状态在用户目录 |
| 回环限定（`127.0.0.1` 字面量，permitlisten 精确匹配） | SECURITY_MODEL §1 | 无需等价物：新栈里节点**根本不暴露任何非 tailnet 面**；暴露面从「relay 回环口对」收敛为「tailnet 内 ACL」 |
| 受限 AK 行（N1–N11 负向矩阵） | SECURITY_MODEL §6 | Headscale policy（deny-by-default）+ SSH ACL + sing-box `disable_forwarding`；新负向矩阵 N1'–Nn' 见 §5 |
| 撤销语义：invitation/node/会话分层；撤销后旧 key 失效（N10）；撤销不复活（N11） | SECURITY_MODEL §6、RELAY_MANUAL §4 | Headscale node expire/delete + ACL 移除 + 邀请撤销；**会话终止行为必须实测**（源规格 §7 明确要求，不许声明未经测试的"立即踢下线"） |
| 重试梯 5→10→20→40→60s，≥10 次 3600s，auth 失败直跳小时级；开机等待 ≤10min；成功清零 | NODE_MANUAL §8 | hs-node supervisor 逐参数复刻（失败模式清单见 §5.2） |
| 幂等登记：同机重装复用槽位；崩溃在 redeem 与 register 之间不留歧义重复节点 | PROTOCOL §1、规格 §6 | 邀请一次性原子兑换 + headscale 节点注册幂等（以 machine key/state 目录为锚） |
| 日志脱敏（私钥体/sk-*/Bearer/公钥体） | SECURITY_MODEL §5 | logging 包复用 + 新增 tailscale node key/preauth key 模式 |
| 凭证一次性、短期（24h）、原子兑换、重放拒绝 | PROTOCOL §1 | 邀请服务（规格 §6 全部 9 项要求） |

### 0.2 依赖核实（2026-11-17 实查，全部有原始出处）

| 组件 | 版本 | 关键事实 |
|---|---|---|
| Headscale | **v0.29.7**（2026-10-30，安全版） | 最低客户端 v1.80.0；公告明确：**Tailscale SSH 用户应升级客户端 ≥ v1.104.4**（修复 SSH forwarding / session handling / Serve 漏洞） |
| sing-box | **v1.14.4**（2026-10-24） | tailscale endpoint 自 1.12.0；`system_interface` 自 1.13.0；**`ssh_server` 自 1.14.0**；1.15 尚未正式发布（tailcat/on_demand 为 1.15 文档） |
| sing-box 内嵌 Tailscale | **sagernet/tailscale fork v1.102.1-mod.5**（tip `79c173244`，2026-10-19） | **低于 1.104.4 安全阈值**——这正是 Headscale 公告点名要升级的那条 SSH 攻击面，而它恰是新架构里 Windows 节点 SSH server 的宿主 |
| 官方 Tailscale（VPS 控制端用） | **v1.104.7**（2026-11-12） | 满足公告要求 |

**结论**：架构可行；存在一个**已证实的安全缺口**（内嵌 fork 的 SSH 栈 < 公告阈值）。处理 = ADR-006 门控（§4），不静默绕过、不过度渲染（Mitigation 见 ADR）。

---

## 1. 与源规格的偏差（全部有理由，其余原样继承）

| # | 源规格 | 终版 | 理由 |
|---|---|---|---|
| D1 | Phase 1 产出 `docs/ARCHITECTURE.md` 等（根级覆盖） | 新架构文档全部进 **`docs/hs/`**（`docs/hs/ARCHITECTURE.md`、`docs/hs/ADR/…`） | 现行 Go 版同名文档**必须保留**（源规格 §16 自己要求"preserve the existing working implementation"）；文档是现行版验收证据链的一部分 |
| D2 | Phase 8 目录 `cmd/controller`、`internal/…` | 新实现全部进 **`hs/`** 子树：`hs/cmd/hs-node`、`hs/cmd/hs-controller`、`hs/cmd/hs-admin`、`hs/internal/…`、`hs/deploy/…` | 与现行 `cmd/dialhome` / `internal/*` 零冲突；同一 Go module，共享 `internal/logging`、`internal/lockfile` 等成熟件 |
| D3 | 二进制命名 | `hs-node`（Windows 节点包装器）、`hs-controller`（VPS 管理面服务）、`hs-admin`（运维 CLI） | 同 D2 |
| D4 | Phase 2 直接启用 sing-box ssh_server | **门控启用**（ADR-006）：测试/CI 全量启用；生产默认 `disable_forwarding: true` + deny-by-default SSH ACL；**生产解锁须满足其一**：(a) sagernet fork 跟进 ≥1.104.4 对应修复；(b) 机主书面接受残余风险并记录在 ADR | 依赖核实发现的 SSH 安全缺口（§0.2），源规格 Phase 1 自身要求"不能静默绕过" |
| D5 | （未提）控制端 SSH 客户端 | VPS 控制端用**官方 Tailscale 客户端（≥1.104.4）**承载 controller tailnet 身份与 `tailscale ssh` | 官方客户端满足公告阈值；源规格 §8 本就要求"用真实支持的 Tailscale SSH 客户端路径" |
| D6 | 测试执行环境 | **全部测试跑 GitHub Actions**（ubuntu + windows 双跑者），本机不跑构建/测试 | 用户既定纪律：GH Actions 是第一验证场；本机不装复刻环境 |
| D7 | DERP 拓扑 | 初版 = Headscale **内嵌 DERP**（443/TCP /derp 路由 + 3478/UDP STUN）；多 DERP profile 作为配置产物交付，CI 只验单 DERP | 源规格 §12 允许单 VPS 起步；多 DERP 属部署扩展 |
| D8 | 「anchor」概念 | **不再引入自定义 Anchor Manager**（除非 CI 实测证明 Tailscale 原生路径切换不满足参考语义）；锚点语义映射为「DERP failover + 控制面 URL」两层，失败分类九类逐条映射 | 源规格 §10 自己要求"不要实现原生已保证的 failover" |

---

## 2. 决策门（CI 实证，写进 ADR，不许拍脑袋）

| 门 | 问题 | 验证方法 | 影响 |
|---|---|---|---|
| **G1** | userspace 模式（`system_interface:false`）下，tailnet 入站流量除 ssh_server 外能否到达任意本机回环服务？ | CI：Windows/Ubuntu 跑者上起 headscale + sing-box endpoint，从第二节点打节点 tailnet IP 的非 22 端口，观察能否转发到 `127.0.0.1:<svc>` | 若不能：服务转发（Phase 6）只能走 sing-box SSH forwarding（全局开关），**逐服务授权无服务端强制点** → 按源规格要求记录偏差；若能：评估 per-service 配置形态 |
| **G2** | sing-box endpoint 的 `control_url` 能否用 `http://`（CI 无真实域名/TLS）连 Headscale？ | CI 实测；不行则在 CI 里给 Headscale 配自签 TLS + endpoint 信任自签 | 只影响 CI 形态；生产恒经 Caddy 443 TLS |
| **G3** | 撤销已连接节点（Headscale node expire/delete）后，**已建立的 SSH 会话**是否立即终止？ | CI 实测：建会话 → revoke → 观察会话存活时长 | 决定撤销语义文档措辞（源规格 §7 明令不许未经测试就声明"立即踢会话"） |
| **G4** | sing-box ssh_server 与 Headscale 的 SSH policy（policy v2 / grants）协同是否生效（deny-by-default、逐客户端放行）？ | CI：配置 allow-controller-only SSH ACL，验证未授权 tailnet 节点被拒 | 授权矩阵（Phase 4）的实现基础 |
| **G5** | 节点身份持久化：state 目录保留时重启/重注册是否复用同一节点身份（不产生重复节点）？ | CI：重启 sing-box 进程（保留 state 目录）→ Headscale 侧节点数不变 | 登记幂等的核心断言 |

每个门的结论连同 CI run 链接记入 `docs/hs/ADR/`（G1→ADR-005/006，G2→ADR-001，G3/G4→ADR-003，G5→ADR-002）。

---

## 3. 版本钉与组件矩阵

| 组件 | 钉定版本 | 交付物 |
|---|---|---|
| Headscale | v0.29.7 | `hs/deploy/headscale/`（config + systemd unit + 升级说明） |
| sing-box | v1.14.4 | `hs-node` 按 sha256 钉定下载（Windows amd64；沿用现行 `internal/openssh` 的"固定版本+哈希+回退"模式） |
| Tailscale（VPS 控制端） | ≥1.104.4（当前 1.104.7） | 部署文档写明"低于 1.104.4 禁止承载 controller SSH 身份" |
| Caddy | 2.x 当前稳定 | 反代 443 → headscale + /derp |
| Go | 1.27（沿用 go.mod + `GOTOOLCHAIN: local`） | CI 不变 |

---

## 4. ADR 清单（`docs/hs/ADR/`）

| ADR | 主题 | 决策内容 |
|---|---|---|
| 001 | 网络架构 | Headscale 控制面 + 内嵌 DERP（443/derp + 3478 STUN）+ Caddy TLS；Windows 节点 userspace endpoint；控制面 URL 稳定；Headscale HA 与 DERP HA 分层（G2 证据挂这里） |
| 002 | 身份与登记 | 三身份映射表；邀请≠preauth key≠节点身份；state 目录即节点身份载体；G5 证据 |
| 003 | 逐节点授权 | Headscale policy v2/grants deny-by-default；逐节点 tag + SSH ACL；G3/G4 证据；撤销语义分层 |
| 004 | 锚点/故障转移 | DERP failover 原生语义 vs 参考实现的 9 类失败映射；控制面 HA 独立；是否需要自定义组件的裁决 |
| 005 | 服务转发 | G1 结论 + 服务转发最终形态（原生 forwarding 开关 / 显式 broker / 文档化偏差） |
| 006 | SSH server 安全门控 | 内嵌 fork < 1.104.4 缺口的完整论证（来源链接）、Mitigation（disable_forwarding 默认、deny-by-default ACL、CVE 跟踪）、解锁条件 (a)/(b) |

---

## 5. 测试设计（按 writing-tests 五阶段）

### 5.1 总则
- **执行场地 = GitHub Actions**（ubuntu-latest + windows-latest）。CI 现有三 job（vet+test / build-matrix / smoke-linux）不动，新增 `hs-test`（单测，双 OS）与 `hs-e2e`（集成，单 job 内全链：headscale + 2×endpoint + controller 客户端）。
- 机器可读：`go test ./hs/... -json` 落 artifact + 末尾汇总步（失败数/总数/耗时/版本指纹两行总汇）。
- 集成测试用的第三方二进制（headscale/sing-box）按 §3 版本钉 + sha256 校验下载（沿用现行 `internal/openssh` 模式）。
- **Iron Law**：每条用例绑定失败模式清单中的一条；每条负向用例必须红过一次（校准证据记 `analysis/CALIBRATION.md`，含 CI run 链接）。

### 5.2 失效模式清单（风险登记表，写用例前先落地到 `analysis/FAILURE_MODES.md`）
按八大类枚举，重点覆盖：
- 失控写入/状态损坏：state 目录损坏→检测与安全恢复（不静默重生成身份）；config 校验拒绝非 tailnet 注入。
- 并发与锁：邀请并发兑换（原子性）、同机双 supervisor 实例锁、registry 写串行化。
- 网络九类失败（源规格 §10）：直连失败→DERP、首选 DERP 失效、全 DERP 不可用、控制面不可达、DNS 失败、TLS 失败、VPS 不可达、进程崩溃、既有会话失败——每类一条用例或一条"明确不测"记录。
- 环境非标：NAT/CGNAT（CI 以防火墙规则模拟 UDP 阻断）、开机早于网络（boot-wait）。
- 配置自伤：`system_interface` 误开、`disable_forwarding` 误开、SSH ACL 缺省放行——全部负向。
- 自身 bug 历史账目：迁移参考实现的已知坑（B1 精确匹配等价物、per-source penalty 节奏、ExitOnForwardFailure 等价语义）。
- 合法结局集合而非具体值：如 G3 会话终止 ∈ {立即终止, ≤T 秒终止, 会话保留至重连}，断言落在集合内并在文档如实记录。

### 5.3 「明确不测」清单（记录理由）
- macOS：无真机，沿用现行版 UNVERIFIED 口径。
- 真实公网跨区 DERP、真实掉电：CI 无法保真，标注外部补验。
- Headscale 多实例 HA：源规格明确初版单 VPS。

### 5.4 评审者五形状预审（对外提交前）
生命周期（真 runtime 集成非 fake）/ 组合缝隙（授权×转发×撤销叠加）/ 对抗输入（畸形邀请码、坏 JSON、非 HTTPS 200）/ 对应关系（节点↔记录↔ACL 对位）/ 缺席类（宣传的功能必须有断言它的测试；降级路径可观察）。

---

## 6. 执行步骤（每步 = 一个 commit + push 到 main；CI 绿后进下一步）

> 推送授权：本任务用户已明确"push after finishing every step"，即每步完成后推送 main 无需逐次再问。
> CI 红 = 停下修，修绿才进下一步。每步更新 `IMPLEMENTATION_PROGRESS.md`。

| Step | 内容 | 对应源规格 |
|---|---|---|
| 0 | 本计划（PLAN.md） | — |
| 1 | `analysis/REFERENCE_INVENTORY.md` + `analysis/FEATURE_TRACEABILITY.md`（含语义区分表） | Phase 0 |
| 2 | `docs/hs/ARCHITECTURE.md` + `SECURITY_MODEL.md` + `THREAT_MODEL.md` + ADR 001–006（初稿，标注待 G 门回填） | Phase 1 |
| 3 | CI `hs-e2e` 骨架：钉版本下载 headscale/sing-box → G1/G2/G5 实证 → ADR 回填证据 | Phase 1 |
| 4 | `hs-node`：布局、config 生成、state 保护、HKCU Run 自启动、supervisor（重试梯复刻）、日志轮转+脱敏；单测 | Phase 2 |
| 5 | 邀请服务 + 兑换流程（原子、一次性、过期、撤销、限速、审计、崩溃安全）；单测+集成 | Phase 3 |
| 6 | 逐节点授权：Headscale policy 管理（deny-by-default、逐节点 grant、SSH ACL）；G4 集成 | Phase 4 |
| 7 | `hs-admin` CLI：invite/node/shell/exec/status/doctor；与官方 tailscale ssh 对拍 | Phase 5 |
| 8 | 服务转发：按 G1 结论落地（或文档化偏差） | Phase 6 |
| 9 | 锚点/故障转移：DERP map 生成、健康检查、九类失败映射测试 | Phase 7 |
| 10 | `hs-controller` 收口：SQLite 持久化、审计、API 鉴权、健康端点 | Phase 8 |
| 11 | 部署产物：`hs/deploy/`（headscale/caddy/systemd/防火墙/备份/恢复/升级/回滚） | Phase 9 |
| 12 | 安全评审：THREAT_MODEL 终稿 + 五形状预审 + 威胁清单逐条落地状态 | Phase 10 |
| 13 | 验收测试矩阵：Windows 权限测试、网络测试（CI 可模拟部分）、登记/授权/SSH/可靠性矩阵；机器可读结果 | Phase 11 |
| 14 | 文档与发布：`docs/hs/` 全套（ENROLLMENT/WINDOWS_NO_ADMIN/SSH_MANAGEMENT/AUTHORIZATION/SERVICE_FORWARDING/ANCHOR_FAILOVER/DEPLOYMENT/OPERATIONS/BACKUP_RECOVERY/TROUBLESHOOTING/TEST_PLAN）+ 迁移指南 + 发布清单 + 终版报告（逐特性对拍） | Phase 12 |

---

## 7. 非协商要求（从源规格原样继承，执行中逐条对照）

**Windows 节点**：Win10/11 x64；标准非提升用户；永不 UAC/管理员/服务/Wintun/系统 TUN/系统路由/路由器端口映射/公网入站；NAT 后可用；全部连接自发起；支持远程命令与交互 shell；命令以启动客户端的账户执行；重启保持身份；网络故障自愈；`system_interface: false`；登录时经用户级机制自启；**不声明 pre-logon 访问**。

**Linux VPS**：单台普通 VPS 可完整部署（Headscale + 内嵌 DERP + Caddy + hs-controller + tailscale 客户端）；全部部署可复现（持久状态、备份、升级、恢复）；不强制多机。

**语义区分**（源规格 §3 原文要求，写进 traceability 表）：认证 key vs 持久节点身份；撤销登记 vs 撤销已登记节点；断开活动会话 vs 删除设备；DERP failover vs 控制面 failover；本地转发 vs 远程转发；shell 许可 vs 端口转发许可。

**执行纪律**：跨阶段自主推进，不逐阶段请示；不做伪代码，实现并实测；大逻辑阶段分 commit；现行实现保留到替代品通过对应测试；不覆盖生产数据/状态；不推上游不做破坏性 VPS 变更；组件名对上≠功能等价，逐特性验收。

---

## 8. 最终验收标准（继承源规格 §17，附新栈特有项）

源规格 §17 全部条目 + 以下新栈特有项：
- 依赖版本钉死且与文档一致（Headscale 0.29.7 / sing-box 1.14.4 / 客户端 ≥1.104.4）；
- ADR-006 门控状态明确（fork 未跟进时的运行姿态与风险记录）；
- G1–G5 每个门有 CI 证据与结论；
- 新负向矩阵（N' 系列）全绿且每条有红过一次的校准记录；
- 现行 Go 版在整项工作结束时**仍然可用**（其 CI job 全程保持绿）。

---

## 9. 风险与未决

| 风险 | 处置 |
|---|---|
| 内嵌 fork 的 SSH 漏洞在 fork 更新前持续存在 | ADR-006 门控 + Mitigation + 跟踪 sagernet/tailscale fork；解锁条件写死 |
| CI 跨 OS 集成（Windows 跑者上跑 headscale.exe + sing-box.exe）可能遇环境差异 | e2e 先在 ubuntu 落地全链；Windows 侧先 smoke 后全链；环境性失败与代码失败分账 |
| GitHub 跑者间跨 job 网络不可靠 | e2e 收敛为单 job 内全链（每 OS 自包含），不依赖 runner 间互连 |
| Headscale policy/grants 细节与文档不符 | G4 实证后回填 ADR-003；不许按文档想当然写断言 |
