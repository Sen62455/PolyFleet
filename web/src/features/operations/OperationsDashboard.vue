<script setup lang="ts">
import SurfaceState from "../../components/SurfaceState.vue";
import ErrorSummary from "../../components/ErrorSummary.vue";
import StatusPill from "../../components/StatusPill.vue";
import { computed, onMounted, ref, watch } from "vue";
import { Activity, CheckCircle2, CircleAlert, ClipboardList, Eye, RefreshCw, RotateCcw } from "@lucide/vue";
import {
  NButton,
  NDrawer,
  NDrawerContent,
  NIcon,
  NPagination,
  NSelect,
  NSpin,
  NTooltip,
  useMessage,
} from "naive-ui";
import IconAction from "../../components/IconAction.vue";
import { api, APIError } from "../../api";
import StatCard from "../../components/StatCard.vue";
import { formatDateTime } from "../../lib/format";
import type {
  NodeOperationRecord,
  NodeOperationStatus,
  NodeOperationType,
  NodeRecord,
} from "../../types";

const props = withDefaults(defineProps<{ nodes: NodeRecord[]; initialNodeId?: string }>(), { initialNodeId: "" });
const emit = defineEmits<{ "session-expired": []; "select-node": [nodeId: string] }>();
const message = useMessage();

const operations = ref<NodeOperationRecord[]>([]);
const total = ref(0);
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
let loadRequest = 0;
const nodeFilter = ref(props.initialNodeId);
const typeFilter = ref<NodeOperationType | "">("");
const statusFilter = ref<NodeOperationStatus | "">("");
const page = ref(1);
const pageSize = ref(20);
const selected = ref<NodeOperationRecord | null>(null);
const retrying = ref("");

const operationLabels: Record<NodeOperationType, string> = {
  probe_core: "探测核心",
  restart_core: "重启核心",
  tail_core_log: "有限日志",
  backup_config: "配置备份",
  ping: "延迟探测",
};
const nodeOptions = computed(() => [
  { label: "全部节点", value: "" },
  ...props.nodes.map((node) => ({ label: node.name, value: node.id })),
]);
const typeOptions = [
  { label: "全部操作", value: "" },
  ...Object.entries(operationLabels).map(([value, label]) => ({ label, value })),
];
const statuses: { value: NodeOperationStatus | ""; label: string }[] = [
  { value: "", label: "全部" },
  { value: "queued", label: "排队中" },
  { value: "running", label: "执行中" },
  { value: "succeeded", label: "成功" },
  { value: "failed", label: "失败" },
  { value: "expired", label: "已过期" },
];
const runningCount = computed(() => operations.value.filter((item) => item.status === "running" || item.status === "queued").length);
const failedCount = computed(() => operations.value.filter((item) => item.status === "failed" || item.status === "expired").length);
const succeededCount = computed(() => operations.value.filter((item) => item.status === "succeeded").length);

function readableError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return "登录已过期。";
  }
  return error instanceof APIError ? error.message : fallback;
}

async function load(silent = false) {
  const request = ++loadRequest;
  if (!silent) loading.value = operations.value.length === 0;
  refreshing.value = true;
  loadError.value = "";
  try {
    const result = await api.listOperations({
      node_id: nodeFilter.value,
      type: typeFilter.value,
      status: statusFilter.value,
      limit: pageSize.value,
      offset: (page.value - 1) * pageSize.value,
    });
    if (request !== loadRequest) return;
    operations.value = result.operations;
    total.value = result.total;
  } catch (error) {
    if (request === loadRequest) loadError.value = readableError(error, "操作记录加载失败。");
  } finally {
    if (request === loadRequest) { loading.value = false; refreshing.value = false; }
  }
}

async function retry(operation: NodeOperationRecord) {
  retrying.value = operation.id;
  try {
    await api.retryNodeOperation(operation.node_id, operation.id);
    message.success("失败操作已重新排队");
    await load(true);
  } catch (error) {
    message.error(readableError(error, "操作重试失败。"));
  } finally {
    retrying.value = "";
  }
}

