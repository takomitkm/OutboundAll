# MIGRATION_GUIDE — 旧版 → 新版（Go）Relay 迁移手册

> 适用对象：要把**已在跑的旧版 PS/Python 体系**（magpie 系列）迁到 Go 重构版 relay 的人。
> 部署/运维命令见 [`RELAY_MANUAL.md`](./RELAY_MANUAL.md)；安全模型见 [`SECURITY_MODEL.md`](./SECURITY_MODEL.md)。
>
> **本轮所有迁移结论来自 2026-11-15 生产 relay 102 实测（Phase 8）**，凭证为空、
> 两个 active 迁移节点 `n-10-110-23-66-2b12 (13440/13441)`、`port-13435 (13437/13435)`，
> 旧 win2 私钥经新 Go helper 仍能回答 `{"ok":true,"node":"port-13435",...}`，**生产隧道零中断**。

---

## 1. 旧版与新版的差异对照表

| 维度 | 旧版（magpie 系列） | 新版（Go relay） |
|---|---|---|
| 节点 AK 行标记 | `magpie-node-<name>` / `magpie-tunnel-node-<port>` | `dialhome-node-<name>` |
| 强制命令 | 指向 `magpie-tunnel-check` 等脚本 | `relay-helper --port <P> tunnel` / `relay-helper --credential <id> enroll` |
| 节点状态存放 | `~/.magpie-connector/machines.json` | `~/.dialhome/registry.json` |
| 硬件事实（uuid/macs/user） | 散在 machines.json | `registry.json[nodes].hardware` |
| 端口 | 13440–13600，legacy 行 `permitlisten` 顺序为 `(app, ssh)` | 同池；新行顺序 `(ssh, app)`（迁移**保留**旧顺序） |
| authorized_keys 维护 | 手工/脚本维护 | helper 结构化解析 + 原子写 + 备份 + 锁（见 RELAY_MANUAL §5） |
| 登记凭证 | 登记私钥内嵌安装器 | 节点本地生成 tunnel key，只上传公钥；一次性凭证 |
| 数据目录 | `~/.magpie-connector` | **`~/.dialhome`**（固定，见 RELAY_MANUAL §0） |

**迁移只改一件事**：把旧 forced command 重写成新的 helper 调用。
**key body 与端口逐字节不变**——所以正在跑的旧隧道连接不会被打断（规格 §10：
"不得直接撤销正在运行的旧隧道"）。

---

## 2. 迁移前置检查

1. **先跑 `relay init`**（见 RELAY_MANUAL §2）把 `~/.dialhome` 与 registry 建好。
   确认 `~/.ssh/authorized_keys` 已存在。
2. **确认旧体系目录还在**：默认 `~/.magpie-connector/`，里面有
   - `machines.json`（旧硬件事实；可选，仅用来补 `hardware` 字段）
   - 旧 AK 行就住在**系统 `~/.ssh/authorized_keys`** 里（不是 magpie 目录）。
3. **备份三样东西**（迁移是幂等且只读式改写，但先备份永远没错）：
   ```bash
   cp ~/.ssh/authorized_keys ~/.ssh/authorized_keys.pre-migrate-$(date +%Y%m%d-%H%M%S)
   cp -r ~/.magpie-connector ~/.magpie-connector.pre-migrate-$(date +%Y%m%d-%H%M%S)
   cp ~/.dialhome/registry.json ~/.dialhome/registry.json.pre-migrate-$(date +%Y%m%d-%H%M%S) 2>/dev/null || true
   ```
4. **确认生产隧道当前在线**（`ss -ltn | grep 127.0.0.1:134` 能看到旧节点端口在监听），
   这样迁移后能用「旧 key 仍可用」这条来验证零中断。
5. **macOS UNVERIFIED**：本迁移路径在 Linux 上实测；macOS 真机未验，别盲上生产。

---

## 3. 逐步迁移（生产零中断）

### 3.1 选一个入口（两者等价，走同一个导入器）
```bash
# 入口 A：relay 子命令（LEGACY-DIR 指向含 machines.json 的旧目录）
dialhome relay migrate-legacy ~/.magpie-connector

# 入口 B：顶层别名（要求 --from-legacy；--legacy-dir 默认 ~/.magpie-connector）
dialhome migrate --from-legacy
dialhome migrate --from-legacy --legacy-dir ~/.magpie-connector
```
> 设计上**只有一条迁移路径**：`dialhome migrate --from-legacy` 直接委托给
> `relay migrate-legacy` 同一个幂等导入器，避免两份实现分叉。

