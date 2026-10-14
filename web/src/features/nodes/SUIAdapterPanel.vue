<script setup lang="ts">
import HintTooltip from "../../components/HintTooltip.vue";
import StatusPill from "../../components/StatusPill.vue";
import { computed, ref, watch } from "vue";
import { ArrowRight, RefreshCw, Save } from "@lucide/vue";
import {
  NAlert,
  NButton,
  NIcon,
  NInput,
  NModal,
  NSelect,
  NSpin,
  useMessage,
} from "naive-ui";
import { api, APIError } from "../../api";
import { formatBytes, formatDateTime, relativeTime } from "../../lib/format";
import type { NodeRecord, SUIClient, SUIState, UserAssignment, UserRecord } from "../../types";

const props = defineProps<{ node: NodeRecord }>();
const emit = defineEmits<{ changed: []; "session-expired": [] }>();
const message = useMessage();

const state = ref<SUIState | null>(null);
const users = ref<UserRecord[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const errorMessage = ref("");
const targetInboundIDs = ref<number[]>([]);
const savingTargets = ref(false);
const mappingSelections = ref<Record<number, string | null>>({});
const workingClientID = ref<number | null>(null);
const adoptClient = ref<SUIClient | null>(null);
const adoptConfirmation = ref("");
let loadGeneration = 0;

const assignedUserIDs = computed(() => {
  const result = new Set<string>();
  for (const user of users.value) {
    if (user.assignments.some((assignment) => assignment.node_id === props.node.id)) {
      result.add(user.id);
    }
  }
  return result;
});

const userOptions = computed(() =>
  users.value
    .filter((user) => !assignedUserIDs.value.has(user.id))
    .map((user) => ({
      label: user.display_name ? `${user.display_name} (@${user.username})` : `@${user.username}`,
      value: user.id,
      disabled: !user.enabled,
    })),
);

const inboundOptions = computed(() =>
  (state.value?.inbounds ?? []).map((inbound) => ({
    label: `${inbound.tag} · ${inbound.listen || "*"}:${inbound.listen_port}`,
    value: inbound.remote_id,
  })),
);

const statusPresentation = computed(() => {
  switch (state.value?.adapter_status) {
    case "compatible":
      return { label: "兼容", type: "success" as const };
    case "incompatible":
      return { label: "版本不兼容", type: "error" as const };
    case "unavailable":
      return { label: "API 不可用", type: "error" as const };
    case "not_configured":
      return { label: "未配置", type: "warning" as const };
    default:
      return { label: "等待探测", type: "default" as const };
  }
});

function readableAPIError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return "登录已过期。";
  }
  const messages: Record<string, string> = {
    sui_mapping_conflict: "S-UI 客户端、用户映射或目标入站已发生变化，请刷新后重试。",
    sui_resource_not_found: "S-UI 客户端、节点或用户已不存在。",
    sui_adapter_required: "该节点不是 S-UI 节点。",
  };
  return error instanceof APIError ? (messages[error.code] ?? error.message) : fallback;
}

async function load(silent = false) {
  const generation = ++loadGeneration;
  if (!silent && state.value === null) loading.value = true;
  refreshing.value = true;
  errorMessage.value = "";
  try {
    const [nextState, nextUsers] = await Promise.all([
      api.getSUIState(props.node.id),
      api.listUsers(),
    ]);
    if (generation !== loadGeneration) return;
    state.value = nextState;
    users.value = nextUsers;
    targetInboundIDs.value = [...nextState.target_inbound_ids];
  } catch (error) {
    if (generation !== loadGeneration) return;
    errorMessage.value = readableAPIError(error, "S-UI 状态加载失败。");
  } finally {
    if (generation === loadGeneration) {
      loading.value = false;
      refreshing.value = false;
    }
  }
}

async function saveTargets() {
  savingTargets.value = true;
  try {
    const nextState = await api.setSUITargets(props.node.id, targetInboundIDs.value);
    state.value = nextState;
    targetInboundIDs.value = [...nextState.target_inbound_ids];
    emit("changed");
    message.success("目标入站已保存，等待 Agent 同步");
  } catch (error) {
    message.error(readableAPIError(error, "目标入站保存失败。"));
  } finally {
    savingTargets.value = false;
  }
}

async function importClient(client: SUIClient) {
  const userID = mappingSelections.value[client.remote_id];
  if (!userID) return;
  workingClientID.value = client.remote_id;
  try {
    await api.importSUIClient(props.node.id, client.remote_id, userID);
    mappingSelections.value[client.remote_id] = null;
    await load(true);
    emit("changed");
    message.success("客户端已只读导入，等待 Agent 确认映射");
  } catch (error) {
    message.error(readableAPIError(error, "客户端导入失败。"));
  } finally {
    workingClientID.value = null;
  }
}

