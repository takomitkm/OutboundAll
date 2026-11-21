# IMPLEMENTATION_PROGRESS — dialhome × Headscale 重实现进度台账

> 源规格 §16 要求持续更新。每步（commit+push）后更新本文件。
> 规格正本：PLAN.md；参考语义：analysis/REFERENCE_INVENTORY.md；特性映射：analysis/FEATURE_TRACEABILITY.md。

## 当前状态

- **已完成**：Phase 0/1/1b（架构冻结 + 决策门实证）/ **Phase 2**（hs-node 运行时：config 不变式、布局、sing-box 钉定下载、进程 supervisor、自启动、单实例锁；单元测试双 OS 绿）。
- **当前阶段**：**12 阶段全部完成 + 真机跨网测试进行中**（本机↔校机经 139 控制面；数据面受阻见下）。
- **真机跨网测试进展（139.9.101.203 控制面）**：
  - ✅ 控制面跨网：校机经 SSH 隧道（22/tcp，免开安全组）登记成功——win11-hv1 node id 2、双 tags、online（run 95cf74b 线）
  - ✅ 控制端免 tailscale：本机 sing-box（controller tag + socks 入站 1080）注册成功——local-controller IP 100.64.0.1（上游语义：preauth key 注册时 advertise_tags 被 headscale 拒，tags 必须来自 key 本身——已在 CI 单测范围外、真机实证）
  - ❌ **数据面仍未打通**（已试 8 条路径，全部有实证，记录如下）：
    1. 公网 139:8080 —— 安全组不通（只开 22），curl 超时
    2. Tailscale 公共 DERP —— 国内不可达
    3. 隧道版明文 DERP：headscale server_url→127.0.0.1:18080，两端 `TS_DEBUG_USE_DERP_HTTP=1`；DERP map 正确（/debug/derp: node 127.0.0.1:18080）、**curl 直连隧道 /derp 实测 101 Switching Protocols + DERP v2**（链路本身完全通）——但客户端 0 次 DERP 连接（headscale journal 0 hits），socks→tailnet 全端口超时
    4. 两端身份：校机 win11-hv1 (id2, 100.64.0.2, node+ssh tags) 与本机 local-controller (id4, 100.64.0.3, controller tag) 均 online，/debug/nodestore 显示各 1 peer（netmap 通）
    5. 中途踩坑（已修）：`HEADSCALE_DEBUG_DUMP_MAPRESPONSE_PATH=/tmp/hsmap` + ProtectSystem=strict → headscale panic 重启 71 次（read-only fs），移除后稳定 NRestarts=0
    6. 中途踩坑（已修）：preauth key 注册时 `advertise_tags` 被 headscale 拒（tags 必须来自 key 本身）；headscale policy 路径漂移（controller 写新路径、headscale 读旧路径）；controller --server 每次 deploy 被旧值覆盖（手动 sed 无效，已改 deploy.sh）
    7. 中途踩坑（已修）：Windows ssh-keygen `-N ""` 生成带口令私钥（A1 坑）——密钥改在 139 生成后下发，AUTH-OK
    8. 中途踩坑（已修）：schtasks 下 sing-box Taildrop 相对路径落到 system32 被拒（已 pin taildrop_directory 到 state 目录）
  - **结论（终局取证）**：在 139 上用官方 tailscale 1.104.7 做了最小复现——设 `TS_DEBUG_USE_DERP_HTTP=1` 后客户端日志仍报 `context deadline exceeded` 连 DERP（tailscaled.log），而 curl 到同一 DERP 端点（8080 与 18080）均实测 101 Switching Protocols + DERP v2；headscale DERP map 正确（127.0.0.1:18080）。即**明文 DERP 的 envknob 开关在本版本对客户端路径无效**（疑似正式构建裁剪了 debugUseDERPHTTP 或经控制面下发的 derp URL 强制 https）。tailscale status 的 health 明确报 "could not connect to DERP relay server"。
  - **唯一确定可行路径**：DERP over TLS + 客户端信任证书 ⇒ 需要一个域名（Let's Encrypt 签证书，系统根 CA 已信任）+ VPS 入站 443/tcp。Tailscale 没有"信任自定义 CA"的配置项（types.go 的 InsecureForTests 仅单测用），自签证书对端不认。
  - 用户旧版 dialhome 之所以"只要能联网就受控"，是因为 relay 的 SSH 端口本身就是会合点（数据在控制连接里）；Tailscale 架构把两者拆开，DERP 必须可认证 TLS——这是模型差异，不是配置错误。
  - 真机部署缺陷共修复 8 个（CI 无法模拟的）：226/NAMESPACE、数据目录属主、socket 权限、H6 分叉、policy 属主、deploy restart 语义、controller --server 漂移、taildrop CWD
