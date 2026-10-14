import { localDate, makeCSV } from "../../lib/export";
import { statusLabels } from "../../lib/format";
import { dashboardHash } from "../../lib/navigation";
import type { NodeAssetRecord, NodeRecord } from "../../types";

export type RenewalFilter = "all" | "overdue" | "7d" | "30d" | "missing";
export interface AssetRow {
  node: NodeRecord;
  asset: NodeAssetRecord | null;
  days: number | null;
}

export function daysUntilExpiry(value: string | null | undefined, now = new Date()): number | null {
  if (!value) return null;
  const expiry = new Date(value);
  if (!Number.isFinite(expiry.getTime())) return null;
  // Compare local calendar dates; DST days can be shorter or longer than 24h.
  const calendarDay = (date: Date) => Date.UTC(date.getFullYear(), date.getMonth(), date.getDate());
  return Math.round((calendarDay(expiry) - calendarDay(now)) / 86_400_000);
}

export function assetRows(nodes: NodeRecord[], assets: Record<string, NodeAssetRecord>, now = new Date()): AssetRow[] {
  return nodes.map((node) => ({ node, asset: assets[node.id] ?? null, days: daysUntilExpiry(assets[node.id]?.expires_at, now) }))
    .sort((a, b) => (a.days ?? Infinity) - (b.days ?? Infinity) || a.node.name.localeCompare(b.node.name, "zh-CN"));
}

export function matchesRenewal(days: number | null, filter: RenewalFilter): boolean {
  if (filter === "all") return true;
  if (filter === "missing") return days === null;
  if (days === null) return false;
  if (filter === "overdue") return days < 0;
  return days >= 0 && days <= (filter === "7d" ? 7 : 30);
}

export function expiryLabel(days: number | null): string {
  if (days === null) return "未设置日期";
  if (days < 0) return `已过期 ${Math.abs(days)} 天`;
  if (days === 0) return "今天到期";
  return `${days} 天后到期`;
}

export function expiryTone(days: number | null): "neutral" | "critical" | "pressure" {
  return days === null ? "neutral" : days <= 7 ? "critical" : days <= 30 ? "pressure" : "neutral";
}

export function assetCSV(rows: AssetRow[]): string {
  return makeCSV([
    ["节点 ID", "节点名称", "地址", "端口", "供应商", "地区", "节点状态", "套餐 / 规格", "购买日期", "到期日期", "剩余天数", "到期状态", "续费周期（月）", "自动续费（记录）", "备注"],
    ...rows.map(({ node, asset, days }) => [
      node.id, node.name, node.public_host, node.public_port, node.provider, node.region, statusLabels[node.status],
      asset?.plan, localDate(asset?.purchased_at), localDate(asset?.expires_at), days, expiryLabel(days),
      asset?.renewal_cycle_months, asset ? (asset.auto_renew ? "是" : "否") : "未登记", asset?.notes,
    ]),
  ]);
}

function calendarText(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/\r\n|\r|\n/g, "\\n").replace(/;/g, "\\;").replace(/,/g, "\\,");
}

function foldLine(line: string): string {
  const encoder = new TextEncoder();
  let result = "";
  let bytes = 0;
  for (const character of line) {
    const size = encoder.encode(character).length;
    if (bytes + size > 75) {
      result += "\r\n ";
      bytes = 1;
    }
    result += character;
    bytes += size;
  }
  return result;
}

export function renewalCalendar(rows: AssetRow[], baseURL: string, now = new Date()): string {
  const stamp = now.toISOString().replace(/[-:]/g, "").replace(/\.\d{3}/, "");
  const lines = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//PolyFleet//VPS Renewals//ZH-CN", "CALSCALE:GREGORIAN", "X-WR-CALNAME:PolyFleet VPS 续费"];
  for (const { node, asset } of rows) {
    const days = daysUntilExpiry(asset?.expires_at, now);
    if (!asset?.expires_at || days === null || days < 0) continue;
    const end = new Date(asset.expires_at);
    end.setDate(end.getDate() + 1);
    const description = [
      `供应商：${node.provider || "未登记"}`,
      `地区：${node.region || "未登记"}`,
      `套餐：${asset.plan || "未登记"}`,
      `自动续费（记录）：${asset.auto_renew ? "是" : "否"}`,
    ].join("\n");
    lines.push(
      "BEGIN:VEVENT",
      `UID:polyfleet-node-${encodeURIComponent(node.id)}@polyfleet.local`,
      `DTSTAMP:${stamp}`,
      `DTSTART;VALUE=DATE:${localDate(asset.expires_at).replace(/-/g, "")}`,
      `DTEND;VALUE=DATE:${localDate(end).replace(/-/g, "")}`,
      `SUMMARY:${calendarText(`VPS 到期 · ${node.name}`)}`,
      `DESCRIPTION:${calendarText(description)}`,
      `URL:${baseURL.split("#")[0]}${dashboardHash({ view: "node-detail", nodeId: node.id, from: "assets" })}`,
      "TRANSP:TRANSPARENT",
      "BEGIN:VALARM", "TRIGGER:-P7D", "ACTION:DISPLAY",
      `DESCRIPTION:${calendarText(`VPS 将于 7 天后到期：${node.name}`)}`,
      "END:VALARM", "END:VEVENT",
    );
  }
  lines.push("END:VCALENDAR");
  return lines.map(foldLine).join("\r\n") + "\r\n";
}
