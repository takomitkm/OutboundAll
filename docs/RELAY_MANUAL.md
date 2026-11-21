# RELAY_MANUAL — Relay 部署与运维手册

> 适用对象：要在生产 Relay 上**搭建、签发凭证、日常运维、排障**的人。
> 配套文档：[`MIGRATION_GUIDE.md`](./MIGRATION_GUIDE.md)（旧版→新版迁移）、
> [`SECURITY_MODEL.md`](./SECURITY_MODEL.md)（安全不变量与负向矩阵）、
> [`PROTOCOL.md`](./PROTOCOL.md)（登记/运行协议）、
> [`PORTS.md`](./PORTS.md)（端口模型）、
> [`CONFIG_SCHEMA.md`](./CONFIG_SCHEMA.md)（registry.json 字段）、
> [`PHASE4_ACCEPTANCE.md`](./PHASE4_ACCEPTANCE.md)（真 SSH 验收证据）。

---

## 0. 两条必须记牢的事实

1. **Relay 数据目录恒定是 `~/.OutboundAll`。**
   `relay` / `relay-helper` 子命令运行时**固定读这里**；它不继承 shell 的
   `OUTBOUNDALL_DATA_DIR`（helper 在 sshd 子进程里跑，环境与你登录 shell 不同，
   一个不匹配会让签发出来的凭证被当成 "unknown credential"）。
   只有 `--base`（relay-helper 上）和极少量测试逃逸口会覆盖它——**正常运维不要碰**。
   内部结构：`~/.OutboundAll/registry.json`（节点与凭证状态）、
   `~/.OutboundAll/registry.lock`（写串行化锁）、`~/.OutboundAll/backups/`（init 时创建）。

2. **macOS 平台全部行为 UNVERIFIED。** 本轮无 macOS 真机验证；下列命令在 Linux/Windows
   上实测，macOS 上若出问题请先在真机补验，不要直接上生产。

---

## 1. 拓扑与端口

```
                        校园网 / 公网
                              │  只有这一个对外端口（路由器已映射）
                              ▼
                  10.101.54.192:18322  ← relay sshd（18322 是路由器转发口）
                              │
          ┌───────────────────┼───────────────────┐
          │   127.0.0.1:13437  │   127.0.0.1:13435  │  ← 每节点一对，仅绑回环
          │   127.0.0.1:13441  │   127.0.0.1:13440  │     外部扫不到
          └───────────────────┴───────────────────┘
          节点 A (win2/port-13435)      节点 B (n-10-110-23-66-2b12)
```

- **入口**：relay 的 sshd 监听 `:18322`（公网口），或同机部署时就是本机 `:22`。
- **每节点一对回环端口**：`(app=偶, ssh=奇)`，例如 `(13440, 13441)`、`(13435, 13437)`。
  路由器**不需要**知道这些端口；从校园网侧扫描不到任何节点。
- **端口池**：`13440–13600`（共 80 对）。分配器步进 2，取池内第一个空闲对。
  分配时合并「registry 内所有 active/pending 节点的端口」∪「`ss -ltn` 当前监听端口」，
  避免与正在运行的隧道冲突。
- **advertise 地址语义（节点该拨哪个地址去登记/保活）**：
  - 默认 `10.101.54.192:18322`（公网口）——**跨网络部署**用这个。
  - **同机部署必须设 `127.0.0.1:22`**：relay 本机的 sshd 实际监听 22，
    `18322` 只是路由器转发口；若节点和 relay 在同一台机器却用 `18322`，
    节点会去拨一个外部转发口而不是本机 sshd。

> 关于凭证里 `relay_port` 的推导（重要陷阱，见 §7.4）。

---

## 2. 一次性布置

### 2.1 前置
- 一个普通用户账号（不要 root 部署二进制，否则 `~/.OutboundAll` 属主会错；若误用 root，
  记得 `chown -R user:user ~/.OutboundAll`）。
- 该用户的 `~/.ssh/authorized_keys` **必须存在**（哪怕是空文件）。`relay init` 会检查它，
  缺失则报错 `system authorized_keys missing at ... (create it first)`。
- relay sshd 已能让你用**管理公钥**（controller / 你的登录 key）登录——本工具不改系统 ssh 配置。