### 3.2 发生了什么（逐步）
1. 读取系统 `~/.ssh/authorized_keys`（旧 `magpie-node-*` / `magpie-tunnel-node-*` 行在这里）。
2. 可选读 `<LEGACY-DIR>/machines.json` 的 uuid/macs/user，用来补全新 registry 的
   `hardware` 字段。
3. 对每一行旧节点：
   - 解析出 `name`：来自 `magpie-node-<name>` 取 `<name>`；
     来自 `magpie-tunnel-node-<port>` 取 `port-<port>`（这就是生产里 `port-13435` 的来历）。
   - **key body、permitlisten 端口原样保留**（旧版顺序是 app 在前、ssh 在后，迁移照旧保留，
     不强行改成新模板顺序）。
   - **只重写 forced command** 为 `relay-helper --port <ssh> tunnel`，注释从
     `magpie-node-*` / `magpie-tunnel-node-*` 改成 `dialhome-node-<name>`。
   - 在 registry 建/补节点记录：`state=active`、`ports`、可选 `hardware`、`registered_at="migrated"`。
4. **写 authorized_keys 前先备份**（`.bak-<ts>`）；只在实际改写了 forced command 的行时才落盘。

### 3.3 输出解读
- 真正改写的行会打印 `MIGRATED <name> -> ports(<app>,<ssh>)`，
  并提示 `authorized_keys left byte-for-byte unchanged` 的反例不会出现——
  **只要有 legacy 行被改写，就会写盘并留备份**。
- 若所有行**早已是新格式**（`dialhome-node-*`），导入器判定无需改写，
  打印 `all already migrated` 且 **authorized_keys 一字不改、不生成备份**（见 §6 幂等）。

### 3.4 零中断的关键
迁移**不删、不重拨、不重置**任何正在运行的隧道连接。旧 key 还在 authorized_keys 里
（只是注释和 forced command 变了），sshd 不会因此断开既有会话；新 helper 能继续服务旧 key
（生产实测：旧 win2 私钥经新 Go helper 仍回答 `{"ok":true,"node":"port-13435",...}`）。
因此迁移可在业务高峰直接跑，无需停机窗口。

### 3.5 单行改造示例（看一眼就懂）
下面是两条真实形态的旧行（节选自生产），以及迁移后对应的新行。注意**公钥体、permitlisten 端口、顺序完全没动**，只改了 forced command 与行尾注释。

旧 `magpie-tunnel-node-13435` 行（app 在前、ssh 在后，旧版约定）：
```
restrict,command="/old/magpie-tunnel-check 13437",port-forwarding,permitlisten="127.0.0.1:13435",permitlisten="127.0.0.1:13437",permitopen="127.0.0.1:1" ssh-ed25519 AAAA....b64 magpie-tunnel-node-13435
```
迁移后：
```
restrict,command="/home/user/.dialhome/bin/dialhome relay-helper --port 13437 tunnel",port-forwarding,permitlisten="127.0.0.1:13435",permitlisten="127.0.0.1:13437",permitopen="127.0.0.1:1" ssh-ed25519 AAAA....b64 dialhome-node-port-13435
```
- `<helper>` 路径 = `relay init` 时烤进 registry 的 dialhome 二进制绝对路径。
- 节点名 `port-13435` 来自旧名 `magpie-tunnel-node-13435` 的端口段。
- 两个 `permitlisten` 端口（13435/13437）**原样保留**，顺序是旧版 `(app, ssh)` 而非新模板 `(ssh, app)`——
  导入器刻意保留原顺序，保证旧隧道监听不被扰动。

旧 `magpie-node-<name>` 行同理，只是 name 直接沿用（例如 `magpie-node-n-10-110-23-66-2b12`
→ `dialhome-node-n-10-110-23-66-2b12`），端口顺序仍是旧约定。

### 3.6 `<LEGACY-DIR>` 解析规则
两个入口对旧目录的处理一致：`<LEGACY-DIR>`（或 `--legacy-dir`）指向**含 `machines.json` 的目录**
（默认 `~/.magpie-connector`）。该目录只用来补 `hardware` 字段；**要改写的旧 AK 行始终来自系统
`~/.ssh/authorized_keys`**，不是这个目录。所以即使 `machines.json` 缺失，迁移也能完成，
只是新 registry 的 `hardware` 为空。

---

## 4. 验证清单（迁移后必做）

