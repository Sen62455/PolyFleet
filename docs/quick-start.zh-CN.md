# PolyFleet 新手部署与使用

本文面向第一次部署 PolyFleet 的管理员。示例使用 `v1.3.0`、Debian/Ubuntu、
systemd 和 Nginx；请把域名与端口替换成自己的值。PolyFleet Server 只监听本机
`127.0.0.1:8080`，必须通过 HTTPS 反向代理访问。

## 1. 部署前准备

- Server 和 Agent 支持 Debian 12/13、Ubuntu 24.04，以及 `amd64`、`arm64`。
- 建议 Server 至少 1 vCPU、512 MiB 可用内存和 2 GiB 可用磁盘。节点资源还需
  计入 Hysteria2 或 sing-box。
- 为面板准备独立域名，例如 `panel.example.com`，添加 A/AAAA 记录并等待解析。
- 开放面板 TCP 80/443。节点另按协议开放 Hysteria2 UDP 端口或 Reality TCP 端口。
- 每个 Agent 必须能通过 HTTPS 访问面板；不要公开 Agent 本地接口、operations
  socket、Hysteria2 鉴权/统计接口或 Reality 管理接口。

先确认域名解析到 Server：

```bash
getent ahosts panel.example.com
```

## 2. 安装 Server

从固定发布标签下载并阅读安装入口。不要从浮动分支直接执行脚本：

```bash
VERSION='v1.3.0'
curl --fail --location --proto '=https' --tlsv1.2 \
  -o install.sh \
  "https://raw.githubusercontent.com/Sen62455/PolyFleet/${VERSION}/install.sh"
less install.sh
sudo bash install.sh server \
  --version "${VERSION}" \
  --public-url https://panel.example.com
```

安装完成会显示一次初始化令牌。它也暂存在 root 可读的
`/etc/hyfleet/server.env`。这里的 `hyfleet-*` 是从 HyFleet 保留的兼容运行时名称，
不是装错了项目。

检查本机服务：

```bash
sudo systemctl is-active hyfleet-server
curl --fail http://127.0.0.1:8080/healthz
```

## 3. 配置 Nginx 和 HTTPS

安装 Nginx 与 Certbot：

```bash
sudo apt-get update
sudo apt-get install -y nginx certbot python3-certbot-nginx
```

创建 `/etc/nginx/sites-available/polyfleet.conf`：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name panel.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

启用站点并申请证书：

```bash
sudo ln -s /etc/nginx/sites-available/polyfleet.conf \
  /etc/nginx/sites-enabled/polyfleet.conf
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d panel.example.com
sudo certbot renew --dry-run
```

若主机已有 Nginx/Caddy 配置，应把同等反向代理规则并入现有配置，不要覆盖其他站点。

## 4. 创建管理员

1. 打开 `https://panel.example.com`。
2. 输入安装时显示的初始化令牌。
3. 创建唯一管理员用户名和至少 12 位的强密码。
4. 初始化成功后删除临时令牌并重启服务：

```bash
sudo rm -f /etc/hyfleet/server.env
sudo systemctl restart hyfleet-server
```

PolyFleet `v1.3.0` 是单管理员、小型自托管控制面，不提供多管理员 RBAC。

## 5. 添加原生 Hysteria2 节点

先在节点上独立安装并验证 Hysteria2。它应使用 systemd，并把配置放在
`/etc/hysteria/config.yaml`。PolyFleet 不接管证书申请，也不替代 Hysteria2 核心。

在面板的“节点”页选择“添加节点”：

- 适配器选择“原生 Hysteria2”；
- 填写公网域名/IP、实际 UDP 端口和 TLS SNI；
- 如服务商给出月流量额度，填写“节点总额度（双向）”和 UTC 重置日；
- 保存后选择“生成注册令牌”。

在节点上运行：

```bash
VERSION='v1.3.0'
curl --fail --location --proto '=https' --tlsv1.2 \
  -o install.sh \
  "https://raw.githubusercontent.com/Sen62455/PolyFleet/${VERSION}/install.sh"
sudo bash install.sh agent \
  --version "${VERSION}" \
  --server-url https://panel.example.com \
  --node-name edge-hy2 \
  --adapter native-hysteria2 \
  --core-config-path /etc/hysteria/config.yaml
```

安装器会在终端中隐藏式读取一次性令牌，并在注册成功后删除令牌环境项。然后按项目中的
`deploy/configure-hysteria.sh` 将 Hysteria2 切到本机 HTTP 鉴权和统计接口。变更前备份
原配置，并在临时 UDP 端口验证；已有生产节点应遵循
[原生切换手册](native-cutover-runbook.zh-CN.md)。

## 6. 添加 VLESS + Reality 节点

Reality 节点应是没有其他 PolyFleet Agent、没有需要保留的 `/usr/bin/sing-box`，且
目标 TCP 端口未被占用的干净 VPS。当前固定配置为 VLESS over direct TCP、Reality、
`xtls-rprx-vision`。不支持 WebSocket、gRPC、HTTPUpgrade 或任意 sing-box JSON。