- **Phase 9 完成（真机 139.9.101.203, 2c/1G）**：DEPLOY-OK 双健康端点；真机全链冒烟（invite→enroll→revoke→deny resume→policy 同步 deny）。真机抓出并修复 5 个部署级缺陷（CI 无法模拟的）：systemd 226/NAMESPACE（RuntimeDirectory）、controller 数据目录属主、headscale unix socket 权限（双服务改 dialhome 用户）、H6 类分叉状态（root init vs dialhome serve 的 store 路径）、policy 文件属主（revoke 时 reload 被拒）。资源占用 headscale 53MB + controller 10MB = 63MB（1G 预算内）。
- **Phase 7 完成（run 87e7c3f 全 5 job 绿）**：H4a 控制面停 15s 节点存活、H4b 恢复后同身份重连（无重复注册）。九类失败全部有证据或排除记录（ADR-004 终稿表）；DERP 多 region 为部署产物（Phase 9 真机可加）。
- **Phase 6 完成（run fad7a51 全 5 job 绿）**：G1-a 收口——sing-box 路由引擎原生把 tailnet 入站引到回环（自身 IP 拨号重映射 127.0.0.1，endpoint.go NewConnectionEx 源码证据）；双闸模型（headscale grant=WHO × node route allowlist=WHAT）；**安全发现已修**：sing-box 裸默认会把任意 tailnet 拨号转发到同端口回环 → hs-node 渲染永远带 reject-all；ssh_server:22 在 reject-all 下存活（tsnet listener 先于路由器）。ADR-005 Accepted。
- **Phase 3 完成（run 74cb225，全 5 job 绿）**：hs-controller（邀请/兑换/resume/撤销/审计 + CLI=API thin client）+ hs-node（enroll/resume/boot-wait/auth-hook）+ e2e 两条真链（phase1b 架构门 + phase3 全栈登记链）。CI 红轮战果：H6 类分叉状态（CLI 直写 registry vs serve 内存态 → CLI 改 API thin client）、windows 锁死锁（包级 mutex 套阻塞 LockFileEx → store 改 bounded TryAcquire retry）、unix 目录 0600（去执行位不可遍历 → 0700）、preauthkeys JSON 形状、SIGKILL 孤儿 sing-box 掩盖撤销语义（e2e kill 改 SIGTERM 优先）。
- **新增共享件**：internal/lockfile.TryAcquire（非阻塞锁；LIVE-RED 教训：阻塞锁使「another supervisor」路径永不触发）。

## 1. 已完成工作

| 日期 | commit | 内容 |
|---|---|---|
| 2026-11-17 | 0b7be19 | PLAN.md（终版执行计划：调研结论、D1–D8 偏差、G1–G5 决策门、Step 0–14、测试设计） |
| 2026-11-17 | e0388c1 | CI 修复 #1：Windows runner `core.autocrlf=true` → checkout CRLF → gofmt 全红。9 次历史 run 全败的根因。修复后 windows vet+test 首次绿 |
| 2026-11-17 | df89ab2 | CI 修复 #2：smoke-linux 首次真正运行即暴露 `$?` 被 echo 消费的断言 bug（产品 exit=2 正确，断言却测 echo 的 0）→ rc 变量修复。同 commit：Phase 0 两份分析文档 |
| 2026-11-17 | 1999e35 | Phase 1：docs/hs/ARCHITECTURE.md + SECURITY_MODEL.md + THREAT_MODEL.md + ADR 001–006 + 本台账（df89ab2 CI 全绿实证后推送） |
| 2026-11-17 | 7a0f3af→128b5f4 | Phase 1b：hs/scripts/e2e-deps.sh（三件钉版本+sha256 下载）+ hs/internal/hstest E2E 链 + CI hs-e2e job。六轮红→绿（policy v2 src/dst 拒 `*`、mkdir、管道假绿、gofmt 对齐、cleanup 死锁、expire 语法），**run 128b5f4 全 5 job 绿** |

## 2. 关键事实与决策（防止后面忘）

