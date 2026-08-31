<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  Activity,
  Archive,
  KeyRound,
  RadioTower,
  RefreshCw,
  RotateCw,
  ScrollText,
  Undo2,
} from "@lucide/vue";
import { NButton, NIcon, NInput, NSpin, NTag, NTooltip, useDialog, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import { formatBytes, formatDateTime, relativeTime } from "../../lib/format";
import type {
  ConfigBackupRecord,
  NodeOperationRecord,
  NodeOperationType,
  NodeRecord,
} from "../../types";

const props = withDefaults(defineProps<{ node: NodeRecord; compact?: boolean }>(), { compact: false });
const emit = defineEmits<{ changed: []; "session-expired": []; "view-all": [] }>();
const message = useMessage();
const dialog = useDialog();

const operations = ref<NodeOperationRecord[]>([]);
const backups = ref<ConfigBackupRecord[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
const working = ref("");
const pingTarget = ref("42.49.64.154");

const hasPending = computed(() =>
  operations.value.some((operation) => operation.status === "queued" || operation.status === "running"),
);
const canRotateRealityIdentity = computed(() => {
  const reality = props.node.reality;
  return props.node.adapter_type === "sing_box_vless_reality" && Boolean(
    props.node.agent_installation_id && reality?.public_key && reality.short_id &&
    props.node.desired_version === props.node.applied_version &&
    reality.key_generation === reality.applied_key_generation &&
    reality.material_applied_version === props.node.applied_version,
  );
});

const operationLabels: Record<NodeOperationType, string> = {
  probe_core: "探测核心",
  restart_core: "重启核心",
  tail_core_log: "有限日志",
  backup_config: "配置备份",
  ping: "延迟探测",
};

function readableError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return "登录已过期。";
  }
  const messages: Record<string, string> = {
    node_not_enrolled: "节点 Agent 尚未注册。",
    operation_conflict: "当前操作不能重试。",
    operation_unsupported: "该节点不支持此操作。",
    reality_identity_rotation_pending: "当前身份或配置尚未完成应用。",
    reality_identity_rotation_conflict: "节点状态已经变化，请刷新后重试。",
    reality_identity_rotation_unsupported: "该节点不支持 Reality 身份轮换。",
  };
  return error instanceof APIError ? (messages[error.code] ?? error.message) : fallback;
}

async function load(silent = false) {
  if (!silent) loading.value = operations.value.length === 0;
  refreshing.value = true;
  loadError.value = "";
  try {
    [operations.value, backups.value] = await Promise.all([
      api.listNodeOperations(props.node.id, props.compact ? 3 : 50),
      props.compact ? Promise.resolve([]) : api.listConfigBackups(props.node.id),
    ]);
  } catch (error) {
    loadError.value = readableError(error, "运维记录加载失败。");
  } finally {
    loading.value = false;
    refreshing.value = false;
  }
}

async function createOperation(type: NodeOperationType, target = "") {
  working.value = type;
  try {
    await api.createNodeOperation(
      props.node.id,
      type,
      type === "tail_core_log" ? 100 : 0,
      target,
    );
    await load(true);
    message.success(`${operationLabels[type]}已加入队列`);
  } catch (error) {
    message.error(readableError(error, `${operationLabels[type]}提交失败。`));
  } finally {
    working.value = "";
  }
}

async function pingNodeTarget() {
  const target = pingTarget.value.trim();
  if (!target) {
    message.warning("请输入要探测的 IPv4 或 IPv6 地址。");
    return;
  }
  await createOperation("ping", target);
}

function restartCore() {
  dialog.warning({
    title: "重启代理核心",
    content: `确认重启“${props.node.name}”的 ${props.node.core_name || "代理核心"}？现有连接会短暂中断。`,
    positiveText: "重启",
    negativeText: "取消",
    async onPositiveClick() {
      await createOperation("restart_core");
    },
  });
}

