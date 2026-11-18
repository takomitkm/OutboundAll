# ENROLLMENT — dialhome-hs 登记指南（密码less，一次性）

> 流程语义继承参考实现（凭证一次性、短期、崩溃安全）；载体换为邀请码 + preauth key。

## 1. 三身份（ADR-002）

| 身份 | 生成/存放 | 用途 |
|---|---|---|
| 邀请码 | hs-admin 产出（256-bit 随机，服务端只存 sha256） | 人类携带，换登记包 |
| 节点身份 | 节点本机生成（tailscale node/machine key），存 `state_directory`，私钥不出机 | tailnet 连接 |
| node secret | redeem 时签发，节点 0600 落盘 | 每次启动 resume 换新 preauth key |

分离不变式：邀请码 ≠ preauth key ≠ node secret；三者能力集互不包含。

## 2. 登记流程

```text
运维:  hs-admin invite create --label win-lab [--profile default|no-ssh] [--ttl-hours H]
        → 一次性邀请码（仅显示一次；服务端只存哈希）
节点:  hs-node enroll --code <CODE> --controller https://mc.example.net [--hostname NAME]
        1. HTTPS 兑换（原子、限速 6/min/IP）
        2. 本地生成节点身份 → state 目录
        3. 拿到单用 preauth key（1h 有效）→ sing-box 注册
        4. node secret 0600 落盘（config 目录）
运行:  hs-node run
        1. 用 node secret 经 /nodes/resume 换新 preauth key（每次启动）
        2. sing-box 以新 key 注册 → 控制面记录 pending→active
```

## 3. 崩溃安全

redeem 与注册间崩溃：邀请进入 `redeemed(未完成)` 态，同 enroll 会话可续传，超时作废
——不留歧义重复节点（N5'：并发兑换恰一路成功，CI 实证）。

## 4. 撤销

```bash
hs-admin node revoke <id>     # headscale expire + ACL 移除 + 墓碑 + 名下邀请作废
hs-admin invite revoke <id>   # 只作废未消费邀请
```
撤销后：新连接拒绝（N6'）、resume 403（N7'）、已建会话按 G3 观察如实记录
（expire 不即时杀会话——与参考实现同口径）。

## 5. 服务暴露（Phase 6）

节点 config.json 的 `services: [{name, target "127.0.0.1:PORT"}]` 声明回环服务；
headscale grant（tcp:P）+ 节点 route allowlist 双闸；未列端口 reject-all。
SSH 转发保持关闭（`disable_forwarding: true` 结构性）。