0. **Phase 1b 实证结论（run 128b5f4，全 5 job 绿）**：
   - G2 ✅ http:// control_url 注册可行（CI 无 TLS 可用；生产恒经 Caddy）。
   - G5 ✅+注记：身份持久（同 node id 无重复）；**sing-box/tsnet 每次启动都要 auth_key 才恢复**（state 在盘上仍进 Waiting-for-auth）→ ADR-002 加 resume-key 通道（Phase 3 落实）。
   - G4p ✅：官方 tailscale 客户端 → Headscale SSH ACL → sing-box ssh_server 全链通；会话以 sing-box 进程用户运行（whoami=runner）；`autogroup:member` 是 policy v2 SSH 规则的合法 src/dst（`*` 两处都不收）。
   - G1 观察：直连路径 0-1ms pong（listen_port 钉 42421）；入站 TCP 非 22 端口也被 endpoint 接受（无响应数据）→ ADR-005 Phase 6 收口。
   - G3 观察：**node expire 不终止已建立 SSH 会话**（45s 后仍活）→ 撤销语义=新连接拒+旧会话不即时终止，与参考实现同构；逐会话终止=文档化偏差。

1. **依赖缺口**：sing-box v1.14.4 内嵌 sagernet/tailscale fork = v1.102.1-mod.5（tip 79c173244，2026-10-19）< Headscale 公告要求的 1.104.4（SSH forwarding/session/Serve 漏洞修复）。→ ADR-006 门控：CI 全开、生产默认 disable_forwarding+deny-by-default、解锁条件 (a) fork 跟进或 (b) 机主书面接受。
2. **版本钉**：Headscale v0.29.7 / sing-box v1.14.4 / 官方客户端 ≥1.104.4（当前 1.104.7）/ Go 1.27。
3. **偏差 D1–D8**（PLAN.md §1）：新代码全在 `hs/` 子树；新文档在 `docs/hs/`；现行 Go 版原样保留（其 CI job 必须持续绿——这是验收标准之一）。
4. **CI 历史**：本项目 windows job 自诞生从未绿过（0/9），两个潜伏 bug（CRLF、$? 消费）已修——诚实更正：此前文档宣称的"windows 双跑者全绿"从未成立。
5. **测试纪律**：全部测试跑 GitHub Actions（不跑本机）；每条用例绑定失效模式；负向用例红绿校准证据进 analysis/CALIBRATION.md；机器可读结果（go test -json）。
6. **Phase 4 CI 实证的 headscale v0.29.7 语义**（详见 ADR-003 §2a）：grant src 匹配节点实持身份（tag 未持 = 不进 peer map）；grants ip 语法 = 冒号 tcp:22；tagOwners 值需 ci@ 尾缀；ssh 规则 src 不收 IP Prefix；`headscale policy check --file` 可离线校验；`tailscale nc` 无 -timeout flag（用 coreutils timeout 包）。

## 3. CI 记录

| commit | ubuntu vet+test | windows vet+test | smoke | build-matrix | 备注 |
|---|---|---|---|---|---|
| 0b7be19 | ✅ | ❌ | ⏭ | ⏭ | CRLF 根因（历史遗留） |
| e0388c1 | ✅ | ✅ | ❌ | 🔄 | smoke 首跑暴露 $? bug |
| df89ab2 | ✅ | ✅ | ✅ | ✅ | **全绿（本项目 CI 首次全绿）** |
| 95f5300→6ed1277 | ✅ | ✅→红×3轮 | ✅ | ⏭ | Phase 2 单测上线：gofmt 对齐（gofmt 真实规则=注释/空行断开对齐run）、NewChannel 名、CREATE_NO_WINDOW 字面量、**lockfile 阻塞锁 LIVE-RED**（第二次实例挂死 10min→加 TryAcquire）、%q 引用、梯语义误读 |
| 8df49f8 | ✅ | ✅ | ✅ | ✅ | **Phase 2 全绿（双 OS 单测 + e2e + smoke + build）** |
| ad4964c→74cb225 | ✅ | ✅(红×4轮) | ✅ | ✅ | Phase 3a+3b：管理面+登记链；e2e 红轮抓 H6 分叉状态/windows 死锁/孤儿进程 |
| 646d98d→739d631 | ✅ | ✅ | ✅ | ✅ | **Phase 8 全绿**：审计上限 10k、健康计数器、token 轮换（CLI+API）；CI 抓出 adminAuth 读 Options 快照→轮换后旧 token 仍有效（改读 store 当前 sha，即时失效） |
| 85fac7f→360fe21 | ✅ | ✅ | ✅ | ✅ | **Phase 4+5 全绿**：deny-by-default 授权（G4 正/负对照）+ hs-admin 运维 CLI（e2e 的 invite/revoke 走 CLI）；CI 红轮战果：grants ip 需冒号语法 tcp:22、grant src 匹配节点实持身份（untagged ctrl 不进 peer map）、windows lockfile 包级 mutex 死锁（阻塞 Acquire 持 mutex 跨 LockFileEx）→ 删 mutex |
| 7a0f3af | ✅ | ✅ | ✅ | ✅ | e2e 假绿（管道吞退出码）——测试实际红 |
| e4deb55/82720c1 | ✅ | ✅ | ✅ | ✅/— | e2e 红（policy `*` / mkdir）——pipefail 后诚实红 |
| 128b5f4 | ✅ | ✅ | ✅ | ✅ | **hs e2e 全绿（G2/G4p/G5 硬门过）** |

