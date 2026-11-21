# OutboundAll 文档索引

> 分支 `go-refactor`（Go / 跨平台重构版）。本索引按**读者角色**组织，列出当前仓库**实际存在**的文档。
> 公共前提（所有文档都假设成立）：**三身份 + 回环 + 无管理员**——节点隧道 key / Controller 管理 key / 短期登记凭证三者互不通用；所有端口绑定均为 `127.0.0.1` 字面量（不允许 `localhost`/`0.0.0.0`）；节点全程 rootless 便携 sshd + 用户级自启动，relay 侧零新增服务。

## 我要部署节点

- **`docs/NODE_MANUAL.md`**（Go 版，由其他代理编写中）— 节点部署完整手册：`node install/run/status/stop/uninstall/logs` 的逐参数与场景。读它：你要在某台 Windows/Linux 机器上把节点装起来。
- **`docs/man-OutboundAll.md`**（Go 版）— 全部命令的 man 页式参考，含退出码与环境变量。读它：你只想快速查某条命令的语法/参数/退出码。
- **`docs/CONFIG_SCHEMA.md`**（Go 版）— `config.json` / `identity.json` / `machines.json` / `registry.json` / 凭证文件结构。读它：你要手写、校验或排错节点/中继配置文件。
- **`docs/SSH_BACKEND_MATRIX.md`**（Go 版）— 三平台原生 OpenSSH 可行性实测（Linux/Windows 已验证，macOS UNVERIFIED）。读它：你想确认你的平台是否被支持、为什么不用嵌入式 Go SSH server。

## 我要管理节点（Controller）

- **`docs/CONTROLLER_MANUAL.md`**（Go 版，由其他代理编写中）— Controller 使用手册：`controller init/list/status/anchors/shell/exec` 与多锚点 failover。读它：你要从总控登入节点取 shell、执行命令、看存活。
- **`docs/man-OutboundAll.md`**（Go 版）— `controller` 子命令参考。读它：你只查 `controller shell/exec` 的精确语法。

## 我要运维 relay

- **`docs/RELAY_MANUAL.md`**（Go 版，由其他代理编写中）— Relay 运维手册：`relay init/list/inspect/revoke/purge/ports/credentials/...` 与 `relay-helper`。读它：你在管中继的 `authorized_keys`、登记凭证、端口分配。
- **`docs/SECURITY_MODEL.md`**（Go 版）— 三身份、AK 行模板、`permitlisten`/`permitopen`、威胁模型、负向测试矩阵。读它：你要理解/审计 relay 的授权边界，或排查"为什么某个 key 没有某能力"。
- **`docs/PROTOCOL.md`**（Go 版）— 登记与运行期协议、relay `authorized_keys` 维护、端口分配器、Controller⇄Node 路径。读它：你要深入 relay/节点/Controller 之间的交互细节。
- **`docs/man-OutboundAll.md`**（Go 版）— `relay` / `relay-helper` 子命令与安全模型要点。读它：你只查 relay 命令语法。

## 我要从旧版迁移

- **`docs/MIGRATION_GUIDE.md`**（Go 版，由其他代理编写中）— 从旧 PS/Python 体系迁移到 Go 版的步骤。读它：你手上有旧版（PowerShell 安装器 / `relay/*.sh` / `client/dial.py`）节点，要迁到 Go 版。
- **`docs/REFACTOR_BASELINE.md`**（Go 版）— 旧实现全量审计（事实正本）：要保留的行为、安全约束、旧仓库清单、接入路径。读它：你想核对"旧版到底做了什么"或迁移要保留哪些事实。
- **`docs/man-OutboundAll.md`**（Go 版）— `migrate --from-legacy` 与 `relay migrate-legacy` 命令。读它：你只查迁移命令的精确用法。

## 我要理解设计