| # | 检查项 | 命令 / 方法 | 通过标准 |
|---|---|---|---|
| V1 | registry 节点数正确 | `dialhome relay list` | 出现 `n-10-110-23-66-2b12`(13440/13441)、`port-13435`(13437/13435)，state 都是 `active` |
| V2 | 凭证为空 | `dialhome relay inspect <name>` 看 credentials | `credentials: {}`（迁移节点不经过一次性凭证） |
| V3 | **AK 字节不变** | `sha256sum ~/.ssh/authorized_keys` | 与「只改 forced command 的预期」一致；key body 与端口段的 sha 子串**逐字节保留**（KEYS-IDENTICAL vs 备份） |
| V4 | **旧 key 仍可用** | 用旧节点私钥跑一次 tunnel/check | 旧私钥仍能建立隧道、仍能被新 helper 识别（生产已验证 win2/port-13435） |
| V5 | 旧端口仍在监听 | `ss -ltn` / `dialhome relay ports` | 迁移前在听的 `127.0.0.1:13435` 等端口**没掉** |
| V6 | 外来行无损 | `diff ~/.ssh/authorized_keys.pre-migrate-* ~/.ssh/authorized_keys` | 非 dialhome 行逐字节一致 |
| V7 | 备份存在 | `ls ~/.ssh/authorized_keys.bak-*` | 至少一份时间戳备份（若本次有改写） |

> V3/V4 是「零中断」的铁证：key 没换、旧私钥还能用，说明运行中的隧道没受影响。

### 4.1 示例输出
迁移后 `relay list` 应类似：
```
NODE                     STATE    APP    SSH    TUNNEL-FP            HW
n-10-110-23-66-2b12      active   13440  13441  SHA256:ab12...        uuid✓
port-13435               active   13435  13437  SHA256:cd34...        -
```
`relay inspect port-13435` 应类似：
```json
{
  "state": "active",
  "ports": { "ssh": 13437, "app": 13435 },
  "tunnel_pubkey_fp": "SHA256:cd34...",
  "registered_at": "migrated"
}
```
`relay ports` 应输出这些端口（已排序）：
```
13435
13437
13440
13441
```
若 `inspect` 里 `state` 不是 `active`、或 `ports` 与旧体系不符，说明迁移没正确识别旧行——
先按 §7 排查，必要时回滚（§5）。

---

## 5. 回滚

迁移是「只改 forced command、key 不变」的局部改写，**回滚非常轻**：

```bash
# 1) authorized_keys 直接还原到迁移前副本
cp ~/.ssh/authorized_keys.pre-migrate-<ts> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys

# 2) registry 还原（迁移前若已存在旧 registry）
cp ~/.dialhome/registry.json.pre-migrate-<ts> ~/.dialhome/registry.json
```
因为 AK 行的 key body 与端口没动，回滚后旧体系脚本/旧 helper 行为完全回到迁移前状态，
不影响任何在线隧道。若你还没动旧 magpie 体系本身（很多部署选择「双版本并存验证」），
甚至可以不回滚 registry、只回滚 AK 行，让两套都留着对照。

---

## 6. 幂等说明（可以放心重复跑）

迁移导入器是**幂等的**，可以安全地重复执行：

- 对已迁移的行（`dialhome-node-*` 标记）：解析器识别为 `node` 且 `Name` 非空，
  **AK 行逐字节保留不动**，只**补登/重建 registry 记录**（状态置 `active`）。
- 回归测试 `TestMigrateLegacyIdempotentKeepsAKByteForByte` 锁死了这一点：
  对已迁移文件再跑一次 `migrate-legacy`，**authorized_keys 字节不变、且不会产生新备份**，
  registry 正确重建（`win2` → active，ports 13435/13437）。
- 这意味着：registry 万一丢了（实测案例：测试误删 registry.json，但 AK 行还在），
  一条 `migrate-legacy` 就能把两个生产节点的记录**从幸存 AK 行恢复回来**，无需重新登记。

> 操作纪律：registry 重大操作前手动 cp 一份（导入器不自动给 registry 打时间戳备份，
> 只给 authorized_keys 打）。详见 RELAY_MANUAL §6。

---

## 7. 常见失败与对策

