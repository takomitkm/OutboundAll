# FAILURE_MODES — 失效模式清单（writing-tests 阶段 1 产出）

> 每条测试用例必须绑定本清单中的一行（测试注释里写 N-<family>-<n> 对应行号）。
> 「不测」是决策，不是遗漏：排除项保留在表里并给理由。

## 1. hs-node 运行时（Phase 2 落地部分）

| # | 类别 | 失效模式 | 机制/触发 | 结果类别 | 注入手段 | 观察 | 测试 |
|---|---|---|---|---|---|---|---|
| FM-n1 | 失控写入 | config.json 被写坏（半截 JSON） | 崩溃在写入中 | 损坏→应拒绝载入 | 写坏文件后 Load | 报错非零退出，不静默 | N-cfg-7*（版本守卫）+ Load 解析错误分支 |
| FM-n2 | 配置自伤 | config.json 藏密钥 | 未来贡献者加字段 | 泄漏 | Save 后全文扫描 | 无 secret 形态字段 | N-cfg-5 |
| FM-n3 | 配置自伤 | 转发被意外打开 | ADR-005/006 姿态回退 | 授权面扩大 | Validate | 拒载 | N-cfg-3/N-cfg-4 |
| FM-n4 | 配置自伤 | 生产配置用 http 控制面 | CI 逃逸口泄漏到生产 | 明文控制面 | Validate | 拒载（TestAllowHTTP 显式才放） | N-cfg-2 |
| FM-n5 | 配置自伤 | 节点名含路径/非法字符 | 用户输入 | 注入面 | Validate | 拒载 | N-cfg-6 |
| FM-n6 | 并发与锁 | 双 supervisor 同时跑 | 用户手跑+自启动同时起 | 资源争用/状态互踩 | 第二次 AcquireInstanceLock | 拒绝 | N-pm-4 |
| FM-n7 | 并发与锁 | 锁未释放导致重启后无法起 | Release 遗漏 | 拒绝服务 | Release 后重取 | 成功 | N-pm-5 |
| FM-n8 | 自身 bug | 重试梯参数漂移 | 重构改数 | 行为漂移 | 12 连败序列 | 精确 5/10/20/40/60/60×4/3600 | N-pm-1 |
| FM-n9 | 自身 bug | auth 失败被当网络失败快重试 | per-source penalty | 惩罚加深 | NextDelayAfterFailure(auth) | 3600s 直跳 | N-pm-2 |
| FM-n10 | 自身 bug | 成功不清零→长期停摆 3600s | MarkConnected 漏调 | 可用性 | 9 败→成功→再败 | 回到 5s | N-pm-3 |
| FM-n11 | 自身 bug | Status 在未启动时 panic | 空指针 | 崩溃 | NewManager 后直接 Status | 不 panic | N-pm-6 |
| FM-n12 | 硬件与底层 | sing-box 包被替换/截断 | 下载链路被劫持 | 恶意代码 | VerifySHA256 | 拒绝 | N-sb-1 |
| FM-n13 | 同地址空间破坏 | zip-slip 解压逃逸 | 恶意压缩包 | 写入任意路径 | 敌意 zip | 拒绝/不逃逸 | N-sb-3 |
| FM-n14 | 环境非标 | 压缩包缺目标二进制 | 半截下载 | 静默失败 | 缺条目 zip | 显式报错 | N-sb-4 |
| FM-n15 | 自身 bug | 自启动命令漂移（错二进制/多壳） | 重构 | 起不来/起错 | RunCommand/Unit/plist 断言 | 逐字节匹配 | N-as-1…5 |
| FM-n16 | 环境非标 | HKCU 写入被沙箱拒 | agent 环境（H13） | 静默失败 | 真实桌面复验 | CI 无法模拟→**UNVERIFIED 标注** | 排除（e2e 真桌面时补） |
| FM-n17 | 环境非标 | 无 user bus 的 Linux 会话 | headless ssh | 回退路径 | userBusAvailable=false | XDG fallback | 集成阶段测（Phase 11） |

### 明确不测（理由）
- sing-box 自身崩溃行为：上游职责；我们测「退出后按梯重启」（procman Run，集成层）。
- 真实掉电/整机断电：CI 无法保真；标注外部补验（与战役纪律一致）。
- macOS LaunchAgent 生效性：UNVERIFIED 平台，仅测渲染内容（N-as-5）。

## 2. hs-controller / 登记面（Phase 3 起补全）
（占位：邀请原子兑换、重放、过期、并发、崩溃恢复、resume-key 通道）

## 3. 授权与撤销（Phase 4 起补全）
（占位：N1'–N14' 已在 docs/hs/SECURITY_MODEL.md §4 列出，实现时逐条映射到此表）
