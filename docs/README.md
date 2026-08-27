# PolyFleet 文档索引

本索引区分“当前参考文档”和“历史实现记录”。部署或排障时应优先使用当前参考文档；
历史记录保留设计演进和兼容背景，但其中的旧版本号、主机角色和命令示例不一定适用于最新
版本。

## 从这里开始

- [新手部署与使用](quick-start.zh-CN.md)：从域名、HTTPS、管理员初始化到节点、用户、
  订阅、流量额度、在线状态、踢下线、更新与排障。
- [从 HyFleet 原地迁移](migration-from-hyfleet.zh-CN.md)：兼容运行时名称、备份、升级
  顺序、跨控制面节点并入与回滚。
- [项目概览](project-overview.zh-CN.md)：需求、边界、技术选型、架构、数据流、安全、
  部署运维、测试和发布。
- [兼容性矩阵](compatibility.md)：受支持的操作系统、架构、部署模式和适配器能力。
- [原生节点切换与旧核心退役](native-cutover-runbook.zh-CN.md)：从临时 UDP 端口切换到
  UDP 443，验证订阅，观察后安全清理旧服务，并保留明确回滚路径。
- [安全策略](../SECURITY.md)：受支持版本、漏洞报告方式和关键安全边界。
- [贡献指南](../CONTRIBUTING.md)：本地工具链、必跑检查和变更要求。

## 产品与架构

- [产品需求基线](00-product-requirements.md)
- [系统架构](01-system-architecture.md)
- [领域与数据模型](02-domain-and-data-model.md)
- [Agent 协议](03-agent-protocol.md)
- [安全威胁模型](04-security-threat-model.md)
- [部署与资源预算](05-deployment-and-resource-budget.md)
- [部署清单示例](inventory.example.yaml)

以上文档记录了 v1 架构约束。若其中的旧部署拓扑与项目概览冲突，以当前代码、兼容性矩阵
和项目概览为准；安全约束只允许收紧，不应因历史记录而放宽。

## 功能与运维参考

- [原生节点切换与旧核心退役](native-cutover-runbook.zh-CN.md)
- [原生 systemd 部署](10-systemd-deployment.md)
- [原生 Hysteria2 用户与缓存鉴权](11-phase-2-native-users.md)
- [流量、在线状态、额度与批量更新](12-phase-3-traffic-and-updates.md)
- [统一订阅与凭据轮换](13-phase-4-unified-subscriptions.md)
- [S-UI 兼容适配器](14-phase-5-sui-adapter.md)
- [有限运维、恢复、备份与告警](15-phase-6-operations.md)
- [订阅运营、流量报表、通知与 VPS 资产](18-operations-layer.zh-CN.md)
- [安装、升级、校验和与灾难恢复](16-phase-7-public-release.md)

这些文件保留了相应功能首次交付时的验收细节。执行命令前，应把示例版本替换为准备安装
的 Release，并先检查该 Release 的说明和 `compatibility.md`。

## 架构决策记录

- [ADR 索引](adr/README.md)
- [控制面与出站 Agent](adr/0001-control-plane-and-agent.md)
- [Go、Vue 与 SQLite WAL](adr/0002-go-vue-sqlite-stack.md)
- [轮询与最终一致性](adr/0003-polling-and-eventual-consistency.md)
- [Agent 侧适配器](adr/0004-agent-side-adapters.md)
- [凭据与幂等流量记账](adr/0005-credentials-and-accounting.md)

## 历史实现记录

以下文件仅用于追溯项目如何从需求盘点逐步达到 v1，不再作为当前安装入口：

- [开发规划](07-development-stages.md)
- [初始设计评审](08-phase-0-review.md)
- [基础框架验收记录](09-phase-1-foundation.md)
- [早期 VPS 盘点](06-vps-inventory.md)
- [原生节点收敛与主机监控交付记录](17-native-convergence-and-monitoring.md)

公开问题、截图和配置示例必须使用合成数据。不得提交真实 IP、私有域名、密码、Token、
订阅 URL、证书私钥、数据库或未脱敏的节点配置。