function statusTag(status: NodeOperationStatus) {
  switch (status) {
    case "succeeded": return { label: "成功", type: "success" as const };
    case "failed": return { label: "失败", type: "error" as const };
    case "expired": return { label: "已过期", type: "error" as const };
    case "running": return { label: "执行中", type: "info" as const };
    default: return { label: "排队中", type: "warning" as const };
  }
}

function duration(operation: NodeOperationRecord) {
  const start = new Date(operation.started_at || operation.created_at).getTime();
  const end = new Date(operation.completed_at || operation.updated_at).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return "-";
  const seconds = Math.round((end - start) / 1000);
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}

function summary(operation: NodeOperationRecord) {
  if (operation.error_code) return [operation.error_code, operation.error_message].filter(Boolean).join(" · ");
  if (operation.output) return operation.output.split("\n")[0];
  if (operation.status === "running") return "Agent 正在执行";
  if (operation.status === "queued") return "等待 Agent 领取";
  return "操作已完成";
}

watch([nodeFilter, typeFilter, statusFilter], () => {
  page.value = 1;
  void load();
});
watch(page, () => void load());
watch(() => props.initialNodeId, (value) => { nodeFilter.value = value; });
onMounted(() => void load());
</script>

<template>
  <main class="workspace operations-workspace operations-log-workspace">
    <div class="page-heading">
      <div><h2>操作记录</h2><p>节点运维任务与执行结果</p></div>
      <icon-action label="刷新操作记录" :loading="refreshing" @click="load(true)"><n-icon><refresh-cw /></n-icon></icon-action>
    </div>

    <section v-if="!loading" class="stat-grid" aria-label="当前页操作摘要">
      <stat-card label="执行中" :muted="!runningCount">
        <template #icon><activity :size="18" /></template>
        {{ runningCount }}
        <template #hint>当前页排队与执行中的任务</template>
      </stat-card>
      <stat-card label="失败 / 过期" :tone="failedCount ? 'critical' : 'neutral'" :muted="!failedCount">
        <template #icon><circle-alert :size="18" /></template>
        {{ failedCount }}
        <template #hint>当前页需关注的任务</template>
      </stat-card>
      <stat-card label="成功" tone="signal" :muted="!succeededCount">
        <template #icon><check-circle2 :size="18" /></template>
        {{ succeededCount }}
        <template #hint>当前页已完成的任务</template>
      </stat-card>
      <stat-card label="匹配记录" tone="neutral">
        <template #icon><clipboard-list :size="18" /></template>
        {{ total }}
        <template #hint>符合筛选条件的全部记录</template>
      </stat-card>
    </section>

    <surface-state v-if="loadError" kind="error" :hint="loadError" :busy="refreshing" @action="load()" />
    <section class="ops-surface operations-table-surface" aria-label="操作记录">
      <header class="operations-filters" aria-label="操作记录筛选">
        <n-select v-model:value="nodeFilter" :options="nodeOptions" :consistent-menu-width="false" aria-label="筛选节点" />
        <n-select v-model:value="typeFilter" :options="typeOptions" :consistent-menu-width="false" aria-label="筛选操作类型" />
        <div class="status-segment">
          <button
            v-for="item in statuses"
            :key="item.value"
            type="button"
            :class="{ active: statusFilter === item.value }"
            :aria-pressed="statusFilter === item.value"
            @click="statusFilter = item.value"
          >{{ item.label }}</button>
        </div>
      </header>
      <surface-state v-if="loading" kind="loading" stats />
      <surface-state v-else-if="!operations.length && !loadError" kind="empty" title="没有匹配的操作记录" hint="调整筛选查看历史记录，或从节点详情发起运维操作。" action-label="重置并刷新" @action="nodeFilter = ''; typeFilter = ''; statusFilter = ''; page = 1; load()" />
      <div v-else class="operations-table-wrap">
        <table class="operations-table">
          <thead><tr><th>时间</th><th>节点</th><th>操作</th><th class="num">序号 / 尝试</th><th>状态</th><th class="num">耗时</th><th>结果摘要</th><th>操作人</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="operation in operations" :key="operation.id">
              <td>{{ formatDateTime(operation.created_at, false) }}</td>
              <td><button type="button" class="table-link" @click="emit('select-node', operation.node_id)">{{ operation.node_name }}</button></td>
              <td>{{ operationLabels[operation.type] }}</td>
              <td class="num">#{{ operation.sequence }} / {{ operation.attempt }}</td>
              <td><status-pill :type="statusTag(operation.status).type">{{ statusTag(operation.status).label }}</status-pill></td>
              <td class="num">{{ duration(operation) }}</td>
              <td><error-summary v-if="operation.error_code || operation.error_message" :code="operation.error_code" :message="operation.error_message" /><span v-else class="operation-summary-text clamp-2" :class="{ mono: operation.output }" :title="summary(operation)">{{ summary(operation) }}</span></td>
              <td>{{ operation.requested_by || "-" }}</td>
              <td>
                <div class="row-actions">
                  <icon-action v-if="operation.status === 'failed' || operation.status === 'expired'" label="重试操作" :loading="retrying === operation.id" @click="retry(operation)"><n-icon><rotate-ccw /></n-icon></icon-action>
                  <icon-action label="查看操作详情" @click="selected = operation"><n-icon><eye /></n-icon></icon-action>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer v-if="total > pageSize" class="operations-pagination">
        <span>共 {{ total }} 条</span>
        <n-pagination v-model:page="page" :page-size="pageSize" :item-count="total" />
      </footer>
    </section>

    <n-drawer :show="selected !== null" width="min(500px, 100vw)" @update:show="!$event && (selected = null)">
      <n-drawer-content v-if="selected" title="操作详情" closable>
        <div class="operation-detail-title">
          <status-pill :type="statusTag(selected.status).type">{{ statusTag(selected.status).label }}</status-pill>
          <strong>{{ operationLabels[selected.type] }}</strong><span>#{{ selected.sequence }}</span>
        </div>
        <dl class="detail-list">
          <div><dt>节点</dt><dd>{{ selected.node_name }}</dd></div>
          <div><dt>尝试</dt><dd>第 {{ selected.attempt }} 次</dd></div>
          <div v-if="selected.target"><dt>目标 IP</dt><dd class="mono">{{ selected.target }}</dd></div>
          <div><dt>提交时间</dt><dd>{{ formatDateTime(selected.created_at, false) }}</dd></div>
          <div><dt>完成时间</dt><dd>{{ formatDateTime(selected.completed_at, false) }}</dd></div>
          <div><dt>耗时</dt><dd>{{ duration(selected) }}</dd></div>
          <div><dt>操作人</dt><dd>{{ selected.requested_by || "-" }}</dd></div>
          <div v-if="selected.rolled_back"><dt>回滚</dt><dd>已恢复最近可用配置</dd></div>
        </dl>
        <error-summary v-if="selected.error_code || selected.error_message" :code="selected.error_code" :message="selected.error_message" />
        <section v-if="selected.output" class="operation-detail-output"><h3>输出</h3><pre>{{ selected.output }}</pre></section>
      </n-drawer-content>
    </n-drawer>
  </main>
</template>

<style scoped>
.operations-table-surface {
  overflow: hidden;
  border-radius: var(--hf-radius);
}

.operations-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding: 12px 16px;
  border-bottom: 1px solid var(--hf-line);
  background: transparent;
}

.operations-filters :deep(.n-select) {
  width: 220px;
  flex: 0 0 auto;
}

.operations-filters .status-segment {
  margin-left: auto;
}

@media (max-width: 900px) {
  .operations-filters .status-segment {
    margin-left: 0;
  }
}

@media (max-width: 640px) {
  .operations-filters :deep(.n-select) {
    width: 100%;
    flex: 1 1 100%;
  }
}
</style>
