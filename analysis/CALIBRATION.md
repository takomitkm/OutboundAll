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
| TestInstanceLockExclusive（N-pm-4） | FM-n6 | 待 LIVE-RED（计划：临时改 Acquire 为 no-op → CI 红 → 恢复） | ⬜ |
| TestConfigRejectsPlainHTTP（N-cfg-2） | FM-n4 | 按构造红：删 Validate 的 https 分支必翻红 | — |
| TestConfigForwardingConflicts（N-cfg-3） | FM-n3 | 按构造红 | — |
| TestSaveWritesNoSecrets（N-cfg-5） | FM-n2 | 按构造红（加 secret 字段必被抓） | — |
| TestVerifySHA256Mismatch（N-sb-1） | FM-n12 | 按构造红 | — |
| TestExtractZipRefusesZipSlip（N-sb-3） | FM-n13 | 按构造红 | — |
| TestLadderSequence（N-pm-1） | FM-n8 | 按构造红（任何参数漂移翻红） | — |
| TestLadderAuthFailureJumpsHourly（N-pm-2） | FM-n9 | 按构造红 | — |

## 后续抽样计划

- Phase 3（登记面）：邀请原子兑换 / 重放拒绝——CI 实测红（并发竞态值得真实红）。
- Phase 4（授权）：N8'/N9' deny-by-default——CI 实测红（先无 policy → 红，再显式 deny → 绿）。