function openAdopt(client: SUIClient) {
  adoptClient.value = client;
  adoptConfirmation.value = "";
}

async function confirmAdopt() {
  const client = adoptClient.value;
  if (!client || adoptConfirmation.value !== client.name) return;
  workingClientID.value = client.remote_id;
  try {
    await api.adoptSUIClient(props.node.id, client.remote_id, adoptConfirmation.value);
    adoptClient.value = null;
    adoptConfirmation.value = "";
    await load(true);
    emit("changed");
    message.success("客户端已接管，等待 S-UI 应用新凭据");
  } catch (error) {
    message.error(readableAPIError(error, "客户端接管失败。"));
  } finally {
    workingClientID.value = null;
  }
}

function assignmentFor(client: SUIClient): UserAssignment | null {
  if (!client.mapped_user_id) return null;
  const user = users.value.find((candidate) => candidate.id === client.mapped_user_id);
  return user?.assignments.find((assignment) => assignment.node_id === props.node.id) ?? null;
}

function canAdopt(client: SUIClient) {
  const assignment = assignmentFor(client);
  return (
    (state.value?.target_inbound_ids.length ?? 0) > 0 &&
    assignment?.state === "applied" &&
    assignment.applied_version >= assignment.desired_version
  );
}

function adoptHint(client: SUIClient) {
  if ((state.value?.target_inbound_ids.length ?? 0) === 0) return "先保存至少一个目标入站";
  return canAdopt(client) ? "接管客户端" : "等待 Agent 确认只读映射";
}

function inboundNames(client: SUIClient) {
  const names = new Map((state.value?.inbounds ?? []).map((inbound) => [inbound.remote_id, inbound.tag]));
  return client.inbound_ids.map((id) => names.get(id) ?? `#${id}`).join(" · ") || "-";
}

function expiryLabel(value: number) {
  return value > 0 ? formatDateTime(new Date(value * 1000).toISOString()) : "永不过期";
}

watch(
  () => props.node.id,
  () => {
    state.value = null;
    users.value = [];
    targetInboundIDs.value = [];
    void load();
  },
  { immediate: true },
);

watch(
  () => props.node.adapter_last_discovered_at,
  (next, previous) => {
    if (state.value && next && previous && next !== previous) void load(true);
  },
);
</script>