## 4. 失败的测试 / 未决风险

- G1–G5 决策门全部未实证（Phase 1b 目标）。
- ADR-006 门控生效中：生产形态的 ssh_server 启用受门控约束。

## 5. 与参考实现的偏差（滚动记录）

| # | 偏差 | 依据 |
|---|---|---|
| D1 | 文档进 docs/hs/，不覆盖现行 docs/ | 保留现行实现（源规格 §16 自身要求） |
| D2/D3 | 新代码 hs/ 子树，二进制 hs-node/hs-controller/hs-admin | 零冲突 |
| D4 | ssh_server 门控启用 | ADR-006 |
| D5 | 控制端用官方 tailscale ≥1.104.4 | 公告要求 |
| D6 | 测试全跑 GH Actions | 用户纪律 |
| D7 | 初版单 DERP | 源规格允许单 VPS 起步 |
| D8 | 不造 Anchor Manager | ADR-004 |
| — | 开放开户口（bootstrap 18443）不做 | ADR-002（邀请码为登记唯一入口） |
| — | 迁移=重新登记（不能逐字节迁身份） | ADR-002（身份体系变更的本质结果） |

## Phase 13 — 架构分家（受控端 sing-box，其余上游原版）+ 安装包拆分 + CD（2026-11-20）

远端对齐：本地 main = origin/main = d377064（唯一远端新增 = LICENSE AGPL-3.0，他线推入，线性无冲突）。
校机确认无源码 repo（仅运行时目录 %LOCALAPPDATA%\Dialhome），不需建。

**ADR-007**（docs/hs/ADR/007-role-transport-assignment.md）四决定 + 一账：
- D1 受控节点 = sing-box（默认；约束解除即换回官方，如 Linux 节点）
- D2 管理机 = 官方 tailscale 客户端——**代码零改动**（admincli/ssh.go 本就按官方 CLI 设计，
  空 socket = 官方客户端默认端点）；测试期的 sing-box socks 测试架（.tmp/launch-ctrl.py）非产品形态
- D3 服务端中继 = 官方 derper v1.102.1（与 fork 基线同源，CI 构建，无官方发布件）
- D4 自签 IP 证书 + DERP map 指纹 pin——**推翻 a11d17f "only domain+443 TLS"**：
  v1.102.1 证据链 = derphttp_client.go:669（CertName sha256-raw → tlsdial 指纹校验）、
  net/tlsdial.go:312（IP 直连免主机名匹配）、cmd/derper/cert.go:116（manual+IP 自动自签
  ECDSA P-256 1 年期证书并打印 CertName 提示）。域名/LE/续期基建全部不需要
- D5 公开中继账（T-17）：derper 不验证客户端（headscale 无准入端点），限流旗标 + SG 闸门缓解

**deploy.sh v2**：derper systemd 单元（dialhome 用户、限流、ProtectSystem）→ 首启自动出证书 →
openssl 算 DER sha256 → 写 /etc/headscale/derp-dialhome.json（RegionID 900、STUNPort -1）→
headscale 配置权威重写（listen_addr 收紧 127.0.0.1、derp.urls=[]→paths=map 文件、备份旧稿）→
摘 18080 nc 遗留桥（校机隧道本来就是 -L 18080→8080 直连）。

