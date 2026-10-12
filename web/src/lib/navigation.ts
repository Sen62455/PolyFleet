export const consolePages = [
  { key: "overview", label: "总览", group: "监控", keywords: "overview dashboard 首页 监控" },
  { key: "nodes", label: "节点", group: "监控", keywords: "nodes servers VPS 主机" },
  { key: "users", label: "用户", group: "管理", keywords: "users 账号 配额" },
  { key: "assets", label: "资产与续费", group: "管理", keywords: "assets renewal 到期 日历 档案" },
  { key: "exits", label: "出口代理", group: "管理", keywords: "exits proxy 落地" },
  { key: "subscriptions", label: "订阅运营", group: "运营", keywords: "subscriptions 订阅" },
  { key: "reports", label: "流量报表", group: "运营", keywords: "reports traffic 用量 导出" },
  { key: "notifications", label: "告警通知", group: "运营", keywords: "notifications alerts 通知规则" },
  { key: "operations", label: "操作记录", group: "审计", keywords: "operations logs 审计 日志" },
] as const;

export type PageView = (typeof consolePages)[number]["key"];
export type DashboardView = PageView | "node-detail";
export type DashboardRoute =
  | { view: Exclude<PageView, "operations"> }
  | { view: "operations"; nodeId?: string }
  | { view: "node-detail"; nodeId: string; from: PageView };

function isPage(value: string): value is PageView {
  return consolePages.some((page) => page.key === value);
}

export function parseDashboardHash(hash: string): DashboardRoute {
  const [path, query = ""] = hash.replace(/^#\/?/, "").split("?");
  const parts = path.split("/");
  const params = new URLSearchParams(query);
  if (parts.length === 2 && parts[0] === "nodes" && parts[1]) {
    try {
      const nodeId = decodeURIComponent(parts[1]);
      if (!nodeId.trim()) return { view: "nodes" };
      const from = params.get("from") ?? "nodes";
      return { view: "node-detail", nodeId, from: isPage(from) ? from : "nodes" };
    } catch {
      return { view: "nodes" };
    }
  }
  if (parts.length === 1 && isPage(parts[0])) {
    if (parts[0] === "operations") {
      return { view: "operations", nodeId: params.get("node") || undefined };
    }
    return { view: parts[0] };
  }
  return { view: "overview" };
}

export function dashboardHash(route: DashboardRoute): string {
  if (route.view === "node-detail") {
    return `#/nodes/${encodeURIComponent(route.nodeId)}?from=${route.from}`;
  }
  if (route.view === "operations" && route.nodeId) {
    return `#/operations?node=${encodeURIComponent(route.nodeId)}`;
  }
  return `#/${route.view}`;
}
