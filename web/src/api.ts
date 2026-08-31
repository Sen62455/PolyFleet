import type {
  AssignUserResponse,
  AlertRecord,
  AssignmentInput,
  ConfigBackupRecord,
  CreateUserResponse,
  EnrollmentToken,
  NodeInput,
  NodeMetricSeries,
  NodeOperationRecord,
  NodeOperationType,
  NodeRecord,
  NodeTelemetrySnapshot,
  MetricRange,
  OperationFilters,
  OperationPage,
  Session,
  SetupStatus,
  SUIState,
  IssuedSubscriptionToken,
  SubscriptionTokenInput,
  SubscriptionTokenRecord,
  UserCredential,
  UserInput,
  UserRecord,
  BulkNodeAction,
  BulkNodeResult,
  NodeAssetInput,
  NodeAssetRecord,
  NotificationNotifierInput,
  NotificationNotifierRecord,
  NotificationSettings,
  NotificationReminderRuleInput,
  NotificationReminderRuleRecord,
  TelegramBotAccessRecord,
  SubscriptionOperationPage,
  SubscriptionOperationPatch,
  SubscriptionOperationRecord,
  TrafficReport,
} from "./types";

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;

  constructor(status: number, code: string, message: string, requestId = "") {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

let csrfToken = "";

function setSession(session: Session | null) {
  csrfToken = session?.csrf_token ?? "";
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body) {
    headers.set("Content-Type", "application/json");
  }
  if (!(["GET", "HEAD", "OPTIONS"].includes(method)) && csrfToken) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers,
  });
  if (!response.ok) {
    let code = "request_failed";
    let message = `请求失败（HTTP ${response.status}）`;
    let requestId = response.headers.get("X-Request-ID") ?? "";
    try {
      const payload = (await response.json()) as {
        error?: { code?: string; message?: string; request_id?: string };
      };
      code = payload.error?.code ?? code;
      message = payload.error?.message ?? message;
      requestId = payload.error?.request_id ?? requestId;
    } catch {
      // Preserve the generic HTTP error when the response is not JSON.
    }
    throw new APIError(response.status, code, message, requestId);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  setupStatus: () => request<SetupStatus>("/api/v1/setup/status"),

  async bootstrap(input: { bootstrap_token: string; username: string; password: string }) {
    const session = await request<Session>("/api/v1/setup/bootstrap", {
      method: "POST",
      body: JSON.stringify(input),
    });
    setSession(session);
    return session;
  },

  async login(input: { username: string; password: string }) {
    const session = await request<Session>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify(input),
    });
    setSession(session);
    return session;
  },

  async session() {
    const session = await request<Session>("/api/v1/auth/session");
    setSession(session);
    return session;
  },

  async logout() {
    await request<void>("/api/v1/auth/logout", { method: "POST", body: "{}" });
    setSession(null);
  },

  async listNodes() {
    const result = await request<{ nodes: NodeRecord[] }>("/api/v1/nodes");
    return result.nodes;
  },

  getNodeMetrics: (nodeId: string, range: MetricRange = "24h") =>
    request<NodeMetricSeries>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/metrics?range=${encodeURIComponent(range)}`,
    ),

  getNodeTelemetry: (nodeId: string) =>
    request<NodeTelemetrySnapshot>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/telemetry`,
    ),

  createNode: (input: NodeInput) =>
    request<NodeRecord>("/api/v1/nodes", { method: "POST", body: JSON.stringify(input) }),

  updateNode: (id: string, input: Required<NodeInput>) =>
    request<NodeRecord>(`/api/v1/nodes/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(input),
    }),

  calibrateNodeTraffic: (id: string, providerUsedBytes: number) =>
    request<NodeRecord>(`/api/v1/nodes/${encodeURIComponent(id)}/traffic-calibration`, {
      method: "POST",
      body: JSON.stringify({ provider_used_bytes: providerUsedBytes }),
    }),

  archiveNode: (id: string, force = false) =>
    request<void>(`/api/v1/nodes/${encodeURIComponent(id)}${force ? "?force=true" : ""}`, {
      method: "DELETE",
      body: "{}",
    }),

  createEnrollmentToken: (id: string) =>
    request<EnrollmentToken>(`/api/v1/nodes/${encodeURIComponent(id)}/enrollment-token`, {
      method: "POST",
      body: "{}",
    }),

  async listNodeOperations(nodeId: string, limit = 50) {
    const result = await request<{ operations: NodeOperationRecord[] }>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/operations?limit=${encodeURIComponent(limit)}`,
    );
    return result.operations;
  },

  listOperations: (filters: OperationFilters = {}) => {
    const query = new URLSearchParams();
    if (filters.node_id) query.set("node_id", filters.node_id);
    if (filters.type) query.set("type", filters.type);
    if (filters.status) query.set("status", filters.status);
    query.set("limit", String(filters.limit ?? 20));
    query.set("offset", String(filters.offset ?? 0));
    return request<OperationPage>(`/api/v1/operations?${query.toString()}`);
  },

  createNodeOperation: (nodeId: string, type: NodeOperationType, maxLines = 0, target = "") =>
    request<NodeOperationRecord>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/operations`, {
      method: "POST",
      body: JSON.stringify({ type, max_lines: maxLines, target }),
    }),

  retryNodeOperation: (nodeId: string, operationId: string) =>
    request<NodeOperationRecord>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/operations/${encodeURIComponent(operationId)}/retry`,
      { method: "POST", body: "{}" },
    ),

  retryNodeSync: (nodeId: string) =>
    request<NodeRecord>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/retry-sync`, {
      method: "POST",
      body: "{}",
    }),

  rotateRealityIdentity: (nodeId: string, expectedKeyGeneration: number, expectedDesiredVersion: number) =>
    request<NodeRecord>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/reality/rotate-identity`, {
      method: "POST",
      body: JSON.stringify({
        expected_key_generation: expectedKeyGeneration,
        expected_desired_version: expectedDesiredVersion,
      }),
    }),

  async listConfigBackups(nodeId: string) {
    const result = await request<{ backups: ConfigBackupRecord[] }>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/backups`,
    );
    return result.backups;
  },

  async listAlerts(status: "active" | "resolved" | "all" = "active") {
    const result = await request<{ alerts: AlertRecord[] }>(
      `/api/v1/alerts?status=${encodeURIComponent(status)}`,
    );
    return result.alerts;
  },

  listNodeAssets: async () => {
    const result = await request<{ assets: NodeAssetRecord[] }>("/api/v1/assets");
    return result.assets;
  },

  updateNodeAsset: (nodeId: string, input: NodeAssetInput) =>
    request<NodeAssetRecord>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/asset`, {
      method: "PUT",
      body: JSON.stringify(input),
    }),

  listSubscriptionOperations: (filters: {
    status?: string;
    search?: string;
    limit?: number;
    offset?: number;
  } = {}) => {
    const query = new URLSearchParams();
    if (filters.status && filters.status !== "all") query.set("status", filters.status);
    if (filters.search) query.set("search", filters.search);
    query.set("limit", String(filters.limit ?? 50));
    query.set("offset", String(filters.offset ?? 0));
    return request<SubscriptionOperationPage>(`/api/v1/subscriptions?${query.toString()}`);
  },

  updateSubscriptionOperation: (tokenId: string, input: SubscriptionOperationPatch) =>
    request<SubscriptionOperationRecord>(`/api/v1/subscriptions/${encodeURIComponent(tokenId)}`, {
      method: "PATCH",
      body: JSON.stringify(input),
    }),

  trafficReport: (range: "7d" | "30d" = "30d") =>
    request<TrafficReport>(`/api/v1/reports/traffic?range=${encodeURIComponent(range)}&group_by=all`),

  getNotificationSettings: () =>
    request<NotificationSettings>("/api/v1/settings/notifications"),

  saveNotificationNotifier: (input: NotificationNotifierInput) =>
    request<NotificationNotifierRecord>("/api/v1/settings/notifications", {
      method: "PUT",
      body: JSON.stringify(input),
    }),

  testNotificationNotifier: (notifierId: string) =>
    request<{ delivered: boolean; response_code: number }>(
      `/api/v1/notifiers/${encodeURIComponent(notifierId)}/test`,
      { method: "POST", body: "{}" },
    ),

  deleteNotificationNotifier: (notifierId: string) =>
    request<void>(`/api/v1/notifiers/${encodeURIComponent(notifierId)}`, {
      method: "DELETE",
      body: "{}",
    }),

  saveNotificationReminderRule: (input: NotificationReminderRuleInput) =>
    request<NotificationReminderRuleRecord>("/api/v1/reminder-rules", {
      method: "PUT",
      body: JSON.stringify(input),
    }),

  runNotificationReminderRule: (ruleId: string) =>
    request<{ sent: boolean; result: string }>(
      `/api/v1/reminder-rules/${encodeURIComponent(ruleId)}/run`,
      { method: "POST", body: "{}" },
    ),

  deleteNotificationReminderRule: (ruleId: string) =>
    request<void>(`/api/v1/reminder-rules/${encodeURIComponent(ruleId)}`, {
      method: "DELETE",
      body: "{}",
    }),

  updateTelegramBotAccess: (notifierId: string, enabled: boolean) =>
    request<TelegramBotAccessRecord>(
      `/api/v1/notifiers/${encodeURIComponent(notifierId)}/telegram-bot`,
      { method: "PUT", body: JSON.stringify({ enabled }) },
    ),

  bulkNodes: (nodeIds: string[], action: BulkNodeAction, maxLines = 0) =>
    request<{ results: BulkNodeResult[] }>("/api/v1/nodes/bulk", {
      method: "POST",
      body: JSON.stringify({ node_ids: nodeIds, action, max_lines: maxLines }),
    }),

  acknowledgeAlert: (alertId: string) =>
    request<AlertRecord>(`/api/v1/alerts/${encodeURIComponent(alertId)}/acknowledge`, {
      method: "POST",
      body: "{}",
    }),

  getSUIState: (nodeId: string) =>
    request<SUIState>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/s-ui`),

  setSUITargets: (nodeId: string, inboundIds: number[]) =>
    request<SUIState>(`/api/v1/nodes/${encodeURIComponent(nodeId)}/s-ui/targets`, {
      method: "PUT",
      body: JSON.stringify({ inbound_ids: inboundIds }),
    }),

  importSUIClient: (nodeId: string, clientId: number, userId: string) =>
    request<UserRecord>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/s-ui/clients/${clientId}/import`,
      { method: "POST", body: JSON.stringify({ user_id: userId }) },
    ),

  adoptSUIClient: (nodeId: string, clientId: number, confirmName: string) =>
    request<UserRecord>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/s-ui/clients/${clientId}/adopt`,
      { method: "POST", body: JSON.stringify({ confirm_name: confirmName }) },
    ),

  async listUsers() {
    const result = await request<{ users: UserRecord[] }>("/api/v1/users?limit=500");
    return result.users;
  },

  listUsersPage: (filters: { search?: string; limit?: number; offset?: number } = {}) => {
    const query = new URLSearchParams();
    if (filters.search) query.set("search", filters.search);
    query.set("limit", String(filters.limit ?? 50));
    query.set("offset", String(filters.offset ?? 0));
    return request<import("./types").UserPage>(`/api/v1/users?${query.toString()}`);
  },

  createUser: (input: UserInput) =>
    request<CreateUserResponse>("/api/v1/users", {
      method: "POST",
      body: JSON.stringify(input),
    }),

  updateUser: (id: string, input: UserInput) =>
    request<UserRecord>(`/api/v1/users/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(input),
    }),

  archiveUser: (id: string) =>
    request<void>(`/api/v1/users/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: "{}",
    }),

  assignUser: (userId: string, nodeId: string, trafficLimitBytes = 0) =>
    request<AssignUserResponse>(`/api/v1/users/${encodeURIComponent(userId)}/assignments`, {
      method: "POST",
      body: JSON.stringify({ node_id: nodeId, traffic_limit_bytes: trafficLimitBytes }),
    }),

  updateAssignment: (userId: string, nodeId: string, input: AssignmentInput) =>
    request<UserRecord>(
      `/api/v1/users/${encodeURIComponent(userId)}/assignments/${encodeURIComponent(nodeId)}`,
      { method: "PUT", body: JSON.stringify(input) },
    ),

  unassignUser: (userId: string, nodeId: string) =>
    request<void>(
      `/api/v1/users/${encodeURIComponent(userId)}/assignments/${encodeURIComponent(nodeId)}`,
      { method: "DELETE", body: "{}" },
    ),

  revealCredential: (userId: string, nodeId: string) =>
    request<UserCredential>(
      `/api/v1/users/${encodeURIComponent(userId)}/assignments/${encodeURIComponent(nodeId)}/credential`,
      { method: "POST", body: "{}" },
    ),

  kickUser: (userId: string, nodeId = "") =>
    request<{ requested_nodes: number }>(`/api/v1/users/${encodeURIComponent(userId)}/kick`, {
      method: "POST",
      body: JSON.stringify({ node_id: nodeId }),
    }),

  async listSubscriptionTokens(userId: string) {
    const result = await request<{ tokens: SubscriptionTokenRecord[] }>(
      `/api/v1/users/${encodeURIComponent(userId)}/subscription-tokens`,
    );
    return result.tokens;
  },

  createSubscriptionToken: (userId: string, input: SubscriptionTokenInput) =>
    request<IssuedSubscriptionToken>(
      `/api/v1/users/${encodeURIComponent(userId)}/subscription-tokens`,
      { method: "POST", body: JSON.stringify(input) },
    ),

  rotateSubscriptionToken: (userId: string, tokenId: string) =>
    request<IssuedSubscriptionToken>(
      `/api/v1/users/${encodeURIComponent(userId)}/subscription-tokens/${encodeURIComponent(tokenId)}/rotate`,
      { method: "POST", body: "{}" },
    ),

  revokeSubscriptionToken: (userId: string, tokenId: string) =>
    request<void>(
      `/api/v1/users/${encodeURIComponent(userId)}/subscription-tokens/${encodeURIComponent(tokenId)}`,
      { method: "DELETE", body: "{}" },
    ),

  rotateAssignmentCredential: (userId: string, nodeId: string) =>
    request<AssignUserResponse>(
      `/api/v1/users/${encodeURIComponent(userId)}/assignments/${encodeURIComponent(nodeId)}/rotate-credential`,
      { method: "POST", body: "{}" },
    ),

  rotateUserCredentials: (userId: string) =>
    request<CreateUserResponse>(
      `/api/v1/users/${encodeURIComponent(userId)}/rotate-credentials`,
      { method: "POST", body: "{}" },
    ),
};