**安装包**（scripts/package.sh，CI/CD 共用）：server（hs-controller+hs-deploycheck+derper+deploy.sh，
tar.gz）/ client（hs-node，win zip + linux tar.gz；sing-box 由 install-singbox 运行时取）/
admin（hs-admin，win zip + linux tar.gz）+ SHA256SUMS；原始二进制保留 hs-dist artifact。
新增 hs-admin windows 构建（此前只有 linux）。

**CI**：hs-build 改调 package.sh（产物 hs-dist + hs-packages 两 artifact）。
**CD**：release.yml 新增——tag push（v*）自动构建并 gh release create --generate-notes
（已存在则 --clobber 重传），workflow_dispatch 手动兜底。

待办：安全组 443 开放 → 139 重部署（derper 上线）→ 139 本机 selftest 验 pin 全链 →
两端重注册 → 端到端 tailscale-ssh。

## Phase 13b — 数据面打通（2026-11-21，真机证据）

SG 443 开放后全链验证（详见 docs/hs/ADR/007 Verification 节）：
1. 外网 TLSv1.3 + 证书指纹逐字匹配 pin（sha256-raw:8d6220c3…）
2. 校机 sing-box ↔ 139:443 连续 Established
3. 本机 SOCKS5 → 100.64.0.2:22 → SSH-2.0-sing-box banner
4. 官方客户端（139, vps-admin 100.64.0.7）netcheck = dialhome-vps 3ms，
   tailscale ssh YiZhidi@win11-hv1 在 Windows 校机执行成功

真机修出的两个新坑：
- 控制面单端口化：advertise 18080 / listen 8080 + nc 桥（单连接）→ 客户端注册
  回退同端口 https 永久卡死；改 headscale 直接 listen 18080，桥废除
  （deploy.sh + OPERATIONS §5 已改，校机/本机隧道 mapping 同步）
- 校机启动脚本残留 TS_DEBUG_USE_DERP_HTTP=1 → 明文打 TLS derper；已删并重启

清理：139 上自测遗留 tailscaled（tselftest）已删；宿主机 vps-admin 保留为管理身份。

## Phase 13c — 管理机在本机落地（2026-11-22 真机）

装官方 Tailscale 客户端（MSI → 服务模式，Automatic），登记为 local-admin-2（100.64.0.10）：
- `tailscale ping win11-hv1` → `pong via DERP(dialhome) 22ms`
- `tailscale ssh YiZhidi@win11-hv1` → `Win11-HV1 / 7.7.0` rc=0
- `hs-admin doctor` → `tailscale state: Running`；`hs-admin exec win11-hv1` → `Win11-HV1` rc=0
- 控制面隧道做成登录任务 `DialhomeControlTunnel`（18080/18800，断线自愈）

真机踩坑（已写进 ENROLLMENT §3 实装记录）：
1. 用户态 dev-mode 不可用于日常：后端随最后客户端断开而停；`tailscale ssh` 在自定义
   命名管道上 `The specified path is invalid`（1.104.7 Windows 缺陷）；AF_UNIX 不支持。
2. **从 S: 盘执行 hs-admin → 其子进程打管道 `Access is denied`；复制到 C: 后 rc=0。**
   （S: 是本地固定 NTFS，但该卷上执行会得到受限令牌；管理机二进制一律放本地 C: 盘。）
3. 本机还残留 `TS_DEBUG_USE_DERP_HTTP=1` 用户环境变量（早期实验），会令 DERP 走明文
   HTTP 打 443 而失败——已删除。

## Phase 13d — STUN 开启与直连结论（2026-11-22 真机）

- derper 一直在 `3478/udp` 跑 STUN（默认 `-stun=true`），但 DERP map 宣告 `stunport: -1`
  让客户端不用；已改为 `stunport: 3478`（SG 已放行 3478/udp）。本机 netcheck 变为
  `UDP: true`、公网映射 `183.36.189.192:46863`。
- **直连仍不可行**：peer `Endpoints=[]`、`CurAddr=[]`、`Relay=dialhome`——sing-box 的
  tailscale endpoint 不上报 UDP 端点，打洞没有候选地址。`tailscale ping` 恒走 DERP。
- 顺带修掉两个真机 bug：deploy.sh 的 18080 "遗留 nc 清理"在单端口化后会**误杀
  headscale 本体**（已加进程名判定，只杀 nc）；`hs-deploycheck` 默认探活端口
  8080 → 18080（源码+调用点都改了）。