function rotateRealityIdentity() {
  const generation = props.node.reality?.key_generation;
  if (!generation || !canRotateRealityIdentity.value) return;
  dialog.warning({
    title: "轮换 Reality 身份",
    content: `确认将“${props.node.name}”轮换到第 ${generation + 1} 代身份？订阅会暂停发布该节点，直到 Agent 应用新身份。`,
    positiveText: "轮换",
    negativeText: "取消",
    async onPositiveClick() {
      working.value = "reality-identity";
      try {
        await api.rotateRealityIdentity(props.node.id, generation, props.node.desired_version);
        emit("changed");
        message.success("Reality 身份轮换已提交，等待 Agent 应用");
      } catch (error) {
        message.error(readableError(error, "Reality 身份轮换失败。"));
      } finally {
        working.value = "";
      }
    },
  });
}

async function retryOperation(operation: NodeOperationRecord) {
  working.value = `retry:${operation.id}`;
  try {
    await api.retryNodeOperation(props.node.id, operation.id);
    await load(true);
    message.success("失败操作已重新排队");
  } catch (error) {
    message.error(readableError(error, "操作重试失败。"));
  } finally {
    working.value = "";
  }
}

async function retrySync() {
  working.value = "sync";
  try {
    await api.retryNodeSync(props.node.id);
    await load(true);
    emit("changed");
    message.success("已生成新的等价快照，等待 Agent 追平");
  } catch (error) {
    message.error(readableError(error, "重新同步失败。"));
  } finally {
    working.value = "";
  }
}

function operationTag(operation: NodeOperationRecord) {
  switch (operation.status) {
    case "succeeded":
      return { label: "成功", type: "success" as const };
    case "failed":
      return { label: "失败", type: "error" as const };
    case "expired":
      return { label: "已过期", type: "warning" as const };
    case "running":
      return { label: "执行中", type: "info" as const };
    default:
      return { label: "排队中", type: "default" as const };
  }
}