### 2.2 初始化
```bash
# 跨网络部署（默认公网口）
OutboundAll relay init

# 同机部署（relay 与节点同在 102 本机）
OutboundAll relay init --advertise HOST:PORT [--relay-user USER] 127.0.0.1:22
```
`relay init` 做的事：
1. 创建 `~/.OutboundAll`（0700）与 `~/.OutboundAll/backups/`。
2. 把 registry 的 `helper_path` 写成**当前 OutboundAll 二进制绝对路径**
   （这个路径会被烤进每一条 authorized_keys 行，所以挪动二进制后旧行会失效——见 §6.5）。
3. 若给了 `--advertise <HOST:PORT>`，写入 `registry.advertise`；否则留空，
   签发凭证时回落到默认公网地址（§1）。

初始化成功后终端会打印：
```
relay init ok: /home/user/.OutboundAll
managing system authorized_keys: /home/user/.ssh/authorized_keys
```

---

## 3. 凭证签发流程

节点上线前，要在 relay 上给它签一张**一次性登记凭证**（enrollment credential）。

### 3.1 签发
```bash
# 标准：签发名为 <name> 的节点凭证，并把 controller 管理公钥烤进去
OutboundAll relay credentials issue <name> --manager-pub /path/to/manager.pub

# 别名（不含 manager-pub；会从 ~/.OutboundAll/manager.pub 读，没有则留空）
OutboundAll relay enroll <name>
```
- `<name>`：节点名（会写进 `OutboundAll-node-<name>` 标记）。生产现有节点形如
  `n-10-110-23-66-2b12`、`port-13435`（后者来自旧 `magpie-tunnel-node-13435`）。
- `--manager-pub FILE`：controller 管理公钥行（ssh-ed25519 …）。不传时尝试读
  `~/.OutboundAll/manager.pub`；仍然没有则凭证里 `manager_pubkey` 为空。
- **幂等**：同名节点复用原端口对，不重新分配；新凭证覆盖旧 `credential_id`，
  若节点之前是空态/已撤销则置为 `pending`。

签发后终端输出两样东西：
```
CREDENTIAL-B64 = <base64(JSON)>
feed it to: OutboundAll node install --credential <that b64 or file>
```

### 3.2 凭证 JSON 里烤了什么
`CREDENTIAL-B64` 解码后是：
```json
{
  "credential_version": 1,
  "node_name": "<name>",
  "private_key_openssh_b64": "<凭证私钥，OpenSSH 格式>",
  "manager_pubkey": "ssh-ed25519 AAAA... controller 或空",
  "ports_ssh": 13441,
  "ports_app": 13440,
  "relay_address": "10.101.54.192",
  "relay_port": 18322,
  "relay_user": "user",
  "expires_at": "2026-11-16T09:00:00Z"
}
```
- 私钥**只在这一次输出里出现**，节点侧安装消费后即删除；请通过安全通道交给节点操作者。
- `expires_at`：签发后 **+24 小时**自动过期；过期后 `enroll` 被 helper 拒绝（N9）。
- `relay_address` / `relay_port`：由 registry 的 advertise 推导（见 §7.4）。

### 3.3 节点侧消费（一句话）
节点操作者拿到 `CREDENTIAL-B64` 后跑：
```bash
OutboundAll node install --credential <文件或 @b64>
```
节点本地生成自己的 tunnel key（私钥不出机），用凭证私钥去 relay `enroll` 提交公钥，
helper 校验凭证有效+未过期+未使用 → 把节点公钥写入 authorized_keys 受限行、
registry 该节点 `state: pending→active`、凭证报废（一次性）。详细见节点手册。

### 3.4 撤销一张未用的凭证
```bash
OutboundAll relay credentials revoke <cred-id>
```
只删该凭证的 registry 记录与 authorized_keys 里的 `OutboundAll-credential-<id>` 行；
不影响已登记节点。

---

## 4. 日常运维

| 命令 | 作用 | 关键行为 |
|---|---|---|
| `OutboundAll relay list` | 列出所有节点 | 列 `NODE / STATE / APP / SSH / TUNNEL-FP / HW` |
| `OutboundAll relay inspect <name>` | 看单节点完整记录 | 打印该节点 `NodeRecord` 的 JSON（含 state/ports/hardware/时间戳） |
| `OutboundAll relay ports` | 列出当前占用端口 | 输出所有 active/pending 节点的 ssh+app 端口（已排序） |
| `OutboundAll relay revoke <name>` | 撤销节点（软删） | 置 `state=revoked` 留**墓碑**（审计用），删 AK 行；端口标记为释放 |
| `OutboundAll relay purge <name>` | 彻底删除墓碑 | **仅接受 `revoked` 态**；删 registry 记录 + 关联凭证；拒绝 active/pending |
| `OutboundAll relay credentials issue/revoke` | 凭证签发/撤销 | 见 §3 |
| `OutboundAll relay migrate-legacy [LEGACY-DIR]` | 旧版导入 | 见 [`MIGRATION_GUIDE.md`](./MIGRATION_GUIDE.md) |

