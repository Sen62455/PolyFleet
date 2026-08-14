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
不能通过 SQL 直接复制。Reality 节点还需要处理两份仅存在节点本地的 root-only 状态：

- `reality-hyfleet-sing-box-reality.json` 中的密钥身份绑定原控制面的节点 ID；
- `reality-hyfleet-sing-box-reality-applied.json` 中的已应用状态同时绑定节点 ID、原控制面的
  desired version 和快照哈希。

因此不能把这两份文件原样带到新节点记录。否则 helper 会以
`reality_node_mismatch`、`reality_identity_failed` 或 `reality_version_conflict` 拒绝应用；
这些检查是防止跨节点复用私钥或旧快照的安全边界，不应关闭。推荐做法是：

1. 在生产 PolyFleet 重新创建节点、用户和订阅 Token；
2. 记录生产节点的新节点 ID，并为它生成新的注册令牌；
3. 停止测试节点 Agent，把 Agent state、Reality identity 和 Reality applied state 备份到
   root-only 目录并异地保存；备份和迁移过程都不要输出文件正文；
4. 切换 Server URL，清除旧控制面的 Agent 注册身份，并仅使用新令牌注册生产 Server；
5. 在首次同步生产快照前，根据是否必须保留现有客户端密钥选择下面一条路径；
6. 启动 Agent，等待生产面板显示 Agent 在线、desired/applied version 一致、Reality
   key generation 一致且用户分配均已应用；
7. 用新控制面生成的订阅完成外部连接、双向流量和在线状态测试后，再停止测试 Server。

允许客户端刷新密钥时，备份后删除旧 identity 和旧 applied state。下一次应用会为生产
节点生成全新的 Reality 身份；旧订阅必须刷新。

必须保持原公钥和 Short ID 时，只能使用经过审阅、基于 JSON parser 的一次性迁移工具，
把 identity 中的 `node_id` 原子地改为生产节点 ID，其他字段一字不改，并保持
`root:root 0600`。随后必须删除旧 applied state，让生产控制面用自己的版本和快照哈希
重新建立它。不要用 `sed` 修改 JSON，不要复制旧 applied state，也不要在日志或终端打印
Reality 私钥。完成后应删除一次性迁移工具。

测试流量历史通常不值得跨主密钥导入。若业务必须保留旧 UUID 或订阅，应编写一次性
受审计导入工具，用旧 master key 解密后再用新 master key 加密，不能复制密文字段。

## 回滚

应用数据库迁移后，旧 Server 二进制不一定能读取新 schema。升级失败应使用更新器生成的
整套升级前快照恢复旧二进制、数据库和 matching master key，而不是只替换二进制。
Agent 可独立回滚到它自己的最近快照；先确认 Server 兼容版本仍在线。

迁移完成后，继续使用 `/etc/hyfleet` 等名称是正常且受支持的。不要为了“改名干净”在
生产机手工移动这些路径或重命名 unit。
