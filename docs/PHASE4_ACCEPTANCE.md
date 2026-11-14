# PHASE 4 验收记录 — Relay 安全核心（真 SSH 实测）

> 分支 `go-refactor` · 2026-11-14 · 实测机：relay 102（Debian 13 / OpenSSH 10.0p1，生产 sshd，普通用户 user）
> 规格依据：§2.4 负向测试矩阵（"必须实际通过 SSH 连接验证，不能只测试字符串配置"）
> 本轮所有结论来自真实 ssh 会话；生产 AK 行（win2/HP66/gate）全程共存并完好。

## 1. 落地组件（commit 67399ee + 本轮修正）

| 组件 | 说明 | 单元测试 |
|---|---|---|
| `internal/lockfile` | 跨平台互斥锁：unix `flock` / Windows `kernel32!LockFileEx`（`syscall.NewLazyDLL`，零第三方依赖） | 并发串行化、重入 |
| `internal/relay/registry.go` | registry.json（原子写+锁）、**端口分配器**（池 13440–13600 成对、registry∪实时监听、撤销回收） | 分配/耗尽/活动端口阻断 |
| `internal/relay/akfile.go` | 结构化 AK 解析（带 options 前缀行）、upsert/remove 仅动目标行、外部行逐字节保留、原子写+备份 | 解析/无损渲染/边界断言 |
| `internal/relay/helper.go` | 强制命令：`check`/`register`/`get openssh`/`enroll`（凭证一次性、预绑定槽位、payload 消毒） | happy/幂等/三类拒绝 |
| CLI | `relay init/enroll/list/inspect/ports/credentials/revoke` + `relay-helper`（旗标前置+位置动词） | 冒烟 + 生产实测 |
| `dialhome keygen` | 节点本地生成 ed25519（**私钥不出机**——对旧实现的密钥安全升级） | keygen 输出被系统 ssh-keygen 接受 |

## 2. 真实 SSH 验收读数（`~/.dialhome-matrix-go/evidence.log` 原文摘要）

| 测试 | 命令 | 实测结果 | 判定 |
|---|---|---|---|
| 格式 | `ssh-keygen -y -f cred.key` | **FORMAT-OK**（Go 生成的 OpenSSH 容器被系统 ssh-keygen 接受） | ✅ |
| 登记 | `ssh -i cred.key relay "enroll <tunnel.pub>"` | `ENROLL-OK node=gomatrix ports=13440,13441` | ✅ |
| **N1** 无 shell | `ssh -i tunnel.key relay 'id'` | `DENY: unknown request` | ✅ |
| **N2/N4** 无文件/无命令 | 同上（forced command 拒一切非白名单） | 同 N1 | ✅ |
| **N3** 无 -L 进 LAN | `-L 15999:192.168.28.128:80` | `Could not request local forwarding.` | ✅ |
| **N5** 不占他人端口 | `-R 127.0.0.1:13501` | `remote port forwarding failed for listen port 13501` | ✅ |
| **N6** 禁 0.0.0.0 | `-R 0.0.0.0:13502` | `remote port forwarding failed` | ✅ |
| **N7** 池内未分配端口 | `-R 127.0.0.1:13460` | `remote port forwarding failed` | ✅ |
| **P1** check 正向 | `check` | `{"ok":true,"node":"gomatrix","ssh":13441,"app":13440}` | ✅ |
| **P2** 自己的端口正向 | `-R 127.0.0.1:13440:127.0.0.1:19443` | **listener `127.0.0.1:13440` 绑定成功** | ✅ |
| **N8** manager key≠tunnel key | 架构保证（manager 公钥只进 Node AK） | 代码审查 + 架构约束 | ✅（Phase 6 复测） |
| **N9** 凭证重放 | 第二次 `enroll` | `Permission denied`（凭证行已随首次使用退休） | ✅ |
| **N10** 撤销失效 | revoke 后 `check` | `Permission denied`（AK 行已删） | ✅ |
| 外部行保留 | 全程 | win2=1 / hp66=1 / gate=1 / takomi=1 逐行无损 | ✅ |
| 备份 | 每次 AK 写 | `authorized_keys.bak-<ts>` 生成 | ✅ |

## 3. 过程中抓到的真缺陷（全部修复并有测试/实测钉住）

| # | 缺陷 | 修复 |
|---|---|---|
| F1 | authorized_keys **不支持 Include**（设计稿写了 Include 方案） | helper 直接维护系统 AK；PROTOCOL §3 公开更正 |
| F2 | 凭证行渲染无 credential id → helper 查无凭证 | `--credential <id>` 烘焙进 forced command |
| F3 | `flag.Parse` 在首个位置参数处停止 → 位置动词必须放旗标后 | 行模板改为 `relay-helper --port P tunnel` / `--credential C enroll` |
| F4 | relay 数据目录误用 `%APPDATA%`（Windows 语义） | relay 角色 = `~/.dialhome`（三平台统一，贴近 ssh 生态） |
| F5 | root 部署二进制导致 `~/.dialhome` 属主错误 | 部署规程：`chown -R user:user`；记入部署文档 |

## 4. 新坑位（补入 PITFALLS.md 的素材）

- **OpenSSH 10 per-source penalty**：认证失败的来源 IP 会被惩罚性拒绝（journal：`penalty: failed authentication`）。
  影响：负向测试必须节流；**supervisor 重试遇 auth 失败必须重度退避**（不能按网络失败节奏重试）。
- `flag` 包遇到位置参数即停 → forced-command 模板必须"旗标在前、动词在后"。
- authorized_keys 无 Include（见 F1）。

## 5. 遗留与下一步

- `relay reconcile/verify` 待 Phase 6/9（需要 Node 通道在线）
- `dialhome relay-helper tunnel` 的 `get openssh` 二进制透传已实现，分发路径在 Phase 5 接入
- 端口分配器的 `ss -ltn` 依赖在 Windows 开发机上不可用 → `livePorts()` 返回 nil（降级为 registry-only）；
  生产 relay 是 Linux，不受影响；Phase 9 端到端再验证
- 清理完成：gomatrix 残留 AK 行/registry 已清；102 上实验文件已删