### 4.1 revoke vs purge（别搞混）
- **`revoke` = 让节点立刻失能但保留记录**：AK 行被删（旧 key 重连 → Permission denied，N10），
  registry 里 `state=revoked` 的**墓碑**留着，端口不再计入分配。运行中的隧道监听由
  sshd 重签或超时兜底释放。
- **`purge` = 物理删除**：只有 `state=revoked` 的节点能被 purge；active/pending 节点会被
  **拒绝**（`refusing to purge node ... in state "active"`），防止手滑把线上节点端口放给别人。
  生产实测现有 2 个 active 节点（`n-10-110-23-66-2b12`、`port-13435`），purge 它们会直接报错。

### 4.2 当前生产 registry 实测状态（2026-11-15）
```
n-10-110-23-66-2b12   active   app=13440 ssh=13441
port-13435            active   app=13435 ssh=13437
credentials: 空
```
这是 `relay migrate-legacy` 从旧体系导入后的结果（见迁移手册），凭证为空说明全是迁移节点、
没有待消费的登记凭证。

---

## 5. AK 文件规范与安全不变量

authorized_keys 是安全核心。**本工具直接维护系统的 `~/.ssh/authorized_keys`**
（OpenSSH 的 authorized_keys **不支持 Include 指令**——Include 属于 sshd_config）。

### 5.1 两行模板（字面量，`127.0.0.1` 不可替换成 localhost/0.0.0.0）
```
# 节点隧道行（节点 key 的 forced command）
restrict,command="<helper> relay-helper --port <SSH_PORT> tunnel",port-forwarding,permitlisten="127.0.0.1:<SSH_PORT>",permitlisten="127.0.0.1:<APP_PORT>",permitopen="127.0.0.1:1" ssh-ed25519 <b64> OutboundAll-node-<name>

# 登记凭证行（一次性凭证的 forced command）
restrict,command="<helper> relay-helper --credential <id> enroll",no-port-forwarding,no-pty,no-user-rc <b64> OutboundAll-credential-<id>
```
- `<helper>` = registry 里烤进去的 OutboundAll 二进制绝对路径。
- **端口标识烘焙进调用者自己的行**：`--port <SSH_PORT>` / `--credential <id>`。
  该行由 relay 自己写，客户端无法冒认他人槽位。
- 节点行能力上限：仅 `-R 127.0.0.1:<自己两个口>`；无 shell、无文件读、无 `-L`
  （`permitopen="127.0.0.1:1"` 拒一切本地转发）、不能占他人端口
  （`permitlisten` 精确匹配）。
- 凭证行能力：仅 `enroll` 原语；`no-port-forwarding,no-pty,no-user-rc`。

### 5.2 强制命令的旗标顺序（写过 Go flag 才踩得到的坑）
`relay-helper` 的 forced command 必须是 **「旗标在前、位置动词在后」**：
`relay-helper --port <P> tunnel` / `relay-helper --credential <id> enroll`。
Go 的 `flag` 包遇到第一个位置参数就停止解析；写成 `tunnel --port <P>` 会导致
`--port` 根本没被解析。模板已固定为正确顺序，手写或改脚本时务必照此。

### 5.3 结构化解析：外来行逐字节保留
解析器把每行分成 `node` / `credential` / `other` 三类：
- `OutboundAll-node-<name>`、`OutboundAll-credential-<id>` 标记的行被识别；
- 其它一切（人工 key、注释、旧 `magpie-*` 行）**原样保留**，渲染无损；
- 只改目标节点的那一行；单节点撤销绝不碰其它行；
- 解析失败 → **中止并保留原文件**，绝不整文件清空。