| 现象 | 原因 | 对策 |
|---|---|---|
| `legacy ak: ... no such file` | 系统 `~/.ssh/authorized_keys` 不存在 | 先 `touch` 并 `chmod 600`（RELAY_MANUAL §7.1） |
| machines.json 读不到 | `<LEGACY-DIR>` 指错或文件名不符 | 确认目录里有 `machines.json`；没有也能迁，只是 `hardware` 为空 |
| 节点名解析不出 / 行被当 foreign 保留 | 旧行注释格式异常、正则不匹配 | 检查旧行是否真以 `magpie-node-<name>$` / `magpie-tunnel-node-<port>$` 结尾；不匹配的行会**原样保留不报错** |
| 迁移后 enrol 报 "unknown credential" | relay 数据目录不一致（`~/.dialhome` vs `--base`/`DIALHOME_RELAY_DIR`） | 正常运维别用覆盖开关；签发与迁移用同一份 `~/.dialhome`（RELAY_MANUAL §7.2） |
| 负向测试/重连被反复重置 | 触发 OpenSSH 10 per-source penalty（`penalty: failed authentication`） | 放慢节奏；auth 失败的重试直接跳小时级（RELAY_MANUAL §7.5） |
| 节点拨错地址 | `--advertise` 没设对（同机没设 `127.0.0.1:22`） | 同机部署必须 `relay init --advertise 127.0.0.1:22`；relay_port 推导陷阱见 RELAY_MANUAL §7.4 |
| 旧 key 连不上新 helper | helper 二进制路径在 `init` 时烤进 AK 行，路径变了 | 重跑签发/迁移刷新 AK 行，或保持二进制路径不变（RELAY_MANUAL §7.6） |

---

## 8. 迁移后建议

1. 跑一遍 RELAY_MANUAL §8 的负向清单（N1–N10），确认新 helper 的强制命令边界依旧成立。
2. 新节点用 `relay credentials issue` 走新版一次性凭证流程（RELAY_MANUAL §3），
   旧 magpie 安装器可逐步退役。
3. 双版本并存验证期：保留 `~/.ssh/authorized_keys.pre-migrate-*` 备份一段时间，
   确认新版稳定后再清理旧 magpie 体系。

---

## 9. 双版本并存验证（推荐过渡做法）

迁移不强制立刻退役旧 magpie 体系。建议保留一段时间、对照验证：

1. 迁移后**不删** `~/.magpie-connector/` 与旧 magpie 脚本；旧 `magpie-tunnel-check` 等仍可服务
   尚未改写为新格式的行（导入器只改写它能识别的旧行，识别不了的旧行/外来行原样保留）。
2. 用 §4 的 V1–V7 逐项核对新体系；同时用旧体系的 `check`/`register` 探活确认旧路径没坏。
3. 新节点一律走新版 `relay credentials issue`（RELAY_MANUAL §3），旧安装器不再用于新机。
4. 并存期把 `~/.ssh/authorized_keys.pre-migrate-*` 备份留着；确认新版稳定 N 天后，
   再清理旧 magpie 体系与备份。

> 并存期间若同一节点同时被新旧两套写入 authorized_keys，会以最后写入者为准；
> 但迁移只改写 forced command、不动 key/端口，两套对同一条隧道的理解是一致的，不会冲突。

---

## 10. 常见问题（FAQ）

**Q：迁移会把正在跑的旧隧道踢下线吗？**
不会。只改 forced command 与行尾注释，key body 与 permitlisten 端口逐字节不变，
sshd 不会因此断开既有会话（生产实测零中断）。

**Q：迁移后旧节点的 `registered_at` 是什么？**
统一为字面量 `"migrated"`（区别于新版 `credentials issue` 写出的 RFC3339 时间戳）。

**Q：迁移能跑几次？**
任意次。已迁移行只补登 registry、不改写 AK（回归测试 `TestMigrateLegacyIdempotentKeepsAKByteForByte`
锁死）；registry 丢了也能靠幸存的 AK 行一键重建。

**Q：`machines.json` 丢了还能迁吗？**
能。旧 AK 行仍在系统 authorized_keys 里，迁移照常改写 forced command；
只是新 registry 的 `hardware` 字段为空（uuid/macs/user 缺失）。

**Q：为什么生产里有个节点叫 `port-13435`？**
它来自旧 `magpie-tunnel-node-13435`（端口段 13435）；导入器把这类旧名映射为 `port-<port>`。
这是预期命名，不是异常。

**Q：迁移要停机吗？**
不需要。见 §3.4 与 §9。

**Q：macOS 能迁吗？**
本轮无 macOS 真机验证，全部结论来自 Linux 生产实测；macOS 上请先在小规模真机补验。

---

相关：部署与日常运维命令见 [`RELAY_MANUAL.md`](./RELAY_MANUAL.md)；
安全不变量与负向矩阵见 [`SECURITY_MODEL.md`](./SECURITY_MODEL.md)；
登记/运行协议见 [`PROTOCOL.md`](./PROTOCOL.md)。

> 2026-11-15 审评修复：`relay migrate-legacy` 幂等性不变；`relay revoke` 现在会连带清除登记凭证（防止撤销被绕过），迁移回滚流程不受影响。
