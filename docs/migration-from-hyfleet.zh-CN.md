# 从 HyFleet 原地迁移到 PolyFleet

PolyFleet `v1.3.0` 延续 HyFleet `v1.2.x` 的数据库和 Agent 协议，支持原地升级。不要创建
空数据库替换生产数据库，也不要只复制数据库而遗漏 master key。

## 保留的兼容接口

首个 PolyFleet 版本故意保留以下名称：

- 二进制和 systemd：`hyfleet-server`、`hyfleet-agent`、`hyfleet-agent-ops`；
- 配置和状态：`/etc/hyfleet`、`/var/lib/hyfleet*`；
- 环境变量和协议头：`HYFLEET_*`、`X-HyFleet-*`；
- Reality 本地 API 路径、受管文件路径和 sing-box 兼容版本后缀。

这些名称已经构成部署 ABI。保留它们可以让数据库、master key、Agent 身份、鉴权缓存、
systemd 权限和自动回滚继续工作。公开品牌、源码 module、Release 资产和容器镜像已改为
PolyFleet。

## 升级顺序

1. 记录版本、服务状态、节点/用户数量和数据库检查结果。
2. 创建一致性数据库备份并把数据库档案和 master key 异地保存。
3. 下载 PolyFleet 固定版本发布包并验证两层 SHA-256。
4. 先运行 `deploy/update-component.sh server`。
5. 确认数据库迁移、HTTPS、用户、节点、订阅和既有 Hysteria2 连接正常。
6. 再逐台运行 `deploy/update-component.sh agent`。
7. 最后添加 Reality 节点；不要把另一实例的加密用户凭据直接复制进生产数据库。

升级前：

```bash
sudo /usr/local/bin/hyfleet-server -version
sudo /usr/local/bin/hyfleet-agent -version
sudo systemctl is-active hyfleet-server hyfleet-agent
sudo bash deploy/backup-server.sh --output-dir /var/backups/hyfleet
sudo -u hyfleet /usr/local/bin/hyfleet-server \
  -config /etc/hyfleet/server.yaml \
  -check-database /var/lib/hyfleet/server.db
```

通过 GitHub Release 或 `scripts/deploy-fleet.ps1` 部署 `polyfleet-v1.3.0-linux-ARCH`
发布包。更新器会保存旧二进制、unit、配置、数据库和 master key 的 root-only 快照，健康
检查失败时自动恢复当前组件。

升级后应确认：

```bash
sudo /usr/local/bin/hyfleet-server -version
sudo systemctl is-active hyfleet-server
curl --fail http://127.0.0.1:8080/healthz
curl --fail https://panel.example.com/healthz
```

然后登录面板核对：

- 原节点、用户、分配、订阅 Token 与累计流量仍在；
- 现有 Hysteria2 客户端仍可连接；
- 数据库已顺序应用 Reality 和节点月流量迁移；
- 所有 Agent 在线，期望版本与已应用版本一致；
- 为节点补充双向月流量额度和 UTC 重置日。

## 合并另一套测试控制面

不同 Server 实例使用不同 master key。另一实例数据库中的节点凭据、用户凭据与订阅凭据
不能通过 SQL 直接复制。推荐做法是：

1. 在生产 PolyFleet 重新创建节点、用户和订阅 Token；
2. 为节点生成新的注册令牌；
3. 在测试节点备份本地 Agent state 和 Reality identity；
4. 清除旧的 Server 注册身份，仅让 Agent 使用新令牌注册生产 Server；
5. 保留本地 Reality identity，等待生产 Server 生成并应用新的受管用户快照；
6. 新订阅验证成功后再停止测试 Server。

测试流量历史通常不值得跨主密钥导入。若业务必须保留旧 UUID 或订阅，应编写一次性
受审计导入工具，用旧 master key 解密后再用新 master key 加密，不能复制密文字段。

## 回滚

应用数据库迁移后，旧 Server 二进制不一定能读取新 schema。升级失败应使用更新器生成的
整套升级前快照恢复旧二进制、数据库和 matching master key，而不是只替换二进制。
Agent 可独立回滚到它自己的最近快照；先确认 Server 兼容版本仍在线。

迁移完成后，继续使用 `/etc/hyfleet` 等名称是正常且受支持的。不要为了“改名干净”在
生产机手工移动这些路径或重命名 unit。