在面板添加节点：

- 适配器选择“VLESS Reality”；
- 填写节点公网域名/IP和监听 TCP 端口；
- SNI/伪装域名及“Reality 握手服务器”使用可从节点正常访问的公网 TLS 站点；
- 握手端口固定为 443；不要填写自己的面板域名或节点自身域名作为伪装目标。
- 保存后生成一次性注册令牌。

在干净节点运行：

```bash
VERSION='v1.3.0'
curl --fail --location --proto '=https' --tlsv1.2 \
  -o install.sh \
  "https://raw.githubusercontent.com/Sen62455/PolyFleet/${VERSION}/install.sh"
sudo bash install.sh agent \
  --version "${VERSION}" \
  --server-url https://panel.example.com \
  --node-name edge-reality \
  --adapter vless-reality
```

发布包包含项目固定版本的 sing-box。安装器会验证架构、版本和 SHA-256 后安装到
`/usr/bin/sing-box`，生成 root-only Reality 身份，并启动
`hyfleet-sing-box-reality.service`。不要自行替换该二进制或编辑受管 JSON；不匹配时
Agent 会失败关闭，而不是尝试兼容未知核心。

检查状态：

```bash
sudo systemctl is-active hyfleet-agent hyfleet-agent-ops.socket \
  hyfleet-sing-box-reality
sudo journalctl -u hyfleet-agent -n 100 --no-pager
sudo ss -ltnp
```

## 7. 添加用户、分配节点和创建订阅

1. 打开“用户”页，选择“添加用户”。
2. 填写用户名、到期时间和全局流量额度；`0` 表示不限额。
3. 选择一个或多个受管节点。上传和下载合计计入用户额度。
4. 保存后等待节点的“期望版本”和“已应用版本”一致。
5. 第一次显示的节点独立凭据只展示一次；通常优先使用统一订阅，不要手工分发凭据。
6. 打开用户详情，创建订阅 Token，选择允许的 URI、Base64、Mihomo/Clash 或
   sing-box 格式。
7. 将生成的 HTTPS 地址导入相应客户端。订阅地址本身是密钥，泄漏后应立即轮换或撤销。

混合订阅会按节点适配器输出 `hysteria2://` 和 `vless://`。只有 Agent 已应用的有效配置
才会进入订阅；Reality 身份轮换期间，该节点会暂时从订阅中隐藏，直到新身份应用完成。

## 8. 流量、在线状态和踢下线

- 用户页显示上传、下载、合计用量、额度状态和活跃连接。
- 节点页显示已归属流量、未归属流量、在线用户数和连接数。
- 打开用户详情，可对支持的受管节点执行“踢下线”。该操作只断开当前连接；若用户仍
  启用且未超额，客户端可以重新连接。
- 禁用用户、到期或达到全局/节点分配额度会拒绝新连接，并在受支持的数据面同步状态。
- 统计由 Agent 定期上报，不是逐字节实时计费；控制面或 Agent 短暂离线时会延迟显示。

## 9. 节点月流量额度

节点编辑框中的额度用于代替没有流量查询 API 的服务商控制台：

- 额度按上传加下载的双向合计计算，单位可选 GiB/TiB；`0` 表示不设置额度。
- 重置日按 UTC 计算；若填写 31，短月份使用当月最后一天。
- 80% 产生预警，100% 产生严重告警；该功能用于提醒，不会自动关停整台节点。
- 面板统计只包含代理核心可归属的受管流量，通常不等于服务商统计的整机网卡流量。
- 在节点流量面板使用“校准”录入服务商控制台当前周期的已用双向流量。之后显示值会以
  该基准加上新采集的代理流量估算；新周期会清除旧校准。

## 10. 更新、备份、恢复和排障

每次更新先备份 Server，并先更新 Server、后更新 Agent：

```bash
sudo bash deploy/backup-server.sh --output-dir /var/backups/hyfleet
sudo bash deploy/diagnose.sh server
sudo bash deploy/diagnose.sh agent
```

数据库备份和 master key 是两个独立文件，缺少任意一个都无法恢复用户凭据。应把两者
分别加密保存到 VPS 之外。升级脚本会在 `/var/lib/hyfleet-backups` 创建本地回滚快照，
但它不能代替异地备份。

常用检查：

```bash
sudo systemctl status hyfleet-server --no-pager --full
sudo systemctl status hyfleet-agent --no-pager --full
sudo journalctl -u hyfleet-server -n 100 --no-pager
sudo journalctl -u hyfleet-agent -n 100 --no-pager
curl --fail http://127.0.0.1:8080/healthz
```

常见错误包括：面板域名还未解析、HTTPS 证书链错误、Agent 使用 HTTP 地址、节点名与
面板记录不一致、注册令牌过期、生产端口已被占用、Reality 伪装站点不可访问、手工替换
固定 sing-box、以及期望版本尚未应用就测试旧订阅。

更完整的兼容边界见 [兼容性矩阵](compatibility.md)，HyFleet 原地升级见
[迁移文档](migration-from-hyfleet.zh-CN.md)。