### 5.4 写规程（原子 + 备份 + 锁）
每次写 authorized_keys：
1. 全程持 `~/.OutboundAll/registry.lock`（unix `flock` / Windows `LockFileEx`）串行化所有写者；
2. 读原文件 → 结构化解析 → 改目标行 → **原子写**（同目录 tmp + rename）→ 保留
   `~/.ssh/authorized_keys.bak-<时间戳>` 备份；
3. 文件权限硬化为 `0600` 等价。

### 5.5 节点名规则
节点名用于 `OutboundAll-node-<name>` 标记，且要能被正则稳定匹配（迁移时
`magpie-node-([a-z0-9-]+)$` 这类规则依赖它只含小写字母/数字/连字符）。
生产节点名如 `n-10-110-23-66-2b12`、`port-13435` 都符合；起名请保持 `[a-z0-9-]`。

---

## 6. 备份与回滚

### 6.1 备份在哪
- 每次 authorized_keys 写操作前，原文件被复制为 `~/.ssh/authorized_keys.bak-<YYYYMMDD-HHMMSS>`
  （与 authorized_keys 同目录，带时间戳）。
- registry 自身也是同目录 tmp+rename 原子写；`~/.OutboundAll/backups/` 由 init 创建。

### 6.2 回滚 authorized_keys
如果一次改写搞错了：
```bash
# 1) 确认备份
ls -1 ~/.ssh/authorized_keys.bak-*
# 2) 用最近一份覆盖回去（注意权限）
cp ~/.ssh/authorized_keys.bak-20261115-120000 ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```
因为外来行是逐字节保留的，回滚只影响 OutboundAll 自己写的行，不会动你的其它 key。

### 6.3 registry 回滚
registry 没有内建的时间戳备份；重大操作前**手动 cp 一份** `registry.json`：
```bash
cp ~/.OutboundAll/registry.json ~/.OutboundAll/registry.json.bak-$(date +%Y%m%d-%H%M%S)
```
`revoke` 用墓碑保留了记录，多数情况不需要回滚 registry——直接 `purge` 清理即可。

---

## 7. 故障排查

### 7.1 `relay init` 报 authorized_keys missing
先 `touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys` 再 init。

### 7.2 凭证 enrol 报 "unknown credential"
最常见原因：**relay 数据目录不一致**。relay/relay-helper 固定读 `~/.OutboundAll`，
不继承你 shell 的 `OUTBOUNDALL_DATA_DIR`。排查：
- 签发凭证和节点 enrol 是不是用的同一份 `~/.OutboundAll/registry.json`？
- 有没有在 `~/.OutboundAll` 之外（比如用 `--base` 或 `OUTBOUNDALL_RELAY_DIR`）跑过命令？
  正常运维别用这些覆盖；它们只用于测试。

### 7.3 节点 key 连不上 / 旧 key 还能用吗
- `revoke` 之后旧 key 立即失效（AK 行被删，重连 Permission denied，N10）。
- 未 revoke 的节点 key 一直有效，直到你主动 revoke。

### 7.4 relay_port 推导陷阱（务必读）
凭证 JSON 里的 `relay_address` / `relay_port` **不是简单回显 advertise 写的端口**，
而是在签发时按地址推导：
- 地址是回环（`127.0.0.1`，含 `127.0.0.1:22`）→ `relay_port = 22`；
- 其它地址 → `relay_port = 18322`。

后果：
- 同机部署必须显式 `relay init --advertise 127.0.0.1:22`，才能得到 `relay_port=22`。
- **不要把 advertise 写成 `host:9999` 指望凭证里 `relay_port=9999`**——端口部分会被忽略，
  仍按上面规则推导。若你的 relay sshd 监听非 22/18322 的自定义端口，请用节点侧的
  `--relay USER@HOST:PORT` 显式指定，而不是依赖 advertise 的端口段。

### 7.5 OpenSSH 10 per-source penalty（节奏坑）
认证失败的**来源 IP** 会被 OpenSSH 10 惩罚性重置，journal 里出现
`penalty: failed authentication`。影响：
- **负向测试要放慢节奏**，别用脚本高频轰同一个失败身份；
- **supervisor 重试遇 auth 失败必须重度退避**（直接跳到小时级），不能按网络失败节奏重试，
  否则会被持续惩罚、雪上加霜。

### 7.6 helper 路径失效
`init` 时把当时 OutboundAll 二进制的绝对路径烤进了 AK 行。若你**移动或重命名了二进制**，
旧 AK 行里的 `<helper>` 路径就指不到文件，节点 enrol/tunnel 会失败。
修法：重跑签发/迁移让 AK 行刷新，或确保二进制路径不变。

