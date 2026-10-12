import { makeCSV } from "../../lib/export";
import type { TrafficReport } from "../../types";

export type ReportExport = "daily" | "users" | "nodes";

export function trafficReportCSV(report: TrafficReport, kind: ReportExport): string {
  const metadata = [report.range, report.from, report.to];
  const prefix = ["报表范围", "统计开始（ISO 8601）", "统计结束（ISO 8601）"];
  if (kind === "daily") {
    return makeCSV([
      [...prefix, "采样日期（ISO 8601）", "上行（字节）", "下行（字节）", "双向总量（字节）"],
      ...report.daily.map((point) => [...metadata, point.bucket_at, point.upload_bytes, point.download_bytes, point.upload_bytes + point.download_bytes]),
    ]);
  }
  const ranks = kind === "users" ? report.top_users : report.top_nodes;
  return makeCSV([
    [...prefix, "排名", kind === "users" ? "用户 ID" : "节点 ID", "名称", "上行（字节）", "下行（字节）", "双向总量（字节）"],
    ...ranks.map((item, index) => [...metadata, index + 1, item.id, item.name, item.upload_bytes, item.download_bytes, item.total_bytes]),
  ]);
}
