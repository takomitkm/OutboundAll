# ADR-005 — 服务转发：G1 裁决驱动；默认全关，绝不宣称逐服务等价

状态：Pending G1-完成（部分实证；Phase 6 收口）

## Context

参考实现通过 `-R 127.0.0.1:<port>` 把节点本机服务暴露到 relay 回环口，`permitlisten` 在**服务端**
逐口精确白名单——这是参考安全模型的核心强项。新栈的转发面由 sing-box ssh_server 承载，其
forwarding 是**全局开关**（`disable_forwarding: true/false`），无逐服务授权点（源规格 §9 自己已
指出这一点）。G1 问的是：userspace 模式下有没有别的原生机制能把 tailnet 入站流量引到任意回环服务。

## Decision（结构先定，内容待 G1）

1. **默认 `disable_forwarding: true`**——新栈初始安全姿态 = 参考实现的"节点 key 无 -L/-R"。
2. G1 结论二选一：
   - **G1-a（存在原生入站转发机制）**：评估其能否表达"目标地址+端口白名单"；能 → 服务定义
     （name/node/target/enabled/allowed_controllers）编译为该机制配置，服务端强制；不能表达白名单 → 落入 G1-b 处理。
   - **G1-b（只有全局 SSH forwarding）**：转发能力 = per-node profile 的显式布尔位（服务端=Headscale
     ACL 无法逐服务裁决 → **文档化偏差**：本版本不支持"逐服务服务端强制"；如需，后续 ADR 评估最小
     forwarding broker（节点侧用户级、只监听白名单目标）。绝不宣称 Headscale tag 能替代 permitlisten 语义。
3. 无论 a/b：**禁止暴露 Windows 回环管理服务**（如 445/3389/5985）——profile 模板带默认拒绝清单。
4. 任何"已实现转发"的文档声明必须对应一个 CI 断言；否则写"偏差"。

## G1 部分证据（run 128b5f4）

- tailnet 入站 TCP :22 → ssh_server 应答（G4p 全链走通）。
- tailnet 入站 TCP :8443（无监听服务）→ **连接被接受但无响应数据**：userspace endpoint 的网络栈接受任意端口的入站 TCP，然后无 handler 处理。即「入站到达」成立；「入站能否被转发到任意回环服务」取决于 sing-box 路由配置是否有面向 tailnet 入站的 inbound 机制——Phase 6 用配置实验收口（当前候选 G1-b：只有全局 SSH forwarding 开关）。

## Consequences

- 若 G1-b：与参考实现的功能差距**如实进入 FEATURE_TRACEABILITY F13/F14 ⚠️**，终版报告逐条列明。
- 转发开启的节点在 THREAT_MODEL T11/T15 中单独标注。
- G1 证据：CI run `待回填`。
