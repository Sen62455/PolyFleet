# 阶段 4：统一订阅与凭据轮换

## 交付范围

阶段 4 为控制面增加：

- 每个用户可创建多个独立、可轮换、可撤销、可到期的订阅 Token；
- Hysteria2 URI、标准 Base64、Clash Meta YAML 与 sing-box JSON 输出；
- 节点公网域名/IP、UDP 端口、TLS SNI 和证书校验设置；
- 自签名端点的证书 SHA-256 指纹与公钥 SHA-256 固定；
- 单节点与用户全部节点的 Hysteria2 凭据轮换；
- 订阅独立限流、无缓存响应、路径日志脱敏和一次性明文展示；
- 对用户状态、到期、额度、节点、分配和 applied 凭据的严格筛选。

`v0.4.0-dev` 首次发布时只包含 `native_hysteria2` 节点。自
`v0.5.0-dev` 起，已显式接管并完成同步的 `s_ui` 分配也复用本阶段的
Token、筛选与渲染器进入同一订阅；只读导入仍不会进入订阅。

## 节点与凭据资格

订阅不会直接读取 desired 配置。一个端点必须同时满足：

1. 用户未归档、已启用、未到期且全局额度未用尽；
2. 节点未归档、已启用、未处于 pending/degraded/disabled，且已配置公网端点；
3. 分配已启用、节点额度未用尽、状态为 applied；
4. `applied_version` 与 `desired_version` 相同；
5. `applied_credential_id` 指向状态为 applied 的凭据。

只解密最后一项 applied 凭据。staged 凭据不会进入订阅响应。

## 输出地址

创建或轮换 Token 后，控制面只在该次响应显示完整 Token 和地址：

| 格式 | 地址 |
| --- | --- |
| 默认 Base64 | `/sub/{token}` |
| Hysteria2 URI | `/sub/{token}/uri` |
| Base64 | `/sub/{token}/base64` |
| Clash Meta | `/sub/{token}/clash` |
| sing-box | `/sub/{token}/sing-box` |

Base64 的原文是以换行分隔的代理 URI。Mihomo/Clash 输出可直接导入的完整配置：

- `PolyFleet` 手动选择组、`自动选择` 延迟测试组和 `DIRECT` 兜底；
- Hysteria2 与 VLESS/TCP/Reality 均显式启用 UDP，VLESS 使用 `xudp` packet encoding；
- fake-IP DNS、域名嗅探和默认关闭的 TUN 基线；
- 内置私网及常用国内域名直连规则，其他流量由 `MATCH,PolyFleet` 接管。

默认规则不引用 `GEOSITE`、`GEOIP` 或远程 rule provider，因此全新的 Mihomo 数据目录不需要
先从 GitHub 下载规则数据库才能加载订阅。内置国内域名是可靠启动基线，不是完整的中国大陆
分流库；未命中的域名优先走代理。需要代理系统代理无法接管的应用、QUIC、游戏或虚拟网卡
流量时，在 Clash Verge 中开启全局 TUN。订阅文件保留 `tun.enable: false`，避免导入后在没有
管理员权限的设备上静默改变网络栈。