let refreshTimer: number | undefined;
onMounted(() => {
  void load();
  refreshTimer = window.setInterval(() => {
    if (hasPending.value && document.visibilityState === "visible") void load(true);
  }, 4_000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
watch(
  () => props.node.id,
  () => {
    operations.value = [];
    backups.value = [];
    void load();
  },
);
</script>

<template>
  <section class="detail-section operations-panel" :class="{ 'operations-panel--compact': compact }">
    <div class="detail-section__heading">
      <h2>运维</h2>
      <div class="operations-panel__heading-actions">
        <n-button v-if="compact" text type="primary" size="small" @click="emit('view-all')">查看全部操作</n-button>
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button circle quaternary size="small" :loading="refreshing" aria-label="刷新运维记录" @click="load()">
              <template #icon><n-icon><refresh-cw /></n-icon></template>
            </n-button>
          </template>
          刷新
        </n-tooltip>
      </div>
    </div>

    <div class="operations-toolbar" aria-label="节点运维操作">
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button circle secondary :loading="working === 'probe_core'" aria-label="探测核心" @click="createOperation('probe_core')">
            <template #icon><n-icon><activity /></n-icon></template>
          </n-button>
        </template>
        探测核心
      </n-tooltip>
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button circle secondary :loading="working === 'restart_core'" aria-label="重启核心" @click="restartCore">
            <template #icon><n-icon><rotate-cw /></n-icon></template>
          </n-button>
        </template>
        重启核心
      </n-tooltip>
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            circle
            secondary
            :disabled="node.adapter_type === 'sing_box_vless_reality'"
            :loading="working === 'tail_core_log'"
            aria-label="获取有限日志"
            @click="createOperation('tail_core_log')"
          >
            <template #icon><n-icon><scroll-text /></n-icon></template>
          </n-button>
        </template>
        {{ node.adapter_type === "sing_box_vless_reality" ? "Reality 日志读取已禁用" : "最近 100 行日志" }}
      </n-tooltip>
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            circle
            secondary
            :disabled="node.adapter_type === 's_ui'"
            :loading="working === 'backup_config'"
            aria-label="备份核心配置"
            @click="createOperation('backup_config')"
          >
            <template #icon><n-icon><archive /></n-icon></template>
          </n-button>
        </template>
        {{ node.adapter_type === "s_ui" ? "S-UI 数据库不做在线文件复制" : "备份核心配置" }}
      </n-tooltip>
      <n-tooltip v-if="node.adapter_type === 'sing_box_vless_reality'" trigger="hover">
        <template #trigger>
          <n-button
            circle
            secondary
            :disabled="!canRotateRealityIdentity"
            :loading="working === 'reality-identity'"
            aria-label="轮换 Reality 身份"
            @click="rotateRealityIdentity"
          >
            <template #icon><n-icon><key-round /></n-icon></template>
          </n-button>
        </template>
        {{ canRotateRealityIdentity ? "轮换 Reality 身份" : "等待当前身份与配置完成应用" }}
      </n-tooltip>
      <n-button
        v-if="node.adapter_type !== 'standalone_sing_box'"
        size="small"
        secondary
        :loading="working === 'sync'"
        @click="retrySync"
      >
        <template #icon><n-icon><undo2 /></n-icon></template>
        重新同步
      </n-button>
    </div>

    <div class="ping-tool">
      <div class="ping-tool__identity">
        <n-icon :size="18"><radio-tower /></n-icon>
        <div>
          <strong>服务器到本机延迟</strong>
          <span>源：{{ node.name }} · {{ node.public_host || node.hostname || "Agent 所在服务器" }}</span>
        </div>
      </div>
      <div class="ping-tool__controls">
        <n-input
          v-model:value="pingTarget"
          aria-label="Ping 目标 IP"
          placeholder="IPv4 或 IPv6"
          maxlength="64"
          clearable
          @keyup.enter="pingNodeTarget"
        />
        <n-button type="primary" :loading="working === 'ping'" @click="pingNodeTarget">
          开始 Ping
        </n-button>
      </div>
      <small>固定发送 4 个 ICMP 请求，每次等待不超过 2 秒；目标默认是 42.49.64.154。</small>
    </div>

    <div v-if="loading" class="operations-state"><n-spin :size="22" /></div>
    <div v-else-if="loadError" class="operations-state operations-state--error">{{ loadError }}</div>
    <div v-else-if="operations.length" class="operation-list">
      <article v-for="operation in operations" :key="operation.id" class="operation-item">
        <header>
          <div>
            <strong>{{ operationLabels[operation.type] }}</strong>
            <span>#{{ operation.sequence }} · 第 {{ operation.attempt }} 次</span>
          </div>
          <n-tag :type="operationTag(operation).type" size="small" :bordered="false">
            {{ operationTag(operation).label }}
          </n-tag>
        </header>
        <p v-if="operation.error_code" class="operation-error">
          {{ operation.error_code }}<span v-if="operation.error_message"> · {{ operation.error_message }}</span>
        </p>
        <div class="operation-meta">
          <span>{{ relativeTime(operation.completed_at || operation.created_at) }}</span>
          <span v-if="operation.target">目标 {{ operation.target }}</span>
          <span v-if="operation.rolled_back">已恢复最近可用配置</span>
          <n-button
            v-if="operation.status === 'failed' || operation.status === 'expired'"
            text
            type="primary"
            :loading="working === `retry:${operation.id}`"
            @click="retryOperation(operation)"
          >
            重试
          </n-button>
        </div>
        <pre v-if="operation.output && !compact" class="operation-output">{{ operation.output }}</pre>
      </article>
    </div>
    <div v-else class="operations-state">尚无运维操作</div>

    <div v-if="backups.length && !compact" class="backup-list">
      <h3>节点本地备份</h3>
      <div v-for="backup in backups" :key="backup.id" class="backup-row">
        <div>
          <strong>{{ formatDateTime(backup.created_at) }}</strong>
          <span>{{ backup.local_path }}</span>
        </div>
        <span>{{ formatBytes(backup.size_bytes) }} · {{ backup.sha256.slice(0, 12) }}</span>
      </div>
    </div>
  </section>
</template>
