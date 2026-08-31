export type AdapterType =
  | "native_hysteria2"
  | "sing_box_vless_reality"
  | "standalone_sing_box"
  | "s_ui";
export type AdapterProbeStatus =
  | "unknown"
  | "compatible"
  | "incompatible"
  | "unavailable"
  | "not_configured";
export type ManagementMode = "managed" | "read_only";

export type NodeStatus = "pending" | "online" | "stale" | "offline" | "degraded" | "disabled";

export interface Admin {
  id: string;
  username: string;
}

export interface Session {
  admin: Admin;
  csrf_token: string;
  expires_at: string;
}

export interface SetupStatus {
  setup_required: boolean;
  bootstrap_token_configured: boolean;
}

export interface NodeRealityInput {
  handshake_server: string;
  handshake_port: number;
}

export interface NodeRealityRecord extends NodeRealityInput {
  key_generation: number;
  applied_key_generation: number;
  public_key: string;
  short_id: string;
  material_applied_version: number;
  material_reported_at: string | null;
}

export interface NodeRecord {
  id: string;
  name: string;
  provider: string;
  region: string;
  adapter_type: AdapterType;
  adapter_status: AdapterProbeStatus;
  adapter_version: string;
  adapter_error_code: string;
  adapter_last_probed_at: string | null;
  adapter_last_discovered_at: string | null;
  s_ui_target_inbound_ids: number[];
  public_host: string;
  public_port: number;
  sni: string;
  tls_insecure: boolean;
  tls_cert_fingerprint: string;
  tls_public_key_sha256: string;
  reality: NodeRealityRecord | null;
  enabled: boolean;
  status: NodeStatus;
  status_reason: string;
  desired_version: number;
  applied_version: number;
  agent_installation_id?: string;
  agent_version: string;
  protocol_version: number;
  os_name: string;
  os_version: string;
  architecture: string;
  hostname: string;
  kernel_version: string;
  core_name: string;
  core_version: string;
  core_running: boolean;
  uptime_seconds: number;
  cpu_cores: number;
  cpu_percent: number;
  memory_used_bytes: number;
  memory_total_bytes: number;
  swap_used_bytes: number;
  swap_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  disk_read_bytes_per_second: number;
  disk_write_bytes_per_second: number;
  network_rx_bps: number;
  network_tx_bps: number;
  network_rx_bytes_total: number;
  network_tx_bytes_total: number;
  load_1: number;
  load_5: number;
  load_15: number;
  usage_enabled: boolean;
  usage_available: boolean;
  usage_outbox_batches: number;
  usage_error_code: string;
  usage_sampled_at: string | null;
  traffic_upload_bytes: number;
  traffic_download_bytes: number;
  traffic_unattributed_bytes: number;
  traffic_last_report_at: string | null;
  traffic_limit_bytes: number;
  traffic_reset_day: number;
  traffic_cycle_started_at: string | null;
  traffic_cycle_upload_bytes: number;
  traffic_cycle_download_bytes: number;
  traffic_used_bytes: number;
  traffic_calibration_bytes: number | null;
  traffic_calibrated_at: string | null;
  online_users: number;
  online_connections: number;
  online_unknown_users: number;
  online_sampled_at: string | null;
  online_last_report_at: string | null;
  last_seen_at: string | null;
  last_applied_at: string | null;
  created_at: string;
  updated_at: string;
}

export type MetricRange = "1h" | "6h" | "24h" | "7d" | "30d";

export interface NodeMetricSample {
  bucket_at: string;
  cpu_percent: number;
  memory_used_bytes: number;
  memory_total_bytes: number;
  swap_used_bytes: number;
  swap_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  disk_read_bytes_per_second: number;
  disk_write_bytes_per_second: number;
  network_rx_bps: number;
  network_tx_bps: number;
  load_1: number;
  load_5: number;
  load_15: number;
  sampled_at: string;
}

export interface NodeMetricSeries {
  range: MetricRange;
  step_seconds: number;
  samples: NodeMetricSample[];
}

