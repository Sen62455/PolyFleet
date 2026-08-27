# VLESS Reality 实验阶段验证记录

> 本文是功能进入稳定版之前的历史实验记录。Reality 已在 PolyFleet `v1.3.0`
> 晋升为固定配置的受管适配器；当前部署与使用请阅读
> [PolyFleet 新手部署与使用](quick-start.zh-CN.md)。以下分支、手工构建和实验退出步骤
> 仅用于追溯，不再是当前安装流程。

本文只适用于 `experimental/vless-reality-singbox` 分支。该功能尚未进入
HyFleet 稳定产品范围，不能把本手册当作生产部署或稳定升级承诺。

本手册使用标准的单 Agent 布局：`hyfleet-agent.service`、
`/run/hyfleet-agent-ops.sock`、`/etc/hyfleet/agent.yaml` 和
`/var/lib/hyfleet-agent`。它不创建、修改或约定任何并行 lab Agent、第二
socket 或带 `-lab` 后缀的服务。已经注册过其他 adapter 的标准 Agent 不能
原地切换为 Reality；请使用尚未注册 HyFleet Agent 的测试主机。

同一 Reality core、配置路径和 identity 不能同时交给两个控制面。Agent 的
`server_url` 指向生产控制面时，实验控制面显示“未连接”是所有权隔离的预期结果，不是
心跳故障。不要为了让两个面板同时在线而复制 credentials、Agent 数据库或 systemd unit；
需要并行实验时应使用另一台 VPS，或使用完全隔离的第二个 sing-box 实例、配置、端口、
identity、Agent 状态目录和 helper socket。

## 固定实验契约

Reality adapter 的可执行边界不能通过安装参数调整：

| 项目 | 固定值 |
| --- | --- |
| 安装器参数 | `--adapter vless-reality` |
| Agent adapter | `sing_box_vless_reality` |
| sing-box | `/usr/bin/sing-box`，仅 HyFleet 构建 `1.13.18-hyfleet-utls1.8.7-api2` |
| systemd unit | `hyfleet-sing-box-reality.service` |
| core config | `/etc/sing-box/hyfleet-reality.json` |
| Reality identity | `/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json` |
| core 用户/组 | `hyfleet-singbox:hyfleet-singbox` |

HyFleet 安装器不会下载、升级或替换 sing-box。这里不能使用官方
`1.13.18`：实验固定使用 sing-box commit
`45ca32dcb966f07f97fc888fe8586e359dbe8405`，仅把
`github.com/metacubex/utls` 从 `v1.8.4` 升到 `v1.8.7`，以包含 Reality
服务端缓冲修正。构建工具链固定为 Go `1.26.5`。

在受信任的 Linux 构建机从仓库根目录运行：

```bash
bash scripts/build-sing-box-reality.sh
```

脚本只从固定 commit 构建 `amd64` 和 `arm64`，校验 uTLS module checksum、
`go mod verify`、ELF 架构、Go build metadata 以及
`deploy/sing-box-reality.sha256`。产物写入
`.codex-lab-build/sing-box-reality/`，不进入 Git 或 release archive。相同源码、
依赖、Go 版本与构建参数应与清单一致；不同 Go 版本不在字节复现承诺内。

按测试主机架构选择产物，人工核对 SHA-256 后，再以 root 安装普通 ELF 文件：

```bash
architecture=amd64 # arm64 主机改为 arm64
artifact="sing-box-1.13.18-hyfleet-utls1.8.7-api2-linux-${architecture}"
sha256sum ".codex-lab-build/sing-box-reality/${artifact}"
grep "  ${artifact}$" deploy/sing-box-reality.sha256
sudo install -o root -g root -m 0755 \
  ".codex-lab-build/sing-box-reality/${artifact}" /usr/bin/sing-box
```

安装前后确认：

```bash
test -f /usr/bin/sing-box
test ! -L /usr/bin/sing-box
/usr/bin/sing-box version
```

第一行必须精确为
`sing-box version 1.13.18-hyfleet-utls1.8.7-api2`。安装器还会按宿主架构读取 release
内的清单并重新校验 SHA-256、root 所有权和 ELF machine；仅伪造版本字符串
不能通过。主机需要 Debian 12、Debian 13 或 Ubuntu 24.04，使用 systemd，并能
从 Agent 通过受信任 TLS 访问控制面。预先选择一个未被占用的 TCP 测试端口；
不要把当前服务端口直接改为 TCP 443。

## 控制面准备

先部署同一实验分支构建的 Server，并在升级数据库前完成数据库备份。旧版
Server 或稳定分支 Agent 不理解 schema v2，不能参与本次测试。

在控制面新增节点时选择“VLESS + Reality（实验）”，填写测试主机的公网
地址、未占用的 TCP 端口、合法的 Reality SNI 和握手 DNS 名称。握手端口
固定为 443。保存节点后生成一次性 enrollment token；不要把 token 写进
脚本、聊天记录或 shell history。

## 安装标准 Agent

从同一实验分支的完整 release archive 中运行：

```bash
sudo bash deploy/install-agent.sh \
  --server-url https://panel.example.com \
  --node-name lisa-reality-test \
  --adapter vless-reality
```

按提示在终端中粘贴一次性 enrollment token。安装器将：

- 验证现有 `/usr/bin/sing-box` 的专用版本、SHA-256、所有权和架构，但不下载它；
- 创建非 root 的 `hyfleet-singbox` 系统用户与组；
- 固定写入 Reality adapter 的 unit、config、binary 和 identity 路径；
- 安装并启用专用 sing-box unit，但不在空配置状态下启动它；
- 启动标准 HyFleet Agent 与 root helper socket，完成注册。

