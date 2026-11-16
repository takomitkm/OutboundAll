# ADR-006 — SSH server 安全门控：内嵌 tailscale fork 低于公告阈值

状态：Accepted（门控生效中；解锁条件见下）

## Context（2026-11-17 实查，全部有一手出处）

1. Headscale v0.29.7 发布公告（2026-10-30，安全版）原文要求：
   "We recommend that all users upgrade to Headscale 0.29.7 **and Tailscale v1.104.4 or newer**
   ... **especially those using Tailscale SSH**. Tailscale v1.104.4 fixes vulnerabilities in
   **SSH forwarding, SSH session handling and Serve**. These fixes run in `tailscaled` on your
   nodes; upgrading Headscale alone does not apply them."
2. sing-box v1.14.4（2026-10-24，当前最新正式版）的 go.mod 钉定
   `github.com/sagernet/tailscale v1.102.1-sing-box-1.14-mod.5.0.20261019070639-79c1732442a9`
   —— fork tip = `79c173244`（2026-10-19），基线为上游 v1.102.1，**早于 1.104.4 修复**。
3. 本架构中 Windows 节点的 SSH server 正是 sing-box 内嵌 tailscale 协议栈的 `ssh_server`
   ——即公告点名的那条攻击面的宿主。

**结论**：依赖现状不能满足源规格 Phase 1 的要求（"If a dependency cannot satisfy the required
security properties, produce an ADR for a narrow replacement or upstream patch. Do not silently
bypass the limitation."）。

## Decision

1. **CI/测试环境**：ssh_server 全量启用（测试是发现问题的场，不因门控而减少覆盖）。
2. **生产默认姿态**（fork 未跟进期间；CI 全量启用并在 1.14.4 + headscale 0.29.7 组合下全链验证通过）：
   - `disable_forwarding: true`（公告点名的 SSH forwarding 漏洞面直接关闭）；
   - SSH ACL deny-by-default，仅 controller 身份可达（缩小暴露面）；
   - 生产启用 ssh_server 须满足其一：
     (a) sagernet/tailscale fork 跟进 ≥1.104.4 对应修复（sing-box 发布包含该修复的版本）；
     (b) 机主书面接受残余风险（记录在本 ADR 末尾"风险接受"节，注明日期与知晓的公告编号）。
3. **跟踪责任**：每次升级检查 sing-box release notes / fork commits，确认基线 ≥ 1.104.4 对应修复；
   检查方法记入 OPERATIONS.md。
4. 不采用"换官方 tailscaled"作为 Windows 规避方案——tailscaled 需要管理员/服务（违反零管理员
   不变式）；不采用"禁用 SSH server 换便携 sshd"——userspace 无 TUN 时 tailnet 入站无法到达
   任意回环服务（G1 待证；若 G1 证明可行，本 ADR 重开评估）。

## Consequences

- CI 证据覆盖的测试全跑在启用 ssh_server 的形态上（诚实：测的即交付的）。
- 生产启用被门控：Phase 11 验收矩阵中"生产形态"一栏区分 test profile / production profile。
- 本 ADR 是**活文档**：fork 更新后，(a) 生效 → 门控解除，记解除日期与 sing-box 版本号。

## 风险接受（如机主选择路径 (b)，在此记录）

```
日期：
决策人：
已知公告：Headscale v0.29.7 release notes（Tailscale SSH 相关漏洞，详见上文引用）
接受范围：
```