export interface HostProcessSnapshot {
  pid: number;
  name: string;
  unit?: string;
  cpu_percent: number;
  rss_bytes: number;
  uptime_seconds: number;
}

export interface SystemdServiceSnapshot {
  unit: string;
  description?: string;
  active_state: string;
  sub_state: string;
  cpu_percent: number;
  cpu_peak_percent: number;
  memory_bytes: number;
  memory_peak_bytes: number;
  tasks: number;
  restarts: number;
  main_pid: number;
}

export interface NodeTelemetrySnapshot {
  supported: boolean;
  sampled_at: string | null;
  processes_available: boolean;
  processes_error_code: string;
  processes_total: number;
  processes_truncated: boolean;
  processes_sampled_at: string | null;
  processes: HostProcessSnapshot[];
  services_available: boolean;
  services_error_code: string;
  services_total: number;
  services_truncated: boolean;
  services_sampled_at: string | null;
  services: SystemdServiceSnapshot[];
}

export interface NodeInput {
  name: string;
  provider: string;
  region: string;
  adapter_type: AdapterType;
  public_host: string;
  public_port: number;
  sni: string;
  tls_insecure: boolean;
  tls_cert_fingerprint: string;
  tls_public_key_sha256: string;
  reality?: NodeRealityInput | null;
  traffic_limit_bytes?: number;
  traffic_reset_day?: number;
  enabled?: boolean;
}

export interface EnrollmentToken {
  node_id: string;
  enrollment_token: string;
  expires_at: string;
}

export interface APIErrorPayload {
  error?: {
    code?: string;
    message?: string;
    request_id?: string;
  };
}

export type UserStatus = "active" | "disabled" | "expired";
export type AssignmentState = "pending" | "applied" | "failed";
export type QuotaState = "unlimited" | "active" | "limited";