- **`docs/ARCHITECTURE.md`**（Go 版）— 角色与进程模型、数据布局、包结构、生命周期、平台矩阵。读它：你要建立整体架构心智模型，或评估改动落在哪一层。
- **`docs/SECURITY_MODEL.md`**（Go 版）— 安全不变式与威胁模型。读它：你评估安全设计是否达标。
- **`docs/PROTOCOL.md`**（Go 版）— 登记/运行/迁移期协议。读它：你理解节点⇄relay⇄Controller 的协议细节。
- **`docs/PRIOR-ART.md`**（设计调研，含旧版对照）— 与同类项目（ngrok/frp/Teleport…）的差异、本方案"零管理员 + 受限 key + 一行 enroll"的不可替代点。读它：你要向别人解释"为什么不直接用现成方案"，或理解设计取舍来源（文中引用了旧版 `client/dial.py` 等背景）。

## 我要排障

- **`docs/PITFALLS.md`**（Go 版文档集，含旧版 PS 时代条目，仍具参考价值）— 每一条都是真机撞过的坑：密钥/认证、反向转发（`permitlisten`/`permitopen`/`127.0.0.1`）、进程生命周期、PowerShell 5.1、便携 sshd、magpie、工具环境。读它：遇到 `Permission denied` / 转发失败 / 权限报错时先扫一遍。
- **`docs/PORTS.md`**（legacy 历史存档）— 端口模型与旧版 bootstrap（18443）开户口说明。读它：你理解"为什么只需一个对外端口"的整体思路；但其中的 iex 一行接入、18443 端口是**旧版**机制，**不要照搬到 Go 版**。
- **`docs/STATUS.md`**（legacy 历史存档）— 早期实测记录（节点 A、`D:\zc\sshd` 等）。读它：你追溯某些历史证据的来龙去脉；当前部署请以 `doctor` 与 `PHASE4_ACCEPTANCE.md` 为准。
- **`docs/man-OutboundAll.md`**（Go 版）— 退出码表（10–19 系列）与环境变量。读它：你根据 doctor/命令的非零退出码定位失败类别。

## 验收证据

- **`docs/PHASE4_ACCEPTANCE.md`**（Go 版）— Relay 安全核心（N1–N10 负向测试、P1/P2 正向）真 SSH 实测记录。读它：你要证明 relay 授权边界确实成立。
- **`docs/SSH_BACKEND_MATRIX.md`**（Go 版）— 三平台 SSH 后端实测读数。读它：你要确认平台支持度与验证状态（macOS UNVERIFIED）。
- **`docs/ARCHITECTURE.md`**（Go 版）— 末尾平台矩阵（真机验证状态口径）。读它：你查某项能力在哪些平台已验证/未验证。

---

## 文档性质速查

**Go 版（当前重构，以实为据）**：`ARCHITECTURE.md`、`CONFIG_SCHEMA.md`、`SECURITY_MODEL.md`、`PROTOCOL.md`、`PITFALLS.md`（含部分旧版 PS 条目）、`PHASE4_ACCEPTANCE.md`、`SSH_BACKEND_MATRIX.md`、`REFACTOR_BASELINE.md`、`man-OutboundAll.md`，以及由其他代理编写中的 `NODE_MANUAL.md`、`CONTROLLER_MANUAL.md`、`RELAY_MANUAL.md`、`MIGRATION_GUIDE.md`。

**legacy 历史存档（旧 PS/Python 体系，迁移窗口内的对照与证据，勿照搬其机制）**：`FAST-PATH.md`、`RUNBOOK.md`、`magpie-remote-provider-research.md`、`PORTS.md`、`STATUS.md`、`PRIOR-ART.md`（设计调研，引用旧版背景）。

**遗留代码（旧版，迁移验收完成前保留、未删除）**：`node/*.ps1`（如 `node/sshserver.ps1`、`node/connector.ps1`）、`relay/*.sh`（如 `relay/enroll.sh`、`relay/revoke-node.sh`）、`client/dial.py`（旧工作站拨号器）、`client/onboard.cmd`（USB 离线旧接入）。这些不是 Go 版代码路径，仅供迁移对照与历史取证。