可以显式重复固定 unit 或 config 参数，但其他值会被拒绝：

```bash
sudo bash deploy/install-agent.sh \
  --server-url https://panel.example.com \
  --node-name lisa-reality-test \
  --adapter vless-reality \
  --service-unit hyfleet-sing-box-reality.service \
  --core-config-path /etc/sing-box/hyfleet-reality.json
```

## 首次收敛

安装刚结束而控制面尚未下发 schema v2 时，以下状态是正常的：

```bash
systemctl is-active hyfleet-agent.service
systemctl is-enabled hyfleet-agent-ops.socket
systemctl is-enabled hyfleet-sing-box-reality.service
test ! -e /etc/sing-box/hyfleet-reality.json
```

前三项应分别返回 `active`、`enabled`、`enabled`；最后一项应成功。不要手工
创建占位 JSON、Reality 私钥或 short ID。

在控制面把至少一个测试用户分配给节点后，Agent 获取精确绑定当前 desired
version/hash 的 VLESS UUID，root helper 在节点本地生成 Reality identity，运行
`sing-box check`，原子发布配置，然后启动专用 unit。检查结果：

```bash
systemctl is-active hyfleet-sing-box-reality.service
systemctl status hyfleet-sing-box-reality.service --no-pager
stat -c '%U:%G %a %n' \
  /etc/sing-box \
  /etc/sing-box/hyfleet-reality.json \
  /var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json
ss -ltn
```

期望 core config 为 `root:hyfleet-singbox 640`，identity 为 `root:root 600`，
目标 TCP 端口处于 `LISTEN`。不要 `cat` core config 或 identity；两者包含
UUID 或私钥。helper 只有在 unit active、目标端口监听且 applied ledger 已
持久化后才发送成功 ACK。

## 客户端与回归验证

等待控制面显示 Reality public key、short ID 和已应用版本，再刷新测试用户
订阅。分别检查 URI/Base64、Mihomo/Clash 和 sing-box 输出；未 ACK 的 endpoint
不应出现。使用支持 VLESS、Reality、TCP 和 `xtls-rprx-vision` 的客户端连接，
确认 DNS、TLS 时间和主机防火墙允许所选 TCP 端口。

随后执行以下回归：

1. 重启 `hyfleet-agent.service`，确认 pending ACK 能继续发送且 Reality
   identity 不变化。
2. 重启 `hyfleet-sing-box-reality.service`，确认订阅仍可连接。
3. 暂时中断控制面连接，确认已运行的数据面不受影响；恢复后 Agent 继续收敛。
4. 在控制面修改到另一个未占用的高位 TCP 端口，确认新端口监听后才 ACK。
5. 用防火墙或端口冲突制造一次可恢复的健康检查失败，确认控制面报告失败且
   helper 恢复上一份配置；不要在仍承载其他服务的主机上执行此步骤。
6. 在节点运维区执行一次 Reality 身份轮换。确认提交后控制面分别显示上一代
   `applied_key_generation` 与下一代 `key_generation`，该节点立即从订阅中消失；
   Agent ACK 新 public key 与 short ID 后，两代重新一致且订阅恢复。旧订阅参数
   的新连接应失败，刷新订阅后的连接应成功。

托管配置默认设置 `log.disabled: true`，不记录 sing-box 连接级日志，避免 VLESS
UUID、用户标识或其他敏感材料进入日志。因此，节点运维区的 `tail_core_log` 和
下面的 core journal 主要用于查看 systemd 启停与失败等生命周期诊断。该限制不
影响 `sing-box check`、服务和监听健康检查、原子配置发布或失败回滚。

排障只查看经过边界化的状态和日志：

```bash
journalctl -u hyfleet-agent.service -b -n 100 --no-pager
journalctl -u 'hyfleet-agent-ops@*' -b -n 100 --no-pager
journalctl -u hyfleet-sing-box-reality.service -b -n 100 --no-pager
systemctl cat hyfleet-agent-ops@.service
```

helper 模板应使用 `ProcSubset=all`，以只读检查 `/proc/net/tcp` 和
`/proc/net/tcp6` 中的监听状态。它仍只允许 `AF_UNIX`，不会建立任意外部网络
连接。

## 备份、恢复与退出实验

Reality identity 只存在节点本地，控制面不能重建私钥。若需要磁盘故障后的
身份连续性，应使用 root-only、加密的离机备份同时保护 identity 与 core
config；备份介质和流程不得把内容写入日志或普通用户目录。丢失 identity 且
无备份时必须在控制面执行显式身份轮换并让所有客户端刷新订阅，不能让 Agent
静默生成替代身份。身份轮换管理请求固定为
`POST /api/v1/nodes/{nodeID}/reality/rotate-identity`，并同时提交当前
`expected_key_generation` 与 `expected_desired_version`；过期或重复请求必须
返回冲突，不能连续跳过代际。

退出实验前先在控制面禁用节点和用户分配，确认订阅不再发布该 endpoint，
再停止并禁用 `hyfleet-agent.service`、`hyfleet-agent-ops.socket` 和
`hyfleet-sing-box-reality.service`。保留 Agent state、identity、core config 和
数据库备份，直到完成结果复核；不要用通配符删除 `/etc/sing-box` 或
`/var/lib/hyfleet-agent-ops`。

实验结果至少记录：Server/Agent commit、sing-box 版本、主机发行版与架构、
测试端口、首次 apply、Agent/core 重启、控制面中断、订阅连接、失败回滚和
资源占用。只有 ADR 0006 的 promotion gates 全部通过后，才应讨论合入稳定
分支或建立新的多协议仓库。
