<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  History,
  KeyRound,
  Link2,
  LogOut,
  Pencil,
  Plus,
  RotateCw,
  Save,
  Server,
  Trash2,
  Wifi,
} from "@lucide/vue";
import {
  NButton,
  NIcon,
  NInputNumber,
  NProgress,
  NSelect,
  NSpin,
  NSwitch,
  NTag,
  NTooltip,
} from "naive-ui";
import { adapterLabels, formatBytes, formatDateTime, progressStatus, quotaPercent, relativeTime } from "../../lib/format";
import type { NodeRecord, SubscriptionTokenRecord, UserAssignment, UserRecord } from "../../types";

const props = defineProps<{
  show: boolean;
  modal?: boolean;
  user: UserRecord | null;
  assignableNodes: NodeRecord[];
  subscriptionTokens: SubscriptionTokenRecord[];
  subscriptionLoading: boolean;
  working: string;
}>();
const emit = defineEmits<{
  "update:show": [show: boolean];
  edit: [user: UserRecord];
  "toggle-user": [user: UserRecord, enabled: boolean];
  assign: [user: UserRecord, nodeId: string, trafficLimitBytes: number];
  "toggle-assignment": [user: UserRecord, assignment: UserAssignment, enabled: boolean];
  unassign: [user: UserRecord, assignment: UserAssignment];
  reveal: [user: UserRecord, assignment: UserAssignment];
  "update-assignment-limit": [user: UserRecord, assignment: UserAssignment, trafficLimitBytes: number];
  "kick-user": [user: UserRecord];
  "kick-assignment": [user: UserRecord, assignment: UserAssignment];
  "create-subscription": [user: UserRecord];
  "rotate-subscription": [user: UserRecord, token: SubscriptionTokenRecord];
  "revoke-subscription": [user: UserRecord, token: SubscriptionTokenRecord];
  "rotate-assignment-credential": [user: UserRecord, assignment: UserAssignment];
  "rotate-user-credentials": [user: UserRecord];
  "open-node": [nodeId: string];
}>();

const selectedNodeID = ref<string | null>(null);
const selectedLimitGiB = ref(0);
const assignmentLimits = ref<Record<string, number>>({});
const subscriptionHistoryOpen = ref(false);
const panel = ref<HTMLElement | null>(null);
const assignedNodeIDs = computed(() => new Set(props.user?.assignments.map((item) => item.node_id) ?? []));
const availableOptions = computed(() =>
  props.assignableNodes
    .filter((node) => !assignedNodeIDs.value.has(node.id))
    .map((node) => ({
      label: `${node.name} · ${adapterLabels[node.adapter_type]}`,
      value: node.id,
      disabled: !node.enabled,
    })),
);
const activeSubscriptionTokens = computed(() =>
  props.subscriptionTokens.filter((token) => token.status === "active"),
);
const inactiveSubscriptionTokens = computed(() =>
  props.subscriptionTokens.filter((token) => token.status !== "active"),
);
const displayedSubscriptionTokens = computed(() =>
  subscriptionHistoryOpen.value
    ? [...activeSubscriptionTokens.value, ...inactiveSubscriptionTokens.value]
    : activeSubscriptionTokens.value,
);
const managedAssignments = computed(
  () => props.user?.assignments.filter((assignment) => assignment.management_mode === "managed") ?? [],
);
const kickableAssignments = computed(
  () => managedAssignments.value.filter((assignment) => supportsKick(assignment)),
);

watch(
  () => [props.show, props.user?.id] as const,
  () => {
    selectedNodeID.value = null;
    selectedLimitGiB.value = 0;
    subscriptionHistoryOpen.value = false;
    assignmentLimits.value = Object.fromEntries(
      (props.user?.assignments ?? []).map((assignment) => [
        assignment.id,
        assignment.traffic_limit_bytes / 1024 ** 3,
      ]),
    );
  },
);

watch(
  () => [props.show, props.user?.id, props.modal] as const,
  ([show, , modal]) => {
    if (show && modal) nextTick(() => panel.value?.focus());
  },
  { immediate: true },
);

function handlePanelKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    emit("update:show", false);
    return;
  }
  if (!props.modal || event.key !== "Tab" || !panel.value) return;
  const focusable = Array.from(
    panel.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => !element.hidden && element.getClientRects().length > 0);
  if (focusable.length === 0) {
    event.preventDefault();
    panel.value.focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function assign() {
  if (!props.user || !selectedNodeID.value) return;
  const trafficLimitBytes = Math.round(selectedLimitGiB.value * 1024 ** 3);
  emit("assign", props.user, selectedNodeID.value, trafficLimitBytes);
  selectedNodeID.value = null;
  selectedLimitGiB.value = 0;
}

function supportsKick(assignment: UserAssignment) {
  return assignment.node_adapter === "native_hysteria2" ||
    assignment.node_adapter === "sing_box_vless_reality";
}

function saveAssignmentLimit(assignment: UserAssignment) {
  if (!props.user) return;
  const value = assignmentLimits.value[assignment.id] ?? 0;
  emit("update-assignment-limit", props.user, assignment, Math.round(value * 1024 ** 3));
}

function assignmentState(assignment: UserAssignment) {
  if (assignment.state === "failed") return { label: "失败", type: "error" as const };
  if (assignment.state === "applied" && assignment.applied_version >= assignment.desired_version) {
    return { label: "已同步", type: "success" as const };
  }
  return { label: "等待同步", type: "warning" as const };
}

function subscriptionState(token: SubscriptionTokenRecord) {
  if (token.status === "revoked") return { label: "已撤销", type: "default" as const };
  if (token.status === "expired") return { label: "已到期", type: "error" as const };
  return { label: "有效", type: "success" as const };
}

function subscriptionEligibility(assignment: UserAssignment) {
  if (assignment.subscription_eligible) return { label: "已纳入订阅", type: "success" as const };
  const labels: Record<string, string> = {
    read_only_requires_adoption: "接管后纳入订阅",
    adapter_not_supported: "适配器不支持订阅",
    node_disabled: "节点已停用",
    node_not_ready: "节点尚未就绪",
    endpoint_missing: "缺少公网端点",
    adapter_not_compatible: "Reality 适配器未通过兼容性检查",
    core_not_running: "sing-box 服务未运行",
    applied_state_mismatch: "Reality 已应用状态不一致",
    reality_material_missing: "Reality 公共材料尚未就绪",
    assignment_disabled: "分配已停用",
    assignment_quota_limited: "节点额度已用尽",
    assignment_not_applied: "等待配置同步",
    credential_not_applied: "凭据尚未应用",
  };
  return { label: labels[assignment.subscription_reason] ?? "未纳入订阅", type: "warning" as const };
}

function formatLabel(format: string) {
  return { uri: "URI", base64: "Base64", clash: "Clash", "sing-box": "sing-box" }[format] ?? format;
}
</script>

<template>
  <aside
    v-if="show && user"
    ref="panel"
    id="user-detail-panel"
    class="user-detail-drawer user-detail-panel user-master-detail__detail"
    :role="modal ? 'dialog' : 'region'"
    :aria-modal="modal ? 'true' : undefined"
    :aria-labelledby="`user-detail-${user.id}-title`"
    tabindex="-1"
    @keydown="handlePanelKeydown"
  >
    <header class="user-detail-shell__header user-detail-panel__header">
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            quaternary
            circle
            class="user-detail-panel__back"
            aria-label="返回用户列表"
            @click="emit('update:show', false)"
          >
            <template #icon><n-icon><arrow-left /></n-icon></template>
          </n-button>
        </template>
        返回用户列表
      </n-tooltip>
      <div class="user-detail-header">
        <div>
          <h2 :id="`user-detail-${user.id}-title`">{{ user.display_name || user.username }}</h2>
          <span>@{{ user.username }}</span>
        </div>
        <span
          class="status-marker"
          :class="`status-marker--${user.status === 'expired' ? 'expired' : user.enabled ? 'active' : 'disabled'}`"
        >
          <i aria-hidden="true" />{{ user.status === "expired" ? "已到期" : user.enabled ? "启用" : "已停用" }}
        </span>
        <n-switch
          :value="user.enabled"
          :loading="working === `user:${user.id}`"
          aria-label="启用用户"
          @update:value="emit('toggle-user', user, $event)"
        />
      </div>
    </header>

    <div class="user-detail-shell__body user-detail-panel__body">

      <section class="user-overview-ledger" aria-label="账户概览">
        <div class="user-overview-ledger__cell">
          <span>账户有效期</span>
          <strong>{{ formatDateTime(user.expires_at) }}</strong>
          <small>创建于 {{ formatDateTime(user.created_at, false) }}</small>
        </div>
        <div class="user-overview-ledger__cell">
          <span>活跃连接</span>
          <strong>{{ user.online_connections }}</strong>
          <small>{{ user.online_nodes }} / {{ user.assignments.length }} 个节点在线</small>
        </div>
        <div class="user-overview-ledger__traffic">
          <header>
            <span>流量使用</span>
            <strong>{{ formatBytes(user.traffic_used_bytes) }} / {{ user.traffic_limit_bytes ? formatBytes(user.traffic_limit_bytes) : "不限额" }}</strong>
          </header>
          <n-progress
            v-if="user.traffic_limit_bytes > 0"
            type="line"
            :percentage="quotaPercent(user.traffic_used_bytes, user.traffic_limit_bytes)"
            :show-indicator="false"
            :status="user.quota_state === 'limited' ? 'error' : progressStatus(quotaPercent(user.traffic_used_bytes, user.traffic_limit_bytes))"
          />
          <footer>
            <span>上传 {{ formatBytes(user.traffic_upload_bytes) }}</span>
            <span>下载 {{ formatBytes(user.traffic_download_bytes) }}</span>
            <span>更新 {{ relativeTime(user.updated_at) }}</span>
          </footer>
        </div>
        <p v-if="user.notes" class="user-notes">{{ user.notes }}</p>
      </section>

      <section class="detail-section">
        <div class="detail-section__heading">
          <h2>统一订阅</h2>
          <n-button size="small" secondary @click="emit('create-subscription', user)">
            <template #icon><n-icon><plus /></n-icon></template>
            创建 Token
          </n-button>
        </div>
        <div v-if="subscriptionLoading" class="subscription-loading"><n-spin :size="20" /></div>
        <div v-else-if="displayedSubscriptionTokens.length" class="subscription-token-list">
          <article v-for="token in displayedSubscriptionTokens" :key="token.id" class="subscription-token-item">
            <header>
              <div>
                <strong>{{ token.name }}</strong>
                <span>{{ token.token_prefix }}••••</span>
              </div>
              <span class="status-marker" :class="`status-marker--${token.status}`">
                <i aria-hidden="true" />{{ subscriptionState(token).label }}
              </span>
            </header>
            <div class="subscription-token-meta">
              <span>{{ token.allowed_formats.map(formatLabel).join(" · ") }}</span>
              <span>使用 {{ relativeTime(token.last_used_at) }}</span>
              <span>到期 {{ formatDateTime(token.expires_at) }}</span>
            </div>
            <footer>
              <span>{{ formatDateTime(token.created_at, false) }}</span>
              <div class="assignment-actions">
                <n-tooltip v-if="token.status === 'active'" trigger="hover">
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      :loading="working === `subscription-rotate:${token.id}`"
                      :aria-label="`轮换 ${token.name} Token`"
                      @click="emit('rotate-subscription', user, token)"
                    >
                      <template #icon><n-icon><rotate-cw /></n-icon></template>
                    </n-button>
                  </template>
                  轮换 Token
                </n-tooltip>
                <n-tooltip v-if="token.status === 'active'" trigger="hover">
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      type="error"
                      :loading="working === `subscription-revoke:${token.id}`"
                      :aria-label="`撤销 ${token.name} Token`"
                      @click="emit('revoke-subscription', user, token)"
                    >
                      <template #icon><n-icon><trash2 /></n-icon></template>
                    </n-button>
                  </template>
                  撤销 Token
                </n-tooltip>
              </div>
            </footer>
          </article>
        </div>
        <div v-else class="subscription-empty">
          <link2 :size="18" aria-hidden="true" />
          <span>暂无有效订阅 Token</span>
        </div>
        <div v-if="!subscriptionLoading && inactiveSubscriptionTokens.length" class="subscription-history-toggle">
          <n-button
            text
            size="small"
            :aria-expanded="subscriptionHistoryOpen"
            :aria-label="subscriptionHistoryOpen ? '收起历史 Token' : '展开历史 Token'"
            @click="subscriptionHistoryOpen = !subscriptionHistoryOpen"
          >
            <template #icon><n-icon><history /></n-icon></template>
            历史 Token {{ inactiveSubscriptionTokens.length }}
            <n-icon class="subscription-history-chevron">
              <chevron-up v-if="subscriptionHistoryOpen" />
              <chevron-down v-else />
            </n-icon>
          </n-button>
        </div>
        <div class="subscription-count">{{ activeSubscriptionTokens.length }} 个有效 Token</div>
      </section>

      <section class="detail-section">
        <div class="detail-section__heading">
          <h2>节点分配</h2>
          <span>{{ user.assignments.length }}</span>
        </div>
        <div class="assignment-add">
          <n-select
            v-model:value="selectedNodeID"
            filterable
            clearable
            :options="availableOptions"
            :disabled="availableOptions.length === 0"
            placeholder="选择受管节点"
          />
          <n-input-number
            v-model:value="selectedLimitGiB"
            :min="0"
            :max="8388607"
            :precision="2"
            size="small"
            placeholder="额度 GiB"
          />
          <n-button
            type="primary"
            :disabled="!selectedNodeID"
            :loading="working === `assign:${user.id}`"
            @click="assign"
          >
            <template #icon><n-icon><plus /></n-icon></template>
            分配
          </n-button>
        </div>

        <div v-if="user.assignments.length" class="assignment-list">
          <article v-for="assignment in user.assignments" :key="assignment.id" class="assignment-item">
            <header>
              <div>
                <strong>{{ assignment.node_name }}</strong>
                <span>{{ adapterLabels[assignment.node_adapter] }} · v{{ assignment.applied_version }} / {{ assignment.desired_version }}</span>
              </div>
              <div class="assignment-tags">
                <n-tag
                  v-if="assignment.management_mode === 'read_only'"
                  type="warning"
                  size="small"
                  :bordered="false"
                >
                  只读导入
                </n-tag>
                <n-tag :type="assignmentState(assignment).type" size="small" :bordered="false">
                  {{ assignmentState(assignment).label }}
                </n-tag>
                <n-tag :type="subscriptionEligibility(assignment).type" size="small" :bordered="false">
                  {{ subscriptionEligibility(assignment).label }}
                </n-tag>
              </div>
            </header>
            <p v-if="assignment.last_error_message" class="assignment-error">
              {{ assignment.last_error_message }}
            </p>
            <div
              v-if="assignment.subscription_reason === 'read_only_requires_adoption'"
              class="assignment-adoption"
            >
              <span>当前远端凭据保持只读，额度和统一订阅尚未接管。</span>
              <n-button text type="primary" @click="emit('open-node', assignment.node_id)">
                <template #icon><n-icon><server /></n-icon></template>
                打开节点
              </n-button>
            </div>
            <div class="assignment-usage">
              <div>
                <span>{{ formatBytes(assignment.traffic_used_bytes) }}</span>
                <span v-if="assignment.traffic_limit_bytes"> / {{ formatBytes(assignment.traffic_limit_bytes) }}</span>
                <span v-else> / 不限额</span>
              </div>
              <span :class="{ 'assignment-online--active': assignment.online_connections > 0 }">
                <wifi :size="13" aria-hidden="true" />{{ assignment.online_connections }}
              </span>
            </div>
            <n-progress
              v-if="assignment.traffic_limit_bytes > 0"
              type="line"
              :percentage="quotaPercent(assignment.traffic_used_bytes, assignment.traffic_limit_bytes)"
              :show-indicator="false"
              :status="assignment.quota_state === 'limited' ? 'error' : progressStatus(quotaPercent(assignment.traffic_used_bytes, assignment.traffic_limit_bytes))"
            />
            <div
              v-if="assignment.management_mode === 'managed'"
              class="assignment-limit-row"
            >
              <n-input-number
                v-model:value="assignmentLimits[assignment.id]"
                :min="0"
                :max="8388607"
                :precision="2"
                size="small"
                placeholder="额度 GiB"
              >
                <template #suffix>GiB</template>
              </n-input-number>
              <n-tooltip trigger="hover">
                <template #trigger>
                  <n-button
                    circle
                    secondary
                    size="small"
                    :loading="working === `limit:${assignment.id}`"
                    :aria-label="`保存 ${assignment.node_name} 流量额度`"
                    @click="saveAssignmentLimit(assignment)"
                  >
                    <template #icon><n-icon><save /></n-icon></template>
                  </n-button>
                </template>
                保存节点额度
              </n-tooltip>
            </div>
            <footer>
              <span>
                {{ assignment.management_mode === "read_only" ? "远端凭据不受管" : assignment.credential_fingerprint }}
              </span>
              <div class="assignment-actions">
                <n-switch
                  v-if="assignment.management_mode === 'managed'"
                  size="small"
                  :value="assignment.enabled"
                  :loading="working === `toggle:${assignment.id}`"
                  :aria-label="`启用 ${assignment.node_name} 分配`"
                  @update:value="emit('toggle-assignment', user, assignment, $event)"
                />
                <n-tooltip
                  v-if="assignment.management_mode === 'managed' && supportsKick(assignment)"
                  trigger="hover"
                >
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      :loading="working === `kick:${assignment.id}`"
                      :aria-label="`将用户从 ${assignment.node_name} 踢下线`"
                      @click="emit('kick-assignment', user, assignment)"
                    >
                      <template #icon><n-icon><log-out /></n-icon></template>
                    </n-button>
                  </template>
                  踢下线
                </n-tooltip>
                <n-tooltip v-if="assignment.management_mode === 'managed'" trigger="hover">
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      :loading="working === `reveal:${assignment.id}`"
                      :aria-label="`查看 ${assignment.node_name} 凭据`"
                      @click="emit('reveal', user, assignment)"
                    >
                      <template #icon><n-icon><key-round /></n-icon></template>
                    </n-button>
                  </template>
                  查看凭据
                </n-tooltip>
                <n-tooltip v-if="assignment.management_mode === 'managed'" trigger="hover">
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      :disabled="assignment.state !== 'applied' || assignment.applied_version < assignment.desired_version"
                      :loading="working === `credential-rotate:${assignment.id}`"
                      :aria-label="`轮换 ${assignment.node_name} 凭据`"
                      @click="emit('rotate-assignment-credential', user, assignment)"
                    >
                      <template #icon><n-icon><rotate-cw /></n-icon></template>
                    </n-button>
                  </template>
                  轮换凭据
                </n-tooltip>
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-button
                      circle
                      quaternary
                      type="error"
                      :aria-label="`取消 ${assignment.node_name} 分配`"
                      @click="emit('unassign', user, assignment)"
                    >
                      <template #icon><n-icon><trash2 /></n-icon></template>
                    </n-button>
                  </template>
                  取消分配
                </n-tooltip>
              </div>
            </footer>
          </article>
        </div>
        <div v-else class="assignment-empty">尚未分配节点</div>
      </section>

    </div>

    <footer class="user-detail-shell__footer user-detail-panel__footer">
      <div class="drawer-actions">
        <n-button @click="emit('edit', user)">
          <template #icon><n-icon><pencil /></n-icon></template>
          编辑用户
        </n-button>
        <n-button
          secondary
          :disabled="kickableAssignments.length === 0"
          :loading="working === `kick:${user.id}`"
          @click="emit('kick-user', user)"
        >
          <template #icon><n-icon><log-out /></n-icon></template>
          全部踢下线
        </n-button>
        <n-button
          secondary
          :disabled="managedAssignments.length === 0"
          :loading="working === `credential-rotate:${user.id}`"
          @click="emit('rotate-user-credentials', user)"
        >
          <template #icon><n-icon><rotate-cw /></n-icon></template>
          全部轮换
        </n-button>
      </div>
    </footer>
  </aside>
</template>
