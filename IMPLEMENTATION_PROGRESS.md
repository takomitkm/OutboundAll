# IMPLEMENTATION_PROGRESS — dialhome × Headscale 重实现进度台账

> 源规格 §16 要求持续更新。每步（commit+push）后更新本文件。
> 规格正本：PLAN.md；参考语义：analysis/REFERENCE_INVENTORY.md；特性映射：analysis/FEATURE_TRACEABILITY.md。

## 当前状态

- **已完成**：Phase 0/1/1b（架构冻结 + 决策门实证）/ **Phase 2**（hs-node 运行时：config 不变式、布局、sing-box 钉定下载、进程 supervisor、自启动、单实例锁；单元测试双 OS 绿）。
- **当前阶段**：Phase 8（控制面收口：SQLite 语义下的 JSON store 审计、API 鉴权已有；转入部署产物）。
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
