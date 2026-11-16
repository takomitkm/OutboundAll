# IMPLEMENTATION_PROGRESS — dialhome × Headscale 重实现进度台账

> 源规格 §16 要求持续更新。每步（commit+push）后更新本文件。
> 规格正本：PLAN.md；参考语义：analysis/REFERENCE_INVENTORY.md；特性映射：analysis/FEATURE_TRACEABILITY.md。

## 当前状态

- **已完成**：Phase 0（全量调研 + 依赖核实）→ PLAN.md 终版 → analysis/ 两份产出 → docs/hs/ 架构/安全/威胁 + ADR 001–006。
- **当前阶段**：Phase 1 收尾 → 下一步 Phase 1b（CI 集成骨架，G1–G5 实证）。
- **在跑 CI**：见 §3 表。

## 1. 已完成工作

| 日期 | commit | 内容 |
|---|---|---|
| 2026-11-17 | 0b7be19 | PLAN.md（终版执行计划：调研结论、D1–D8 偏差、G1–G5 决策门、Step 0–14、测试设计） |
| 2026-11-17 | e0388c1 | CI 修复 #1：Windows runner `core.autocrlf=true` → checkout CRLF → gofmt 全红。9 次历史 run 全败的根因。修复后 windows vet+test 首次绿 |
| 2026-11-17 | df89ab2 | CI 修复 #2：smoke-linux 首次真正运行即暴露 `$?` 被 echo 消费的断言 bug（产品 exit=2 正确，断言却测 echo 的 0）→ rc 变量修复。同 commit：Phase 0 两份分析文档 |
| 2026-11-17 | （本 commit） | Phase 1：docs/hs/ARCHITECTURE.md + SECURITY_MODEL.md + THREAT_MODEL.md + ADR 001–006 + 本台账 |

## 2. 关键事实与决策（防止后面忘）

1. **依赖缺口**：sing-box v1.14.4 内嵌 sagernet/tailscale fork = v1.102.1-mod.5（tip 79c173244，2026-10-19）< Headscale 公告要求的 1.104.4（SSH forwarding/session/Serve 漏洞修复）。→ ADR-006 门控：CI 全开、生产默认 disable_forwarding+deny-by-default、解锁条件 (a) fork 跟进或 (b) 机主书面接受。
2. **版本钉**：Headscale v0.29.7 / sing-box v1.14.4 / 官方客户端 ≥1.104.4（当前 1.104.7）/ Go 1.27。
3. **偏差 D1–D8**（PLAN.md §1）：新代码全在 `hs/` 子树；新文档在 `docs/hs/`；现行 Go 版原样保留（其 CI job 必须持续绿——这是验收标准之一）。
4. **CI 历史**：本项目 windows job 自诞生从未绿过（0/9），两个潜伏 bug（CRLF、$? 消费）已修——诚实更正：此前文档宣称的"windows 双跑者全绿"从未成立。
5. **测试纪律**：全部测试跑 GitHub Actions（不跑本机）；每条用例绑定失效模式；负向用例红绿校准证据进 analysis/CALIBRATION.md；机器可读结果（go test -json）。

## 3. CI 记录

| commit | ubuntu vet+test | windows vet+test | smoke | build-matrix | 备注 |
|---|---|---|---|---|---|
| 0b7be19 | ✅ | ❌ | ⏭ | ⏭ | CRLF 根因（历史遗留） |
| e0388c1 | ✅ | ✅ | ❌ | 🔄 | smoke 首跑暴露 $? bug |
| df89ab2 | ✅ | ✅ | ✅ | ✅ | **全绿（本项目 CI 首次全绿）** |

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
