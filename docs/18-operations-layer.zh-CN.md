# 订阅运营、流量报表、通知与 VPS 资产

本文说明 `0014_operations_layer.sql` 引入的运营层。它面向单管理员、小型自托管
fleet，不是计费、收款或客户自助系统。升级前必须同时备份 Server SQLite 数据库和
master key；旧二进制不能保证读取升级后的数据库。

## 1. 订阅运营

控制台“订阅运营”页按 Token 展示用户、双向流量、额度、最近拉取时间、有效期、节点
分配和在线节点数。状态优先级依次为：已吊销、已到期、用户停用、额度用尽、七天内
到期、活跃。

- `GET /api/v1/subscriptions` 支持 `status`、`search`、`limit` 和 `offset`。
- `PATCH /api/v1/subscriptions/{tokenID}` 可改 Token 到期、用户到期、用户全局额度或
  吊销 Token。
- 到期字段传 RFC 3339 时间；传 `null` 表示清空。省略字段表示保持不变。
- 用户到期、用户额度、Token 到期和吊销在一个 SQLite 事务中提交。用户策略变化仍会
  递增受影响节点的 desired version，并按原有最终一致性流程下发。
- 页面中的流量额度属于用户，而不是某一个 Token。修改它会影响该用户的所有订阅和
  节点分配。

## 2. 流量报表

“流量报表”按 UTC 自然日提供 7 天或 30 天总量、上期对比、每日上下行轨迹、TOP 用户
和 TOP 节点。所有总量均为上传加下载的双向流量。

报表只使用控制面已经确认接收的幂等 `traffic_batches`。TOP 用户只统计能归属到受管用户
且 disposition 为 `accounted` 的明细；节点总量还包含该节点批次中的未归属流量。因此
TOP 用户之和可能小于节点或全局总量，这是预期行为。控制面或 Agent 离线时，报表会在
Outbox 补发后更新，不应作为逐字节实时计费凭证。

接口为 `GET /api/v1/reports/traffic?range=7d|30d&group_by=all`。`group_by` 也接受
`day`、`user`、`node` 作为兼容查询值；当前响应始终返回日序列和两组排行，避免前端为
同一时间窗重复查询。

## 3. 告警通知

“告警通知”支持 Telegram、Slack Incoming Webhook 和自定义 HTTPS Webhook。告警创建
和恢复在告警事务中写入持久化投递队列；后台每 15 秒处理到期项。失败后的退避为 1 分钟、
5 分钟、30 分钟、2 小时、6 小时，第 6 次失败进入最终失败。

- 通道密钥使用 Server master key 加密，API 只返回目标提示，不回传完整 URL 或 Bot
  Token。
- Webhook 只允许标准 443 端口的绝对 HTTPS URL，拒绝凭据、片段、重定向、localhost、
  私网、链路本地、保留地址和 DNS 解析到非公网地址的目标。
- Telegram 传输错误经过归一化，不会把含 Bot Token 的请求 URL写入数据库或响应。
- 停用通道会暂停其待投递项；删除通道会级联删除关联投递记录。
- “发送测试”是同步诊断请求，不进入告警重试队列。生产防火墙必须允许 Server 出站
  TCP 443 和 DNS。

管理接口：

- `GET /api/v1/settings/notifications`
- `PUT /api/v1/settings/notifications`
- `POST /api/v1/notifiers/{id}/test`
- `DELETE /api/v1/notifiers/{id}`

### 自动提醒、Bot 交互与消息文案

提醒规则支持周期运行概览、活动告警、VPS 到期和节点流量阈值。Telegram Bot 查询支持
`/status`、`/nodes`、`/node <节点名>`、`/help`，也可直接发送节点名。交互只接受通知
通道中配置的数字 Chat ID；其他会话会被忽略。

当前消息文案是后端代码模板，不在控制台中开放自由模板，避免未转义变量、超长消息或敏感
字段被误发。需要自行调整时，修改以下位置并重新构建 Server：