export interface UserAssignment {
  id: string;
  node_id: string;
  node_name: string;
  node_adapter: AdapterType;
  enabled: boolean;
  traffic_limit_bytes: number;
  traffic_upload_bytes: number;
  traffic_download_bytes: number;
  traffic_used_bytes: number;
  quota_state: QuotaState;
  last_traffic_at: string | null;
  online_connections: number;
  online_sampled_at: string | null;
  kick_generation: number;
  credential_fingerprint: string;
  management_mode: ManagementMode;
  remote_client_id: number;
  subscription_eligible: boolean;
  subscription_reason: string;
  desired_version: number;
  applied_version: number;
  state: AssignmentState;
  last_error_code: string;
  last_error_message: string;
  last_attempt_at: string | null;
  applied_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface UserRecord {
  id: string;
  username: string;
  display_name: string;
  notes: string;
  enabled: boolean;
  expires_at: string | null;
  status: UserStatus;
  traffic_limit_bytes: number;
  traffic_upload_bytes: number;
  traffic_download_bytes: number;
  traffic_used_bytes: number;
  quota_state: QuotaState;
  last_traffic_at: string | null;
  online_connections: number;
  online_nodes: number;
  assignments: UserAssignment[];
  created_at: string;
  updated_at: string;
}

export interface UserInput {
  username: string;
  display_name: string;
  notes: string;
  enabled: boolean;
  expires_at: string | null;
  traffic_limit_bytes: number;
  node_ids: string[];
}

export interface AssignmentInput {
  enabled?: boolean;
  traffic_limit_bytes?: number;
}

export interface UserCredential {
  node_id: string;
  node_name: string;
  credential: string;
  credential_fingerprint: string;
}

export interface CreateUserResponse {
  user: UserRecord;
  credentials: UserCredential[];
}

export interface AssignUserResponse {
  user: UserRecord;
  credential: UserCredential;
}

export type SubscriptionFormat = "uri" | "base64" | "clash" | "sing-box";
export type SubscriptionTokenStatus = "active" | "expired" | "revoked";

export interface SubscriptionTokenRecord {
  id: string;
  user_id: string;
  name: string;
  token_prefix: string;
  allowed_formats: SubscriptionFormat[];
  status: SubscriptionTokenStatus;
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface SubscriptionTokenInput {
  name: string;
  allowed_formats: SubscriptionFormat[];
  expires_at: string | null;
}

export interface SubscriptionURLs {
  uri?: string;
  base64?: string;
  clash?: string;
  sing_box?: string;
}

export interface IssuedSubscriptionToken {
  subscription: SubscriptionTokenRecord;
  token: string;
  urls: SubscriptionURLs;
}

export interface SUIInbound {
  remote_id: number;
  tag: string;
  type: "hysteria2";
  listen: string;
  listen_port: number;
  observed_at: string;
}

export interface SUIClient {
  remote_id: number;
  name: string;
  enabled: boolean;
  inbound_ids: number[];
  upload_bytes: number;
  download_bytes: number;
  expires_at: number;
  online: boolean;
  observed_at: string;
  mapped_user_id?: string;
  mapped_username?: string;
  management_mode?: ManagementMode;
}

export interface SUIState {
  node_id: string;
  adapter_status: AdapterProbeStatus;
  adapter_version: string;
  adapter_error_code: string;
  last_probed_at: string | null;
  last_discovered_at: string | null;
  target_inbound_ids: number[];
  inbounds: SUIInbound[];
  clients: SUIClient[];
}

export type NodeOperationType =
  | "probe_core"
  | "restart_core"
  | "tail_core_log"
  | "backup_config"
  | "ping";
export type NodeOperationStatus = "queued" | "running" | "succeeded" | "failed" | "expired";

export interface NodeOperationRecord {
  id: string;
  node_id: string;
  node_name: string;
  sequence: number;
  type: NodeOperationType;
  status: NodeOperationStatus;
  retry_of?: string;
  attempt: number;
  max_lines: number;
  target: string;
  output: string;
  error_code: string;
  error_message: string;
  rolled_back: boolean;
  requested_by: string;
  expires_at: string;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface OperationFilters {
  node_id?: string;
  type?: NodeOperationType | "";
  status?: NodeOperationStatus | "";
  limit?: number;
  offset?: number;
}

export interface OperationPage {
  operations: NodeOperationRecord[];
  total: number;
  limit: number;
  offset: number;
}

export interface ConfigBackupRecord {
  id: string;
  node_id: string;
  node_name: string;
  operation_id: string;
  local_path: string;
  sha256: string;
  size_bytes: number;
  created_at: string;
}

export type AlertType =
  | "offline"
  | "degraded"
  | "core_down"
  | "usage_error"
  | "sync_failed"
  | "sync_stuck"
  | "operation_failed"
  | "traffic_quota_warning"
  | "traffic_quota_exhausted";
export type AlertStatus = "open" | "acknowledged" | "resolved";

export interface AlertRecord {
  id: string;
  node_id: string;
  node_name: string;
  type: AlertType;
  severity: "warning" | "critical";
  status: AlertStatus;
  message: string;
  occurrence_count: number;
  first_seen_at: string;
  last_seen_at: string;
  acknowledged_by?: string;
  acknowledged_at: string | null;
  resolved_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface UserPage {
  users: UserRecord[];
  total: number;
  limit: number;
  offset: number;
}

export interface NodeAssetRecord {
  node_id: string;
  node_name: string;
  plan: string;
  purchased_at: string | null;
  expires_at: string | null;
  renewal_cycle_months: number;
  auto_renew: boolean;
  notes: string;
  created_at: string;
  updated_at: string;
}

export interface NodeAssetInput {
  plan: string;
  purchased_at: string | null;
  expires_at: string | null;
  renewal_cycle_months: number;
  auto_renew: boolean;
  notes: string;
}

export type SubscriptionOperationStatus =
  | "active"
  | "expiring"
  | "exhausted"
  | "expired"
  | "revoked"
  | "disabled";

export interface SubscriptionOperationRecord {
  token_id: string;
  user_id: string;
  username: string;
  display_name: string;
  name: string;
  token_prefix: string;
  allowed_formats: SubscriptionFormat[];
  status: SubscriptionOperationStatus;
  token_expires_at: string | null;
  user_expires_at: string | null;
  last_used_at: string | null;
  last_traffic_at: string | null;
  revoked_at: string | null;
  traffic_limit_bytes: number;
  traffic_upload_bytes: number;
  traffic_download_bytes: number;
  traffic_used_bytes: number;
  assignment_count: number;
  online_nodes: number;
  created_at: string;
  updated_at: string;
}

export interface SubscriptionOperationPage {
  subscriptions: SubscriptionOperationRecord[];
  total: number;
  limit: number;
  offset: number;
}

export interface SubscriptionOperationPatch {
  token_expires_at?: string | null;
  user_expires_at?: string | null;
  traffic_limit_bytes?: number;
  revoke?: boolean;
}

export interface TrafficReportPoint {
  bucket_at: string;
  upload_bytes: number;
  download_bytes: number;
}

export interface TrafficReportRank {
  id: string;
  name: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
}

export interface TrafficReport {
  range: "7d" | "30d";
  from: string;
  to: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
  previous_upload_bytes: number;
  previous_download_bytes: number;
  previous_total_bytes: number;
  daily: TrafficReportPoint[];
  previous_daily?: TrafficReportPoint[];
  top_users: TrafficReportRank[];
  top_nodes: TrafficReportRank[];
}

export type NotificationKind = "telegram" | "slack" | "webhook";
export type NotificationEvent = "created" | "resolved";

export interface NotificationNotifierRecord {
  id: string;
  name: string;
  kind: NotificationKind;
  enabled: boolean;
  target_hint: string;
  events: NotificationEvent[];
  created_at: string;
  updated_at: string;
}

export interface NotificationNotifierInput {
  id?: string;
  name: string;
  kind: NotificationKind;
  enabled: boolean;
  events: NotificationEvent[];
  url?: string;
  bot_token?: string;
  chat_id?: string;
}

export interface NotificationDeliveryRecord {
  id: string;
  notifier_id: string;
  notifier_name: string;
  notifier_kind: NotificationKind;
  alert_id: string;
  event_type: NotificationEvent;
  status: "queued" | "retry" | "delivered" | "failed";
  attempt_count: number;
  next_attempt_at: string;
  last_error: string;
  response_code: number;
  delivered_at: string | null;
  created_at: string;
}

export type NotificationReminderKind =
  | "fleet_summary"
  | "active_alerts"
  | "asset_expiry"
  | "traffic_usage";

export interface NotificationReminderRuleRecord {
  id: string;
  name: string;
  notifier_id: string;
  notifier_name: string;
  kind: NotificationReminderKind;
  enabled: boolean;
  interval_minutes: number;
  lead_days: number;
  threshold_percent: number;
  node_ids: string[];
  last_run_at: string | null;
  last_success_at: string | null;
  last_result: string;
  last_error: string;
  next_run_at: string;
  created_at: string;
  updated_at: string;
}

export interface NotificationReminderRuleInput {
  id?: string;
  name: string;
  notifier_id: string;
  kind: NotificationReminderKind;
  enabled: boolean;
  interval_minutes: number;
  lead_days: number;
  threshold_percent: number;
  node_ids: string[];
}

export interface TelegramBotAccessRecord {
  notifier_id: string;
  notifier_name: string;
  enabled: boolean;
  last_poll_at: string | null;
  last_error: string;
  updated_at: string;
}

export interface NotificationSettings {
  notifiers: NotificationNotifierRecord[];
  deliveries: NotificationDeliveryRecord[];
  reminder_rules: NotificationReminderRuleRecord[];
  telegram_bots: TelegramBotAccessRecord[];
}

export type BulkNodeAction =
  | "probe_core"
  | "restart_core"
  | "backup_config"
  | "tail_core_log"
  | "retry_sync";

export interface BulkNodeResult {
  node_id: string;
  status: "accepted" | "failed";
  error?: string;
  operation?: NodeOperationRecord;
}
