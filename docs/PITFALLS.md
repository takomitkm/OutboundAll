# PITFALLS — 每一条都是真机上撞过的

格式：**症状** → **根因** → **修法**（现在代码里已经这么做了）。
按区域分组；动手前先扫一遍，能省掉一整晚。

---

## A. 密钥与认证

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| A1 | `Permission denied (publickey)`，key 明明放对了 | Windows 自带 `ssh-keygen -N '""'`（PowerShell 传两个字面引号）会生成**带口令**的私钥 | **在 Linux 上生成**无口令 key 再下发；或走便携版 `ssh-keygen.exe`（它把 `""` 当空串） |
| A2 | `UNPROTECTED PRIVATE KEY FILE` / `bad permissions` | 私钥继承了宽 ACL（含沙箱 `LogonSessionId_*` ACE） | `icacls /inheritance:r /grant:r <user>:F`，**再 `/reset` 一遍后重授**（两遍才稳） |
| A3 | 节点 key 能连上，但读不到任何东西 | 强制命令 + `restrict` 生效了 | 这是**预期**：节点 key 只允许反向转发；要 shell 请用 relay 的登录 key |
| A4 | relay 上 `REMOTE HOST IDENTIFICATION HAS CHANGED` | 端口复用：同一 `127.0.0.1:<port>` 换了一台机器 | `ssh-keygen -f ~/.ssh/known_hosts -R "[127.0.0.1]:<port>"` 后 `accept-new` |
| A5 | 手动 `scp` 回环 key，落地后 ssh 报权限错 | Windows OpenSSH 对私钥 ACL 极严 | 落地后立刻 `icacls` 收紧（`node/sshserver.ps1` 里就是这么做的） |

## B. 反向转发（最费时间的一组）

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| B1 | `Error: remote port forwarding failed for listen port N`；relay 日志 `port forwarding refused` | **`permitlisten="127.0.0.1:N"` 是精确匹配**，而 `-R N:...` 客户端默认绑 `localhost:N` | 必须写全 **`-R 127.0.0.1:N:127.0.0.1:目标`** |
| B2 | 节点能把 relay 当跳板访问 relay 的 LAN | `port-forwarding` 同时放行 `-L` 和 `-R` | 加 `permitopen="127.0.0.1:1"`（一个连不上的目标）→ `-L` 全废，`-R` 不受影响 |
| B3 | 想只放行某些端口，`PermitOpen none` 没用 | `PermitOpen` 管的是 `-L` 目标，不管 `-R` | 用 `permitlisten` 控 `-R`；用 `permitopen` 控 `-L` |
| B4 | 两条通道想共用一条 ssh 连接，结果 RDP/别的转发一起掉 | `ExitOnForwardFailure=yes` 下新增 `-R` 失败会带走整条连接 | **每个通道一条独立 ssh 连接**（本项目两条：13435 / 13437） |
| B5 | 端口偶尔明明空着却转发失败 | 被杀掉的 ssh 留下的监听尚未被 sshd 回收（几秒） | 等 1–2 秒重试；或换个端口验证 |
| B6 | `-R` 通了但 relay 上拿到的是错误服务 | 隧道指到了 magpie 的**页面**端口 3430 | 隧道必须指 **gateway 3425**，3430 永远不通 |
| B7 | 撤销脚本跑完，授权行还在 | 标记写在**行末**（`... AAAAC3… magpie-node-win2`），脚本按 `marker + 空格` 匹配 → 永远匹配不到 | `grep -v -e 'marker$' -e 'marker '` 两种都匹配；撤销后必须复查 `grep -c` |
| B8 | relay 上出现 `0.0.0.0:<port>` 监听（对校园网可见） | 有客户端写了 `-R 0.0.0.0:...` 或裸 `-R <port>:...` | 验收固定看 `ss -ltn`：节点端口**必须**是 `127.0.0.1`；本项目靠 `permitlisten` 强制 |
| B9 | 同一台机器重复跑安装器，越建越多 | 安装器没有"已存在就复用"的逻辑 | `enroll.sh` 幂等：按节点名复用 key 与端口，只补缺失的授权行 |

