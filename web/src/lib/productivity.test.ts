import { describe, expect, it } from "vitest";
import { assetCSV, assetRows, daysUntilExpiry, matchesRenewal, renewalCalendar } from "../features/assets/assets";
import { trafficReportCSV } from "../features/reports/reportExport";
import type { NodeAssetRecord, NodeRecord, TrafficReport } from "../types";
import { makeCSV } from "./export";
import { dashboardHash, parseDashboardHash, type DashboardRoute } from "./navigation";

const node = (id: string, name = id) => ({ id, name, provider: "Example", region: "Example region", public_host: "192.0.2.1", public_port: 443, status: "online" }) as NodeRecord;
const asset = (id: string, expires: Date | null) => ({ node_id: id, plan: "VPS", expires_at: expires?.toISOString() ?? null, purchased_at: null, renewal_cycle_months: 0, auto_renew: false, notes: "" }) as NodeAssetRecord;

describe("renewal dates and exports", () => {
  it("uses calendar days across midnight, month and DST boundaries", () => {
    const now = new Date(2026, 2, 8, 23, 59);
    expect(daysUntilExpiry(new Date(2026, 2, 8, 0).toISOString(), now)).toBe(0);
    expect(daysUntilExpiry(new Date(2026, 2, 9, 0).toISOString(), now)).toBe(1);
    expect(daysUntilExpiry(new Date(2026, 2, 1).toISOString(), new Date(2026, 1, 28))).toBe(1);
    expect(daysUntilExpiry(null, now)).toBeNull();
    expect(daysUntilExpiry("invalid", now)).toBeNull();
  });

  it("keeps overdue, upcoming and missing dates distinct at filter boundaries", () => {
    const days = [null, -1, 0, 7, 8, 30, 31];
    expect(days.filter((day) => matchesRenewal(day, "overdue"))).toEqual([-1]);
    expect(days.filter((day) => matchesRenewal(day, "7d"))).toEqual([0, 7]);
    expect(days.filter((day) => matchesRenewal(day, "30d"))).toEqual([0, 7, 8, 30]);
    expect(days.filter((day) => matchesRenewal(day, "missing"))).toEqual([null]);
  });

  it("includes nodes without asset records and orders overdue first, missing last", () => {
    const records = { past: asset("past", new Date(2026, 9, 11)), soon: asset("soon", new Date(2026, 9, 13)) };
    const rows = assetRows([node("missing"), node("soon"), node("past")], records, new Date(2026, 9, 12));
    expect(rows.map((row) => row.node.id)).toEqual(["past", "soon", "missing"]);
    expect(assetCSV(rows)).toContain('"未设置日期"');
    expect(assetCSV(rows)).toContain('"已过期 1 天","0","否"');
  });

  it("preserves Chinese, embedded quotes, commas and newlines in CSV", () => {
    expect(makeCSV([["节点", '套餐,"高级"\n备注', 0, null]])).toBe('\uFEFF"节点","套餐,""高级""\n备注","0",""\r\n');
  });

  it("neutralizes spreadsheet formulas in editable text without changing numbers", () => {
    expect(makeCSV([["=1+1", " +SUM(A1)", "\t@name", "-2", -2, "normal"]]))
      .toBe('\uFEFF"\'=1+1","\' +SUM(A1)","\'\t@name","\'-2","-2","normal"\r\n');
  });

  it("exports local all-day dates with exclusive end, stable UIDs and reminders", () => {
    const nodes = [node("year-end", "示例节点"), node("past"), node("unknown")];
    const records = { "year-end": asset("year-end", new Date(2026, 11, 31)), past: asset("past", new Date(2026, 11, 29)) };
    const now = new Date(2026, 11, 31, 14);
    const rows = assetRows(nodes, records, now);
    const text = renewalCalendar(rows, "https://console.example/#/assets", now).replace(/\r\n /g, "");
    expect(text.match(/BEGIN:VEVENT/g)).toHaveLength(1);
    expect(text).toContain("DTSTART;VALUE=DATE:20261231\r\nDTEND;VALUE=DATE:20270101");
    expect(text).toContain("UID:polyfleet-node-year-end@polyfleet.local");
    expect(text).toContain("TRIGGER:-P7D");
    expect(text).toContain("URL:https://console.example/#/nodes/year-end?from=assets");
  });

  it("escapes calendar text and folds UTF-8 lines without splitting characters", () => {
    const n = node("unicode", "示例节点,;\\\n".repeat(15));
    const now = new Date(2026, 9, 12);
    const text = renewalCalendar(assetRows([n], { unicode: asset("unicode", now) }, now), "https://console.example/", now);
    const encoder = new TextEncoder();
    for (const line of text.split("\r\n")) expect(encoder.encode(line).length).toBeLessThanOrEqual(75);
    expect(text.replace(/\r\n /g, "")).toContain("示例节点\\,\\;\\\\\\n");
    expect(text).not.toContain("\uFFFD");
  });
});

describe("console links", () => {
  it("round-trips page, encoded node and operations links", () => {
    const routes: DashboardRoute[] = [{ view: "assets" }, { view: "reports" }, { view: "node-detail", nodeId: "node / 示例#1", from: "assets" }, { view: "operations", nodeId: "n&1" }];
    for (const route of routes) expect(parseDashboardHash(dashboardHash(route))).toEqual(route);
  });

  it("recovers from old anchors, unknown routes, bad escaping and invalid return pages", () => {
    expect(parseDashboardHash("")).toEqual({ view: "overview" });
    expect(parseDashboardHash("#reports")).toEqual({ view: "reports" });
    expect(parseDashboardHash("#/unknown")).toEqual({ view: "overview" });
    expect(parseDashboardHash("#/nodes/%zz")).toEqual({ view: "nodes" });
    expect(parseDashboardHash("#/nodes/node-1?from=https://example.test")).toEqual({ view: "node-detail", nodeId: "node-1", from: "nodes" });
  });
});

describe("traffic report CSV", () => {
  it("exports the loaded range, raw bytes and only supplied ranking rows", () => {
    const report = { range: "7d", from: "2026-10-05T00:00:00Z", to: "2026-10-12T00:00:00Z", daily: [{ bucket_at: "2026-10-11T00:00:00Z", upload_bytes: 10, download_bytes: 20 }], top_users: [{ id: "u1", name: "=Example", upload_bytes: 2, download_bytes: 3, total_bytes: 5 }], top_nodes: [] } as unknown as TrafficReport;
    expect(trafficReportCSV(report, "daily")).toContain('"7d","2026-10-05T00:00:00Z","2026-10-12T00:00:00Z","2026-10-11T00:00:00Z","10","20","30"');
    expect(trafficReportCSV(report, "users")).toContain('"1","u1","\'=Example","2","3","5"');
    expect(trafficReportCSV(report, "nodes").trim().split("\r\n")).toHaveLength(1);
  });
});