sing-box 输出顶层 `outbounds`。字段分别遵循
[Hysteria2 URI Scheme](https://v2.hysteria.network/docs/developers/URI-Scheme/)、
[Mihomo Hysteria2](https://wiki.metacubex.one/en/config/proxies/hysteria2/) 和
[sing-box Hysteria2 outbound](https://sing-box.sagernet.org/configuration/outbound/hysteria2/)。

从 `v1.1.2` 起，自签名证书可配置两类 Pin。证书指纹会进入 Hysteria2 URI 的
`pinSHA256` 和 Mihomo 的 `fingerprint`；Base64 公钥哈希会进入 sing-box 的
`tls.certificate_public_key_sha256`。两者不是同一个值，不能互相替代。自签名节点应同时
启用“跳过证书验证”和对应客户端的 Pin；只有 `insecure` 而没有 Pin 会暴露中间人攻击
风险。

所有订阅响应包含 `Cache-Control: no-store`。访问日志把完整路径改写为
`/sub/[redacted]/格式`，不会记录 Token。数据库只保存 Token 的 SHA-256
和短前缀；关闭一次性对话框后无法恢复旧地址，只能轮换 Token。

Mihomo/Clash 响应同时返回 `Subscription-Userinfo`、`Profile-Update-Interval` 和
`Profile-Web-Page-Url`。客户端可据此显示上传、下载、总额度、剩余流量与到期时间。
`upload` 与 `download` 都参与额度计算；用户额度为 `0` 时响应中的 `total=0`，表示不限额，
客户端会显示“无限”而不是一个剩余数值。要验收剩余流量显示，应先在用户页设置非零全局
流量额度，再刷新订阅。

## 两种轮换

### 订阅 Token 轮换

Token 轮换立即替换哈希。旧订阅地址立即返回 404，新地址立即可用。撤销是幂等操作；
已撤销或已到期的 Token 不允许轮换，应创建新 Token。

### Hysteria2 凭据轮换

凭据轮换先生成 encrypted staged 凭据并创建新 desired snapshot。Agent 尚未确认时：

- 节点继续使用旧 applied 凭据处理现有连接；
- 新 staged 凭据只在一次性管理响应中展示；
- 该端点暂时从随后拉取的订阅中省略。

Agent 确认 snapshot 后，事务会将新凭据提升为 applied、旧凭据标记为 retired，
订阅才开始输出新密码。存在其他 pending/failed 变更时，轮换返回 409，避免多次轮换
覆盖尚未应用的状态。用户级全部轮换是一个控制面事务，任一分配不满足条件时不会生成
任何新凭据。

## 升级与配置

先部署 `v0.4.0-dev`：

```powershell
.\scripts\deploy-fleet.ps1 -Version v0.4.0-dev
```

Server 首次启动会自动应用 `0005_unified_subscriptions.sql`。升级前仍应备份控制面数据库、
WAL 和 master key；丢失 master key 后无法生成订阅或轮换凭据。

部署完成后在“节点 → 编辑”中为 LisaHost 填写：

- 证书对应的公网域名或 IP；
- Hysteria2 对外 UDP 端口；
- 需要覆盖时填写 TLS SNI；
- 只有自签名或明确需要时才启用“跳过证书验证”。
- 自签名节点填写证书指纹；需要 sing-box 输出时另外填写 Base64 公钥哈希。

保存后等待节点和分配重新显示“已同步”，再进入用户详情创建订阅 Token。可用占位符进行
服务端检查，不要把真实地址写入 shell 历史、工单或 Git：

```bash
curl -fsS -D /tmp/hyfleet-subscription.headers \
  'https://panel.example.com/sub/<token>/uri' -o /tmp/hyfleet-subscription.txt
grep -i '^cache-control: no-store' /tmp/hyfleet-subscription.headers
rm -f /tmp/hyfleet-subscription.headers /tmp/hyfleet-subscription.txt
```

## 阶段验收

- Token 明文和完整订阅 URL 不出现在数据库列表、普通管理 API、错误或访问日志中；
- 无效、到期和撤销 Token 均无法获取订阅，撤销重复执行保持成功；
- URI 对密码、节点名、IPv4/IPv6 和 SNI 正确转义；YAML/JSON 可被结构化解析；
- disabled/expired/quota-limited 用户不会获取订阅；
- disabled/pending/failed 分配与未配置公网端点的节点不会输出；
- 凭据轮换 pending 期间不发布 staged 密码，Agent 确认后只发布新 applied 密码；
- 空订阅在四种格式下仍返回合法文档；
- 桌面和移动端均可配置端点、管理 Token 和观察轮换等待状态。