## C. 进程与会话生命周期

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| C1 | 远程起的 magpie/sshd 一会儿就没了 | `Start-Process` 起的子进程**随 ssh 会话结束被回收** | 常驻进程必须放进**计划任务**再 `Start-ScheduledTask`（用户级、RunLevel Limited 即可） |
| C2 | 计划任务里的 `plugin add`/下载任务秒退（`rc=` 空） | 任务上下文里没有交互控制台 + PowerShell 流重定向不稳 | 让 **`cmd.exe /c`** 做重定向：`cmd /c ""<exe>" <args> > "<log>" 2>&1"` |
| C3 | 新实例起来就退，日志 `another instance is already running` | 上一次的实例还活着，占着命名互斥体 | 先按命令行匹配杀掉旧实例（`connector.ps1 * run`），再起 |
| C4 | 守护进程反复报 `Cannot bind argument ... because it is null` | 守护循环里 `Get-NetTCPConnection` 在某些会话拿不到结果 | 端口探测改 `TcpClient`；日志写 `[IO.File]::AppendAllText` |

## D. PowerShell 5.1 的语言坑

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| D1 | `running scripts is disabled on this system` | 默认 `ExecutionPolicy=Restricted`：**粘贴的文本不受限，但 `& x.ps1` 会被拦** | ①用 `powershell -NoProfile -ExecutionPolicy Bypass -File x.ps1`（进程级、免管理员）；②粘贴块里**绝不出现 `& "…\x.ps1"`** |
| D2 | 想让用户"一行 `iex` 跑脚本" | `ssh` 输出是**字符串数组**，`iex` 只吃 string；且不落盘时 `$PSCommandPath` 为空 → 守护/自启没法重建自己 | 不要 `iex`。用"**下到临时文件再 `-File` 跑**"两行 |
| D3 | `Cannot find an overload for GetBytes and argument count 2` | `-replace "a", 'b'` 写在方法调用括号里时，逗号被当成**第二个参数** | 先算到变量再传：`$s2 = $s -replace "`r",""` → `GetBytes($s2)` |
| D4 | `Error formatting a string: ... near offset N` | `-f` 格式化模板里含代码大括号 `{ }` | 别用 `-f` 拼代码模板，改用**变量插值**（并对 `$` 加反引号转义） |
| D5 | 生成的粘贴块里残留 `{0}` | 用了占位符却忘了 `-f` | 生成后**自检**：`if ($s -match '\{\d+\}') { throw }` + 用 `Parser::ParseInput` 验语法 |
| D6 | 配置校验误报 `ssh.port out of range` | PS 5.1 `ConvertFrom-Json` 把整数解析成 `Int64`，而校验写 `-is [int]` | 接受 `[int]`/`[long]` 后再 `[int]` 比较 |
| D7 | 用 `$Host`/`$host` 存参数，行为诡异 | `$Host` 是**只读自动变量** | 改名（如 `$SshHost`） |
| D8 | 重定向出来的文件 base64 解不开 | PS 5.1 `>` 写 **UTF-16LE（带 BOM）** | `-File` 能直接解析；要 base64 就先 sanitize：`-replace '[^A-Za-z0-9+/=]',''` |
| D9 | `icacls`/`takeown` 后文件反而不给删 | 沙箱/工具环境的 ACL 副作用；真机复现不了 | 卸载脚本里加 `takeown + icacls /reset + attrib` 兜底，别在沙箱里做结论 |
| D10 | `mkdir (Split-Path $p)` 报路径为空 | `$p` 没赋值/为空时 `Split-Path` 返回空 | 先判断空；或用固定目录常量 |