<template>
  <section class="detail-section sui-panel">
    <div class="detail-section__heading">
      <h2>S-UI 适配器</h2>
      <hint-tooltip trigger="hover">
        <template #trigger>
          <n-button circle quaternary size="small" :loading="refreshing" aria-label="刷新 S-UI 状态" @click="load()">
            <template #icon><n-icon><refresh-cw /></n-icon></template>
          </n-button>
        </template>
        刷新
      </hint-tooltip>
    </div>

    <div v-if="loading" class="sui-panel__state"><n-spin :size="22" /></div>
    <n-alert v-else-if="errorMessage && !state" type="error" :show-icon="false">
      <div class="alert-row">
        <span>{{ errorMessage }}</span>
        <n-button text type="error" @click="load()">重试</n-button>
      </div>
    </n-alert>
    <template v-else-if="state">
      <n-alert v-if="errorMessage" type="error" :show-icon="false" class="sui-panel__alert">
        {{ errorMessage }}
      </n-alert>
      <div class="sui-adapter-summary">
        <status-pill :type="statusPresentation.type">
          {{ statusPresentation.label }}
        </status-pill>
        <span>{{ state.adapter_version || "尚未上报版本" }}</span>
      </div>
      <dl class="detail-list">
        <div><dt>最近探测</dt><dd>{{ relativeTime(state.last_probed_at) }}</dd></div>
        <div><dt>最近发现</dt><dd>{{ relativeTime(state.last_discovered_at) }}</dd></div>
        <div v-if="state.adapter_error_code"><dt>适配器错误</dt><dd>{{ state.adapter_error_code }}</dd></div>
      </dl>

      <div v-if="state.adapter_status === 'compatible'" class="sui-subsection">
        <div class="sui-subsection__heading">
          <h3>受管 Hysteria2 入站</h3>
          <span>{{ targetInboundIDs.length }} / {{ state.inbounds.length }}</span>
        </div>
        <div v-if="state.inbounds.length" class="sui-target-row">
          <n-select
            v-model:value="targetInboundIDs"
            multiple
            clearable
            :options="inboundOptions"
            placeholder="选择目标入站"
          />
          <hint-tooltip trigger="hover">
            <template #trigger>
              <n-button circle type="primary" :loading="savingTargets" aria-label="保存目标入站" @click="saveTargets">
                <template #icon><n-icon><save /></n-icon></template>
              </n-button>
            </template>
            保存目标入站
          </hint-tooltip>
        </div>
        <div v-else class="sui-panel__state sui-panel__state--empty">未发现 Hysteria2 入站</div>
      </div>

      <div v-if="state.adapter_status === 'compatible'" class="sui-subsection">
        <div class="sui-subsection__heading">
          <h3>Hysteria2 客户端</h3>
          <span>{{ state.clients.length }}</span>
        </div>
        <div v-if="state.clients.length" class="sui-client-list">
          <article v-for="client in state.clients" :key="client.remote_id" class="sui-client-item">
            <header>
              <div>
                <strong>{{ client.name }}</strong>
                <span>#{{ client.remote_id }} · {{ inboundNames(client) }}</span>
              </div>
              <div class="sui-client-tags">
                <status-pill v-if="client.online" type="success" dot>在线</status-pill>
                <status-pill v-else type="error" dot>离线</status-pill>
                <status-pill v-if="client.management_mode === 'managed'" type="info">受管</status-pill>
                <status-pill v-else-if="client.management_mode === 'read_only'" type="warning">只读</status-pill>
                <status-pill v-else>未映射</status-pill>
              </div>
            </header>
            <dl class="sui-client-metrics">
              <div><dt>上传</dt><dd>{{ formatBytes(client.upload_bytes) }}</dd></div>
              <div><dt>下载</dt><dd>{{ formatBytes(client.download_bytes) }}</dd></div>
              <div><dt>到期</dt><dd>{{ expiryLabel(client.expires_at) }}</dd></div>
              <div><dt>状态</dt><dd>{{ client.enabled ? "启用" : "停用" }}</dd></div>
            </dl>

            <div v-if="!client.mapped_user_id" class="sui-client-action">
              <n-select
                v-model:value="mappingSelections[client.remote_id]"
                filterable
                clearable
                :options="userOptions"
                :disabled="userOptions.length === 0"
                placeholder="映射到已有用户"
              />
              <n-button
                secondary
                :disabled="!mappingSelections[client.remote_id]"
                :loading="workingClientID === client.remote_id"
                @click="importClient(client)"
              >
                只读导入
              </n-button>
            </div>
            <div v-else class="sui-client-mapping">
              <span>映射到 @{{ client.mapped_username }}</span>
              <hint-tooltip v-if="client.management_mode === 'read_only'" trigger="hover">
                <template #trigger>
                  <n-button
                    secondary
                    size="small"
                    :disabled="!canAdopt(client)"
                    :loading="workingClientID === client.remote_id"
                    @click="openAdopt(client)"
                  >
                    <template #icon><n-icon><arrow-right /></n-icon></template>
                    接管
                  </n-button>
                </template>
                {{ adoptHint(client) }}
              </hint-tooltip>
            </div>
          </article>
        </div>
        <div v-else class="sui-panel__state sui-panel__state--empty">未发现 Hysteria2 客户端</div>
      </div>
    </template>
  </section>

  <n-modal
    :show="adoptClient !== null"
    preset="card"
    title="接管 S-UI 客户端"
    class="sui-adopt-modal"
    :bordered="false"
    :mask-closable="workingClientID === null"
    :close-on-esc="workingClientID === null"
    @update:show="!$event && (adoptClient = null)"
  >
    <n-alert type="warning" :show-icon="false" class="sui-adopt-warning">
      接管会替换该客户端的 Hysteria2 密码并重载关联入站，现有连接可能断开。
    </n-alert>
    <n-input
      v-model:value="adoptConfirmation"
      :placeholder="adoptClient?.name"
      maxlength="128"
      autocomplete="off"
      aria-label="输入远端客户端名称确认接管"
    />
    <template #footer>
      <div class="modal-actions">
        <n-button :disabled="workingClientID !== null" @click="adoptClient = null">取消</n-button>
        <n-button
          type="primary"
          :disabled="adoptConfirmation !== adoptClient?.name"
          :loading="workingClientID === adoptClient?.remote_id"
          @click="confirmAdopt"
        >
          确认接管
        </n-button>
      </div>
    </template>
  </n-modal>
</template>
