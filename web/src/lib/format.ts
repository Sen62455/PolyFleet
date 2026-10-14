import type { AdapterType, NodeRecord, NodeStatus } from "../types";

const byteUnits = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(value: number, digits = 1): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), byteUnits.length - 1);
  const scaled = value / 1024 ** index;
  const precision = scaled >= 100 || index === 0 ? 0 : digits;
  return `${scaled.toFixed(precision)} ${byteUnits[index]}`;
}

export function formatRate(bitsPerSecond: number): string {
  if (!Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return "0 bps";
  const units = ["bps", "Kbps", "Mbps", "Gbps"];
  const index = Math.min(Math.floor(Math.log(bitsPerSecond) / Math.log(1000)), units.length - 1);
  const scaled = bitsPerSecond / 1000 ** index;
  return `${scaled.toFixed(scaled >= 100 ? 0 : 1)} ${units[index]}`;
}

export function percent(used: number, total: number): number {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return 0;
  return Math.max(0, Math.min(100, (used / total) * 100));
}

export function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return "0%";
  return `${Math.max(0, Math.min(100, value)).toFixed(value >= 10 ? 0 : 1)}%`;
}

export function quotaPercent(used: number, limit: number): number {
  return limit > 0 ? percent(used, limit) : 0;
}

export function relativeTime(value: string | null, now = Date.now()): string {
  if (!value) return "尚未连接";
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return "时间未知";
  const seconds = Math.max(0, Math.round((now - time) / 1000));
  if (seconds < 10) return "刚刚";
  if (seconds < 60) return `${seconds} 秒前`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return `${days} 天 ${hours} 小时`;
  const minutes = Math.floor((seconds % 3600) / 60);
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`;
  return `${minutes} 分钟`;
}

export function formatDateTime(value: string | null, neverLabel = true): string {
  if (!value) return neverLabel ? "永不过期" : "-";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "时间未知";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date);
}

export const adapterLabels: Record<AdapterType, string> = {
  native_hysteria2: "原生 Hysteria2",
  sing_box_vless_reality: "VLESS + Reality（sing-box）",
  standalone_sing_box: "独立 sing-box",
  s_ui: "S-UI",
};

export const statusLabels: Record<NodeStatus, string> = {
  pending: "待连接",
  online: "在线",
  stale: "数据延迟",
  offline: "离线",
  degraded: "异常",
  disabled: "已停用",
};

export function issueCount(nodes: NodeRecord[]): number {
  return nodes.filter((node) => ["stale", "offline", "degraded"].includes(node.status)).length;
}

// Shared presentation thresholds for resource and quota meters.
export function severity(value: number): "normal" | "warning" | "critical" {
  return value >= 90 ? "critical" : value >= 75 ? "warning" : "normal";
}

export function progressStatus(value: number): "default" | "warning" | "error" {
  const level = severity(value);
  return level === "critical" ? "error" : level === "warning" ? "warning" : "default";
}

const operationErrorLabels: Record<string, string> = {
  operation_invalid: "操作参数或类型无效，请查看原始原因",
  operation_expired: "操作在 Agent 领取前已过期",
  ping_failed: "目标没有响应 Ping",
  core_inactive: "代理核心未运行",
  core_log_failed: "读取核心日志失败",
  core_config_not_configured: "该节点尚未配置核心配置文件路径",
  config_backup_failed: "备份配置失败",
  core_restart_failed: "重启核心失败",
};

export function operationErrorText(code?: string, message?: string): string {
  if (!code) return message || "";
  if (code === "operation_invalid" && /operation type is unsupported/i.test(message || "")) {
    return "该节点的 Agent 不支持此操作，请升级 Agent 后重试";
  }
  if (operationErrorLabels[code]) return operationErrorLabels[code];
  if (code.startsWith("reality_")) return "Reality 配置处理失败，请查看原始原因";
  return message || code;
}

export function isHistoricalFailure(status: string, timestamp: string | null, now = Date.now()): boolean {
  return (status === "failed" || status === "expired") && !!timestamp && now - Date.parse(timestamp) > 7 * 86400000;
}