## E. 便携 sshd（rootless）

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| E1 | 没有系统 sshd，脚本直接放弃 | 只找 `C:\Windows\System32\OpenSSH\sshd.exe` | 用 **portable（rootless）sshd**：下 `OpenSSH-Win64.zip` 解到用户目录直接跑 |
| E2 | 便携 sshd 起了但登不进 | `AuthorizedKeysFile` 默认指向账号 `~/.ssh/authorized_keys`，而实际写的是别处 | 在 `sshd_config` 里**显式写死** `AuthorizedKeysFile <绝对路径>` |
| E3 | 登进去报 `Permission denied` 但 key 明显对 | `AllowUsers` 限制的账号名不对（Windows 账号大小写/实际名） | `AllowUsers <真实账号>`；先在节点上 `whoami` 确认 |
| E4 | 校验 key 能不能用，结果判成不可用 | 混用了系统 `ssh-keygen`（对 `""` 处理不同） | 一律用**便携版** `ssh-keygen.exe` 校验 |
| E5 | 想 `schtasks /SC ONLOGON` 建自启，报需要权限 | 该开关要管理员 | 用**启动文件夹快捷方式**（`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup`） |
| E6 | 拿 `atime` 判断"文件有没有被拉走"结论错了 | 挂载是 `relatime`：atime 比 mtime 新且不到 24h 时，再读也不更新 | 别用 atime 做取证；看日志/进程/端口 |

## F. magpie 侧

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| F1 | magpie 起不来，日志 `no Magpie gateway found` | 机器上**根本没装/没启动** magpie（不是 WebView2 的锅） | 先找 exe（`%LOCALAPPDATA%\magpie\magpie-windows-amd64.exe`），没有就拉便携版 |
| F2 | `plugin add` 卡 10 分钟不动 | 它第一步要下 **Bun**（插件运行时），校园网慢；而进程又随会话死 | 放进计划任务 + `cmd` 重定向跑；或在快网机器上下好再塞过去 |
| F3 | `provider test` 花了钱 | 该命令会**真发请求**计费 | 只用 `provider refresh`（免费，只拉列表）；真请求只在必要时挑免费模型 |
| F4 | 429/404 且 trial 与非 trial 返回同一 trace id | magpie **内存态 rest**（`rests.json` 里没有它） | 重启对应机器的 magpie 即清；只探 `-Trial` 模型，别用付费模型试 |
| F5 | magpie 窗口版报错，安装包装 WebView2 又说"已安装" | 注册表残留（如 `122.0.2365.80`）但 `EdgeWebView\Application` 目录不存在 | 想用 UI 就得真装 WebView2（通常一次管理员）；**只要网关就用 `serve`**，完全不需要 WebView2 |
| F6 | 隐藏 magpie 时弹窗/闪窗 | `serve` 是前台进程 | `Start-Process -WindowStyle Hidden`（或计划任务里 `-WindowStyle Hidden`） |

## G. 工具环境（与本项目代码无关，但会让你误判）

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| G1 | 杀掉"上一批工具调用起"的进程后，命令执行器自己崩（`job runner exited ...`） | 运行器对子进程做 job 记账，杀其子孙会破坏记账 | 用**持久终端（PTY）**做清理；或让被杀进程走独立计划任务 |
| G2 | 嵌套引号把远端 PowerShell 命令搞坏（中文乱码 + `ParserError`） | `工作站 → relay → 节点 → powershell` 四层引号 | 远端命令一律 `-EncodedCommand`（UTF-16LE base64）+ 本地脚本文件承载 |
| G3 | 中文输出乱码 | 本机 ACP=936（GBK），PS 5.1 默认按 ANSI 读写 | 所有 `Get-Content`/`Set-Content`/`Out-File` 加 `-Encoding UTF8`；Python `open(..., encoding='utf-8')` |
---

