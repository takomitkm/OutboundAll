# CALIBRATION — 红绿校准台账（Iron Law 证据）

> 每条负向用例必须"红过一次"。本文件记录每条用例的红是怎么来的：
> CI 实测红 / 开发期红（测试先于守卫写出）/ 按构造红（删除守卫必然翻红，理由写明）。

## 政策

- **集成级**红绿：hs-e2e 链的每一轮失败都是真实红（policy 拒 `*`、假绿管道、cleanup 死锁各红过一轮，见 git log 7a0f3af→128b5f4）。
- **单元级**：负向测试（拒绝类）按构造红——即"守卫若被删除，测试必然失败"；理由写在每条用例旁。抽样做真实红：对守卫做过一次"守卫关闭→CI 红"验证的，在表里标 LIVE-RED + run 链接。
- 红绿校准抽样比例：每个家族至少 1 条 LIVE-RED。

## 台账

| 测试 | 绑定失效模式 | 红的来源 | 证据 |
|---|---|---|---|
| TestPhase1bChain（policy/注册链） | 架构假设错误 | CI 实测红 ×5 轮（7a0f3af…45d2840） | git log + run 记录 |
| TestInstanceLockExclusive（N-pm-4） | FM-n6 | **LIVE-RED 实证**：TryAcquire 落地前，lockfile.Acquire 是阻塞锁（unix LOCK_EX 无 LOCK_NB；windows LockFileEx 无 FAIL_IMMEDIATELY）→ 第二次获取挂死，CI 跑者 10 分钟超时（run a3d6ce9 之前的 97a30a5）。这同时暴露了共享 lockfile 包缺少非阻塞语义——参考实现的「another supervisor owns this node」路径在阻塞锁下永远不会触发。修复 = 加 TryAcquire（平台原语 additive，不动既有阻塞调用方） | run 97a30a5 超时日志 |
| TestConfigRejectsPlainHTTP（N-cfg-2） | FM-n4 | 按构造红：删 Validate 的 https 分支必翻红 | — |
| TestConfigForwardingConflicts（N-cfg-3） | FM-n3 | 按构造红 | — |
| TestSaveWritesNoSecrets（N-cfg-5） | FM-n2 | 按构造红（加 secret 字段必被抓） | — |
| TestVerifySHA256Mismatch（N-sb-1） | FM-n12 | 按构造红 | — |
| TestExtractZipRefusesZipSlip（N-sb-3） | FM-n13 | 按构造红 | — |
| TestLadderSequence（N-pm-1） | FM-n8 | 按构造红（任何参数漂移翻红） | — |
| TestLadderAuthFailureJumpsHourly（N-pm-2） | FM-n9 | 按构造红 | — |

## 已收获的教训（Phase 2 CI 红轮）

- TestLadderSequence 首版把「第 10 次失败才跳小时级」错写成「第 11 次」——测试抓住了
  我对规格（≥10 次→3600s）的误读，改的是测试不是代码。
- TestRunCommand 抓住 %q 引用：Windows 命令行不要 Go 式反斜杠转义。
- TestConfigRejectsWrongVersion 抓住「Save 拒绝非法配置」与「Load 拒绝非法配置」的
  互补设计——测试改为直写原始 JSON 模拟未来版本落盘。

## 后续抽样计划

- Phase 3（登记面）：邀请原子兑换 / 重放拒绝——CI 实测红（并发竞态值得真实红）。
- Phase 4（授权）：N8'/N9' deny-by-default——CI 实测红（先无 policy → 红，再显式 deny → 绿）。
