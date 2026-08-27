# 阶段 6：运维、恢复、配置备份与告警

## 交付范围

`v0.6.0-dev` 增加以下能力：

- Agent 恢复联网后继续拉取最新 desired state，并补发尚未确认的应用结果；
- 节点运维操作使用单调 sequence 和本地 SQLite 结果 Outbox；
- 核心状态探测、核心重启、最近日志和配置备份四种有限操作；
- 失败或过期操作的显式重试；
- 重启前备份，以及重启失败后的最近可用配置恢复；
- 节点离线、核心停止、流量采集、同步和运维失败告警；
- 告警确认与故障恢复后的自动关闭；
- S-UI 接管后的节点额度和统一订阅准入修复。

本阶段不是远程 Shell、SSH 跳板、文件管理器或通用 RMM。控制面不能提交命令、服务名
或任意文件路径。BandwagonHost 在本阶段可以进行状态、重启、有限日志、配置备份和告警，
但仍不管理它的 sing-box 用户，也不把它加入统一订阅。

## 操作链路

```text
管理员 API
  -> node_operations（每节点递增 sequence）
  -> Agent 拉取固定类型操作
  -> /run/hyfleet-agent-ops.sock
  -> root helper 执行白名单动作
  -> Agent SQLite operation_results
  -> 控制面幂等接收结果
  -> 操作历史、备份元数据和告警
```

Agent 先补发本地未上报结果，再拉取新操作。控制面暂时不可达时，已经执行的结果不会
丢失；Agent 重启后也会继续补发。helper 另有按 operation ID 保存的本地结果账本，重复
投递同一个操作不会重复重启核心或重复创建备份。

排队操作有 10 至 15 分钟有效期。超过有效期仍未被 Agent 领取的操作会标为“已过期”，
不会在节点很久以后突然执行。管理员可在面板显式重试，重试会生成新的 operation ID、
sequence 和 attempt。已经开始执行的操作不会因为控制面暂时断线而被自动过期。

## 权限边界

普通 `hyfleet-agent` 继续以专用低权限用户运行，并保持 `NoNewPrivileges=true`。只有
systemd socket 按需启动的 `hyfleet-agent-ops` helper 以 root 运行。helper 只接受：

| 操作 | 固定行为 | 上限 |
| --- | --- | --- |
| `probe_core` | `systemctl is-active` 配置中的固定 unit | 无参数 |
| `restart_core` | 备份、重启、健康检查，失败时恢复 | 45 秒 Agent deadline |
| `tail_core_log` | 固定 unit 的 `journalctl` | 200 行、32 KiB |
| `backup_config` | 复制配置中的固定文件或受限分片目录 | 解压后最大 8 MiB |

helper 不解释 shell，不接受命令字符串，也不能切换到其他 systemd unit。Hysteria2
配置必须位于 `/etc/hysteria`，sing-box 配置必须位于 `/etc/sing-box`；这与 systemd
helper 唯一允许回滚写入的目录一致。日志在 helper、Agent 和 Server 三层限制并脱敏，
避免常见 Token、密码、Authorization 和订阅链接进入控制面。

S-UI 的运行数据在 SQLite 中，在线复制数据库可能得到不一致快照，因此 S-UI 节点暂不
提供“配置备份”；它仍支持探测、重启和有限日志。

## 配置备份与回滚

原生 Hysteria2 和独立 sing-box 的备份保存在对应节点：

```text
/var/lib/hyfleet-backups       root:root 0700
  <time>-<operation-id>-<name>.bak     root:root 0600
  <time>-<operation-id>-<dir>.tar.gz   root:root 0600
/var/lib/hyfleet-agent-ops     root:root 0700
  <operation-id>.json              root:root 0600
```

v1.0 起，`core_config_path` 也可以指向 `/etc/sing-box` 下的实际配置目录。目录快照最多
512 个条目、16 层，拒绝符号链接和特殊文件，并在失败回滚时替换完整目录树。

控制面只保存备份路径、SHA-256、大小、时间和 operation ID，不上传配置正文。备份因此
不会把节点密钥集中复制到 DMIT，但也意味着恢复必须在原节点完成。

重启操作先记住此前最近的备份，再为当前配置创建一次重启前备份。如果重启或随后的
`is-active` 检查失败，helper 会恢复此前最近的配置并再次启动固定核心。操作结果中的
`rolled_back` 会显示“已恢复最近可用配置”。若节点从未有过旧备份，只能恢复本次重启前
的同一份配置；首次变更前应先在面板执行一次“配置备份”。

## 告警模型

控制面每 15 秒协调以下活动条件：

| 告警 | 条件 | 恢复 |
| --- | --- | --- |
| 节点离线 | 已注册启用节点超过离线阈值无心跳 | 心跳恢复 |
| 节点降级 | 节点状态为 degraded | 状态恢复 |
| 核心停止 | 最近有心跳但固定核心未运行 | 核心恢复 |
| 流量采集异常 | 已启用采集且 Agent 报错 | 正常采样 |
| 同步失败 | 至少一个分配应用失败 | 后续应用成功 |
| 同步超时 | desired 领先 applied 超过 5 分钟 | 版本追平 |
| 运维操作失败 | 有尚未被后续成功操作恢复的失败或过期操作 | 同类型后续操作成功 |