## H. Go 重构版（2026-11 新增，全部真机撞到）

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| H1 | Go spawn `powershell -Command "Expand-Archive ..."` 返回 0，但目标目录**没有**解压结果 | 父进程无控制台时 `Expand-Archive` 的 `Write-Progress` 读控制台缓冲区失败（`Win32 internal error "Access is denied" 0x5`），命令**中途放弃却仍 exit 0** | 不要调 PowerShell 解压；用 Go 标准库 `archive/zip`（本项目 `internal/openssh/extract_windows.go`，带 zip-slip 防护） |
| H2 | `ssh.exe -y -f key` 报 `ERROR: Access is denied.` | `ssh.exe` **没有** `-y` 选项，它把 `-y` 当连接参数 | 公钥渲染用 `ssh-keygen -y -f key`（本项目 `pubOf`） |
| H3 | Linux 上 `fork/exec /usr/sbin/ssh-keygen: no such file` | 从 `sshd` 路径推导 `ssh-keygen` 只在便携包里成立；系统包的 keygen 在 `/usr/bin` | 非 Windows 固定 `/usr/bin/ssh-keygen`（回退 `/bin`） |
| H4 | `ssh -i K -p P user@host -o BatchMode=yes echo marker` 返回 255，marker 没执行 | ssh 把**第一个非选项参数之后**的一切当远程命令；`-o` 与 `echo` 被吞成远端命令 | 连接选项与目标必须在远程命令**之前**：`ssh [opts] user@host echo marker`；回归测试 `TestBuildProbeArgsOrder` |
| H5 | controller 报 `REMOTE HOST IDENTIFICATION HAS CHANGED`，节点明明刚装好 | 节点每次 install 重新生成 host key，而 `127.0.0.1` 的 known_hosts 是上一台机器的 | dialhome 锚点用**控制器私有** known_hosts + `StrictHostKeyChecking=accept-new`，不污染用户 `~/.ssh/known_hosts` |
| H6 | relay 上 `relay credentials issue` 成功，但 helper 报 `unknown credential` | relay 子命令尊重了 `DIALHOME_DATA_DIR` 覆盖，而 helper 在 sshd 下运行**不继承**该变量，两者读了不同 registry | relay/relay-helper 数据目录**恒定** `~/.dialhome`（`relayDataDir()`），只有显式 `--base` 可覆盖 |
| H7 | 凭证里 `relay_address` 是公网地址，同机节点连不上 | `advertise` 默认公网口，但 relay 本机 sshd 实际监听 22（18322 是路由器转发口） | `relay init --advertise 127.0.0.1:22` 用于同机部署；`relay_port` 从 advertise 字符串推导 |
| H8 | Go 里 `if ok, v := f(); ok { ... }` 之后用 `v` 报 undefined | 简短变量声明的作用域是 **if 语句块内**（含 else），块外不可见 | 先调用后判断：`ok, v := f(); if ok { ... }` |
| H9 | 幂等命令（`migrate-legacy`）每跑一次 AK 就多一个空行 | `Render()` 会给每个 Entry（含末尾空行）追加 `\n`，无条件写盘就累积 | 无改写时**跳过写盘**；回归测试 `TestMigrateLegacyIdempotentKeepsAKByteForByte` |
| H10 | forced command 里 `flag` 解析不到值（如 `enroll` 报 unknown credential） | Go `flag` 包**遇到第一个位置参数即停止解析** | 旗标必须在位置动词之前：`relay-helper --credential ID enroll` |
| H11 | 部署新二进制到正在运行的目标路径，scp 成功但远端时间戳/哈希不变 | 目标文件被正在运行的进程占用/替换被延迟 | 先 `pkill` 或 scp 到 `/tmp` 再 `install -m 755 -o user -g user`；部署后用 `sha256sum` 双向比对 |
| H12 | `node run` 报 `open .../config.json: no such file`（明明 install 过） | `node run` 与 `node install` 用了不同的 `DIALHOME_DATA_DIR` | 同一角色命令必须用同一数据目录；E2E 脚本里显式对 controller 命令设同一 `CTRL_DIR` |
| H13 | Windows agent 沙箱里 `reg add HKCU\...\Run` 报 `Access is denied` | agent 工具的子进程继承**受限令牌**，不是产品缺陷 | 用户级注册表写入需在真实桌面会话验证；文档已标"需真实桌面复验" |
| H14 | Windows 上 `os.Rename` 到已存在的目录静默失败 | Windows 不允许 rename 覆盖目录 | 目标目录先 `os.RemoveAll` 再 `os.Rename` |
| H15 | `os.Chmod(0600)` 在 NTFS 上是 no-op，密钥仍宽 ACL | NTFS 用 ACL 不用 mode bits | 用 `icacls /inheritance:r /grant:<user>:F`（本项目 `platform.HardenFile`） |
| H16 | `sshd -t -N "" -f key` 生成 host key 报 `Extra argument ed25519` | portable `sshd` 不接受 key 生成参数 | 用 `ssh-keygen -q -t ed25519 -N "" -f hostkey` |

