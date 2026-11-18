# OPERATIONS — dialhome-hs 日常运维（VPS 139.9.101.203, 2c/1G）

> 纪律：这台机 2c/1G，**不要**在上跑构建/测试/并行压测；一切二进制由 GH Actions
> build-matrix 产出后 scp 上去。

## 1. 部署 / 升级（幂等）

```bash
# 本地（Actions artifact 下载后）：
scp hs-controller hs-node hs-deploycheck root@139.9.101.203:/usr/local/bin/
scp hs/deploy/deploy.sh root@139.9.101.203:/root/
ssh root@139.9.101.203 'bash /root/deploy.sh <version-tag>'
```

deploy.sh 幂等：重跑 = 原地升级；headscale 配置只在首次生成（不覆盖已有）。

## 2. 日常检查

```bash
ssh root@139.9.101.203 hs-deploycheck                     # 两个健康端点
ssh root@139.9.101.203 'systemctl status headscale hs-controller --no-pager | head -20'
ssh root@139.9.101.203 'journalctl -u hs-controller -n 30 --no-pager'
```

## 3. 管理操作（全部经 hs-admin / hs-controller CLI）

```bash
ssh root@139.9.101.203 hs-controller invite create --label win-lab
ssh root@139.9.101.203 hs-controller node list
ssh root@139.9.101.203 hs-controller token-rotate   # 换管理 token（须重启 serve）
```

## 4. 备份 / 恢复

```bash
# 每周（或改前手动）：
ssh root@139.9.101.203 'tar czf /root/dialhome-hs-backup-$(date +%F).tgz \
  /etc/headscale /var/lib/headscale/db.sqlite /var/lib/dialhome-hs/registry.json \
  /var/lib/dialhome-hs/admin-token'
# 恢复：解包 → systemctl restart headscale hs-controller
```

## 5. 端口清单（对外只开这些）

| 端口 | 用途 |
|---|---|
| 22/tcp | ssh 管理（已有） |
| 3478/udp | STUN（DERP 打洞） |

控制面 8080 与管理面 8800 **只绑 127.0.0.1**——节点从公网经 DERP/直连连控制面时，
通过 DERP over HTTPS 的 443…（注：单 VPS 初始部署里，tailnet 控制连接走 DERP over
HTTPS 443 由 headscale 自身提供；如需对外暴露 8080，上 Caddy TLS，见 §6）。

## 6. TLS（升级路径，未默认启用）

控制面现在 http://127.0.0.1:8080 只监听回环——纯本机部署/单机演示用。
要接真节点（跨网络），启用 Caddy：

```
hs.example.net {
  reverse_proxy 127.0.0.1:8080
}
```

然后 `server_url` / `control_url` 改 https 并 re-enroll 节点（节点配置的
TestAllowHTTP 为 false，生产环境强制 https——见 ADR-001 / SECURITY_MODEL）。

## 7. 已知部署边界

- 管理面 HTTP 无 TLS：凭据是 Bearer token，明文仅限本机回环；跨机管理请走
  `ssh -L 8800:127.0.0.1:8800` 隧道。
- DERP `verify_clients: false`：初始部署开放中继（见 ADR-006 生产门控同理）；
  收紧 = true + 所有节点登记后。
- 2c/1G 资源：headscale+controller 常驻 <100MB；不要在这台机编译/跑 e2e。
