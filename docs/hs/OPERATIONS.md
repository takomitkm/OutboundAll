# OPERATIONS — dialhome-hs 日常运维（VPS 139.9.101.203, 2c/1G）

> 纪律：这台机 2c/1G，**不要**在上跑构建/测试/并行压测；一切二进制由 GH Actions
> build-matrix 产出后 scp 上去。

## 1. 部署 / 升级（幂等）

```bash
# 本地（dialhome-server 包 = CI artifact 或 GitHub Release 附件，解包即得）：
tar xzf dialhome-server-linux-amd64.tar.gz -C /tmp/srv
scp /tmp/srv/hs-controller /tmp/srv/hs-deploycheck /tmp/srv/derper root@139.9.101.203:/usr/local/bin/
scp /tmp/srv/deploy.sh root@139.9.101.203:/root/
ssh root@139.9.101.203 'bash /root/deploy.sh <version-tag>'
```

deploy.sh 幂等：重跑 = 原地升级（binaries/units 重写；headscale 配置权威重写、
旧稿存 config.yaml.bak-<ver>；DERP map 按当前证书重算指纹）。

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
| 22/tcp | SSH 管理 + 控制面/管理面隧道（节点与 admin 机各自 `-L` 转发） |
| 443/tcp | derper（数据面中继；自签 IP 证书 + 客户端指纹 pin，ADR-007 D4） |
| 3478/udp | STUN（可选；未开放前 DERP map 里 STUNPort=-1） |

控制面 18080 与管理面 8800 **只绑 127.0.0.1**，不经公网暴露：节点/管理机的控制连接
全部走各自的 SSH 隧道（`-L 18080:127.0.0.1:18080`，server_url 与监听同端口，无中间桥）。
数据面（tailnet 流量）走 443 的 derper，WireGuard 端到端加密不变。

> 单端口教训（2026-11-21 真机）：旧配置 advertise 18080 / listen 8080，中间用
> `nc -l -p 18080 -c "nc 127.0.0.1 8080"` 桥接。该桥**一次只能接一个连接**，
> tailscale 注册要并发多次请求 → 客户端失败后回退成"同端口 https"→ 注册永久卡死。
> 现在 headscale 直接监听 18080，桥已废。管理机上的官方 tailscale 客户端因此能正常注册。

管理机要求：装 **官方 tailscale 客户端**（ADR-007 D2），`hs-admin` 的 default socket
即官方客户端；受控节点不装（用包内 hs-node + sing-box）。

## 6. DERP 证书轮换（ADR-007 D4，替代旧 Caddy TLS 升级路径）

derper `-certmode=manual -hostname=<IP>` 首次启动自动生成自签 IP 证书
（ECDSA P-256，SAN=IP，1 年期，存 `/var/lib/dialhome-derp/`）。轮换：

```bash
systemctl stop dialhome-derp
rm /var/lib/dialhome-derp/139.9.101.203.{crt,key}   # 或等 1 年自然过期后重跑
bash /usr/local/bin/deploy.sh <ver>                  # 重启 derper→重算指纹→重写 map→重启 headscale
```

客户端无需任何改动：新指纹随 DERP map 下发（update_freq 1m），旧 pin 自动失效。
不需要域名、不需要 Let's Encrypt、不需要续期基建——这是对 a11d17f
"only domain+443 TLS" 结论的取代（证据链见 ADR-007 D4）。

## 7. 已知部署边界

- 管理面 HTTP 无 TLS：凭据是 Bearer token，明文仅限本机回环；跨机管理请走
  `ssh -L 8800:127.0.0.1:8800` 隧道。
- derper 公开中继（T-17）：不验证客户端（headscale 无 DERPAdmitClientRequest
  准入端点、`-verify-clients` 需本地 tailscaled），任何能连 443 者可借道转发密文；
  缓解 = 连接限流旗标 + 安全组闸门 + 中继只见元数据（ADR-007 D5）。
- 2c/1G 资源：headscale+controller+derper 常驻 <150MB；不要在这台机编译/跑 e2e。