---

## I. 代码审评修复（2026-11-15，P1/P2）

| # | 症状 | 根因 | 修法 |
|---|---|---|---|
| I1 | **P1 安全**：为节点签凭证→不消费→`relay revoke`→用旧凭证 enroll，节点被重新置为 active | `relayRevoke` 只删节点 AK 行，未清凭证；`EnrollCommand` 不检查 node.State、不检查凭证是否仍是节点当前 ID，直接 `State="active"` | 双重防线：revoke 删除该节点全部凭证记录 + AK 行并清空 `CredentialID`；helper 拒绝 `revoked` 节点与过期凭证 ID。**真实 SSH 负向测试**（临时 rootless sshd，replay 旧凭证行）两道均 DENY |
| I2 | **P2**：helper 装在 `/home/user/my tools/dialhome`，forced command 不执行 | `%q` 只做了 authorized_keys 选项值转义，可执行路径没做 POSIX shell 参数引用，登录 shell 把路径拆成多词 | 分离两层转义：`ShellQuote()`（单引号 + `'\''`）包住路径，`AKOptionValue()`（`\"`/`\\`）包住整条命令；CR/LF/NUL 直接拒绝。测试覆盖空格、单双引号、反斜杠、`$(id)`、反引号、分号 |
| I3 | **P2**：`0.0.0.0:13440` / `[::]:13440` 已占用，`AllocatePair` 仍把该端口分给新节点 | `livePorts()` / `LivePortsFromSS` 只匹配 `127.0.0.1:` 前缀 | 解析 `ss -ltn` 本地地址列的**所有**形式（IPv4/通配/`[::]`/`*:`/`[::1]`），取最后冒号后的数字端口；拒绝把 Recv-Q/Send-Q 数字列误判为端口。单测覆盖 6 种写法 |
| I4 | **P2**：`relay purge` 后凭证公钥仍留在系统 authorized_keys | purge 只删 registry 记录，没同步 `RemoveCredential` + 写盘 | purge 接收 `akPath`，删除凭证记录时一并删 AK 行并原子写回；其他节点/凭证行保持不变 |
| I5 | **P2**：通用部署必须改代码里的 `relay_user: "user"` 和 `10.101.54.192` | 凭证签发硬编码账户名，地址虽有 `--advertise` 但无账户配置 | `relay init --relay-user USER` 存入 registry；`advertiseUser()` 依次取配置 / `$USER` / `$USERNAME` / 兼容默认；地址默认仍在 `--advertise` 帮助里写明并可由 co-located 部署覆盖 |
| I6 | `go.mod` 声明 1.27，CI `setup-go` 装 1.24，靠 `GOTOOLCHAIN` 自动下载补齐 | 版本不统一 ⇒ 构建不可复现 | 三处统一 `1.27` 并加工作流级 `GOTOOLCHAIN: local`（版本不符就显式失败，不静默下载） |
| I7 | 测试里用假 base64 公钥体构造 AK 行，断言"已删除"却其实 `Parse` 根本没识别 | `parseAKLine` 会校验 blob 内嵌 `ssh-ed25519` 前缀 | 测试一律用 `crypto.Generate` 或 `NodeLine/CredentialLine` 生成真实行 |