---

## 8. 负向验证清单（部署后自测）

以下断言全部来自真实 SSH 会话（[`SECURITY_MODEL.md` §6](./SECURITY_MODEL.md) 与
[`PHASE4_ACCEPTANCE.md`](./PHASE4_ACCEPTANCE.md)，2026-11-14 relay 102 真机全绿）。
验收门要求**实际通过 SSH 连接验证**，不能只测字符串配置。

| # | 断言 | 怎么验 |
|---|---|---|
| N1 | tunnel key 无法登 relay shell | `ssh -i tunnel.key relay 'id'` → `DENY: unknown request` |
| N2 | tunnel key 无法读 relay 文件 | 同上，forced command 无文件能力 |
| N3 | tunnel key 无法 `-L` 进 LAN | `ssh -L 15999:192.168.x:80 ...` → `Could not request local forwarding.` |
| N4 | tunnel key 不能执行任意命令 | 同 N1 |
| N5 | tunnel key 不能占他人端口 | `-R 127.0.0.1:<他人端口>` → `remote port forwarding failed` |
| N6 | 不能绑 `0.0.0.0` | `-R 0.0.0.0:<port>` → 拒 |
| N7 | 只能绑自己的两个口 | 非 permitlisten 白名单端口 → 拒 |
| N8 | manager key 不用于隧道 | manager 公钥只进 Node AK，不在 relay AK |
| N9 | 过期/已用凭证不能再登记 | 二次 `enroll` → Permission denied |
| N10 | 撤销后旧 key 失效 | `revoke` 后重连 → Permission denied |

> 节奏提示：N3–N7 涉及失败认证，按 §7.5 放慢；每条之间留几秒间隔，避免触发 per-source penalty。

---

## 9. 相关命令速查（来自 usage）

```
OutboundAll relay init [--advertise HOST:PORT] | list | inspect <name> | revoke <name> | purge <name> | ports
OutboundAll relay credentials issue <name> [--manager-pub FILE] | revoke <id>
OutboundAll relay migrate-legacy [LEGACY-DIR]
OutboundAll relay-helper --port <p> tunnel | --credential <id> enroll   (内部强制命令)
OutboundAll migrate --from-legacy [--legacy-dir DIR]
```

需要把旧版机器迁过来？看 [`MIGRATION_GUIDE.md`](./MIGRATION_GUIDE.md)。

## 附：2026-11-15 代码审评修复（P1/P2）

- **P1 撤销凭证漏洞**：`relay revoke <name>` 现在会同时删除该节点**全部**登记凭证记录、对应 `authorized_keys` 凭证行，并清空墓碑上的 `CredentialID`。`relay-helper` 的 `EnrollCommand` 另加两道检查：节点处于 `revoked` 直接拒绝；凭证 ID 不等于节点当前 `CredentialID` 也拒绝。已在 102 上用**真实 SSH**（临时 rootless sshd + 旧凭证私钥 + replay 的 AK 行）跑通两条负向路径：`DENY: unknown credential` 与 `DENY: victim is revoked`，节点始终保持 revoked。
- **Forced command 路径转义**：helper 路径分别做 POSIX shell 单引号引用与 authorized_keys 选项值转义（`ShellQuote` / `AKOptionValue`），支持带空格、引号、`$()`、反引号的路径；CR/LF/NUL 直接拒绝。
- **端口分配**：`livePorts` 现识别 `0.0.0.0:` / `[::]:` / `*:` 等所有通配与 IPv6 监听，不再误分配已占用端口。
- **purge**：`relay purge <name>` 删除凭证记录时同步删除其 AK 行，避免 sshd 里留下失效公钥。
- **部署参数**：新增 `relay init --relay-user USER`（写入 registry 的 `advertise_user`）；凭证里的 `relay_user` 按 配置 → `$USER` → `$USERNAME` → `user` 回退，不再写死。测试可用 `OUTBOUNDALL_RELAY_DIR` / `OUTBOUNDALL_RELAY_AK` 把 relay 数据目录与 AK 重定向到沙箱。
- **构建可复现**：CI 的 `setup-go` 与 `go.mod` 统一为 Go 1.27，并设 `GOTOOLCHAIN: local`。