- `internal/server/notification_handlers.go` 的 `deliverNotification`：所有 Telegram、Slack
  通知外层前缀；
- `internal/server/reminder_handlers.go` 的 `renderNotificationReminder`：四类周期提醒正文；
- 同文件的 `telegramBotReply`：命令帮助、未知命令和命令路由；
- 同文件的 `renderTelegramNodeDetail`：单节点详情正文。

不要把 Bot Token、完整 Chat ID、订阅 Token 或 Agent 凭据写进文案。Telegram 文本在发送前
会限制长度；修改后至少运行 `go test ./internal/server -run 'Notification|Reminder'`。

## 4. VPS 资产与探针

节点卡片把 Agent 实时上报的系统、CPU、内存、Swap、磁盘、磁盘 I/O、负载、网络、核心
和在线连接，与管理员录入的套餐、购买时间、到期时间、续费周期、自动续费和备注合并显示。
VPS 服务商通常没有统一且可靠的查询 API，所以生命周期字段由管理员维护，不假装自动
获取。

`GET /api/v1/assets` 返回全部未归档节点及其资产档案；
`PUT /api/v1/nodes/{nodeID}/asset` 创建或更新档案。到期时间只产生可视提醒，不会自动
续费、关机或删除节点。

## 5. 分页与批量操作

用户列表的 `search`、`limit`、`offset` 在 Server 执行，节点分配只读取当前页用户，
避免用户和分配增长后每次拉取全表。告警列表支持 `node_id`、`type`、`status`、`limit`
和 `offset`。

节点目录可选择多台节点后执行核心探测、重启、配置备份或重试同步。接口
`POST /api/v1/nodes/bulk` 每次最多接受 50 个节点，去重后为每个节点分别进入现有有界
操作队列。它不是分布式事务：部分节点接受、部分节点失败时会逐项返回结果，管理员应在
操作记录中核对实际完成状态。

## 6. 升级验收

1. 备份数据库和 master key，升级 Server，确认迁移表包含
   `0014_operations_layer.sql`。
2. 登录后打开订阅运营、流量报表、告警通知和节点资产编辑，确认无 5xx。
3. 创建一个临时通知通道并发送测试；随后删除或停用测试通道。
4. 调整测试订阅的额度和日期，再清空日期，确认用户详情与订阅页一致。
5. 对实验节点执行批量“探测核心”，确认操作记录逐项完成。
6. 观察至少一个 Agent 流量采样周期，确认报表增长方向和节点明细一致。

不要通过制造生产节点离线、破坏配置或耗尽真实额度来验收通知。失败重试、回滚和私网
Webhook 拒绝由自动测试覆盖。

## 7. 本地 UI 预览

Windows 上安装 Go 1.26、Node.js 22 和 pnpm 11 后，在仓库根目录打开两个 PowerShell。
第一个终端启动一套与线上隔离的本地 Server：

```powershell
$env:HYFLEET_PUBLIC_URL = "http://127.0.0.1:5173"
$env:HYFLEET_BOOTSTRAP_TOKEN = "local-development-only"
go run ./cmd/server
```

它使用仓库内被 Git 忽略的 `data/server.db` 和 `data/master.key`。第二个终端启动前端：

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web dev
```

浏览器打开 `http://127.0.0.1:5173`。首次进入时使用上述 bootstrap token 创建本地管理员。
Vue 文件保存后 Vite 会自动热更新；停止时在两个终端分别按 `Ctrl+C`。

若只想让本地前端读取 DMIT2，可先建立到远端 `8080` 的 SSH 隧道，再设置
`VITE_DEV_PROXY_TARGET` 与 `VITE_DEV_PUBLIC_ORIGIN`。这会连接实验场真实数据，任何保存、
删除或运维操作都会作用于 DMIT2，因此日常改 UI 更推荐使用上面的隔离本地 Server。