确认告警只表示管理员已经看到问题，不会掩盖状态，也不会执行恢复动作。条件消失后告警
自动转为 resolved；相同节点、相同类型的活动告警不会重复创建多条。

## DMIT 的阶段 5 验收修复

S-UI 只读导入仍有意禁止修改节点额度，也不会进入订阅，因为 HyFleet 不拥有远端凭据。
在 DMIT 节点详情完成以下流程后才会改变：

1. 保存目标 Hysteria2 入站；
2. 把远端客户端只读映射到全局用户；
3. 等待该映射显示“已同步”；
4. 显式执行“接管”，等待新的 desired version 被 Agent 确认；
5. 用户详情显示“已纳入订阅”，并出现 DMIT 节点额度输入框。

统一 Clash 订阅只输出 `subscription_eligible=true` 的 assignment。因此完成接管并同步后，
DMIT 会和 LisaHost 一起出现在代理列表与 `HyFleet` 选择组；只读、停用、额度用尽、缺少
公网端点或尚未应用的分配仍会被排除。

## BandwagonHost 初次接入

用户提供的是 Clash 客户端配置，它只能证明客户端如何连接，不能确定服务端 sing-box
用户结构或配置文件路径。先在 BandwagonHost 本机检查：

```bash
sudo systemctl cat sing-box.service
sudo systemctl status sing-box.service --no-pager -l
```

在面板创建 `Standalone sing-box` 节点并生成一次性 enrollment token。上传并校验
`v0.6.0-dev` 发布包后，在解压目录运行：

```bash
sudo bash deploy/install-agent.sh \
  --server-url https://panel.example.com \
  --node-name BandwagonHost \
  --adapter standalone-sing-box \
  --core-config-path /etc/sing-box/config.json
```

把域名和配置路径换成实际值。注册 token 只在安装器的无回显提示中输入，不应写进命令
历史。若 `systemctl cat` 显示其他 `-c` 或 `--config` 路径，必须使用该服务端路径。

安装后检查：

```bash
sudo systemctl is-active hyfleet-agent hyfleet-agent-ops.socket sing-box
sudo systemctl status hyfleet-agent-ops.socket --no-pager -l
sudo stat -c '%a %U:%G %n' \
  /var/lib/hyfleet-agent /var/lib/hyfleet-backups /var/lib/hyfleet-agent-ops
```

三个目录应分别是 Agent 自有 `0700`、root 自有 `0700`、root 自有 `0700`。先在面板执行
“探测核心”和“配置备份”，确认成功后再验收“重启核心”。本阶段不要尝试从客户端配置
推导或覆盖服务端用户。

## 从 v0.5 升级三台现有主机

先确保 GitHub Release 已包含 `v0.6.0-dev` 的压缩包与校验文件。已配置
`scripts/fleet.local.psd1` 时，在 Windows 项目根目录执行：

```powershell
.\scripts\deploy-fleet.ps1 -Version v0.6.0-dev
```

更新器会同时部署 Agent 和新增 helper/socket。每台主机在提交更新前都会检查 ELF 架构、
配置、systemd unit 和服务健康；任一组件失败会恢复该组件更新前的二进制和 unit。首次
接入的 BandwagonHost 仍需先用安装器完成 enrollment，之后才能加入 fleet 更新清单。

单机手工升级仍可在新发布包解压目录执行：

```bash
sudo bash deploy/update-component.sh server
sudo bash deploy/update-component.sh agent
```

DMIT 同时运行 Server 和 Agent，需要依次更新两个组件；LisaHost 和已注册的
BandwagonHost 只更新 Agent。

## 验收

1. 三台节点均显示在线，核心状态和本机 `systemctl is-active` 一致。
2. LisaHost 或 DMIT Agent 离线时修改一个受管用户，恢复 Agent 后无需再次编辑即可追平。
3. 控制面短暂不可达时完成的节点操作会保留在 Agent SQLite，并在恢复后显示结果。
4. 相同 operation ID 的 helper 请求不会重复执行；失败/过期记录可以生成新序列重试。
5. 日志不超过 200 行和 32 KiB，且不出现节点密码、Token 或完整订阅 URL。
6. 原生节点配置备份只保存在节点本地；控制面只显示元数据。
7. 停止测试节点 Agent 或核心会产生对应告警，恢复后告警自动关闭。
8. DMIT 接管后的 assignment 可以保存节点额度，并出现在 Clash 订阅代理列表中。

不要为了制造失败而故意破坏生产节点配置。重启失败回滚由 helper 单元测试覆盖；线上验收
使用一次正常备份和一次正常重启即可。

## 已知边界

- 告警已支持加密保存的 Telegram、Slack 和自定义 HTTPS Webhook 出站队列；邮件仍未实现；
- 备份没有自动保留策略和远端下载，需由管理员在节点侧管理磁盘；
- S-UI 在线数据库备份和恢复尚未实现；
- BandwagonHost 用户、流量与统一订阅需要后续提供脱敏后的服务端 sing-box 配置、实际
  配置路径和版本，再实现单独适配器；
- 控制面数据库和 master key 仍必须按部署指南成对做外部备份，本阶段没有新增远程恢复
  编排。
