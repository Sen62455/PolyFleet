<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { Eye, RefreshCw, RotateCcw } from "@lucide/vue";
import {
  NButton,
  NDrawer,
  NDrawerContent,
  NIcon,
  NPagination,
  NSelect,
  NSpin,
  NTag,
  NTooltip,
  useMessage,
} from "naive-ui";
import { api, APIError } from "../../api";
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
    operations.value = result.operations;
    total.value = result.total;
  } catch (error) {
    loadError.value = readableError(error, "操作记录加载失败。");
  } finally {
    loading.value = false;
    refreshing.value = false;
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
    case "expired": return { label: "已过期", type: "warning" as const };
    case "running": return { label: "执行中", type: "info" as const };
    default: return { label: "排队中", type: "default" as const };
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
  <main class="workspace operations-workspace">
    <div class="page-heading">
      <div><h1>操作记录</h1><p>节点运维任务与执行结果</p></div>
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button circle secondary aria-label="刷新操作记录" :loading="refreshing" @click="load(true)">
            <template #icon><n-icon><refresh-cw /></n-icon></template>
          </n-button>
        </template>
        刷新
      </n-tooltip>
    </div>

    <section class="operations-summary" aria-label="当前页操作摘要">
      <div><span>执行中</span><strong>{{ runningCount }}</strong></div>
      <div class="operations-summary--danger"><span>失败 / 过期</span><strong>{{ failedCount }}</strong></div>
      <div class="operations-summary--success"><span>成功</span><strong>{{ succeededCount }}</strong></div>
      <div><span>匹配记录</span><strong>{{ total }}</strong></div>
    </section>

    <section class="operations-filters" aria-label="操作记录筛选">
      <n-select v-model:value="nodeFilter" :options="nodeOptions" aria-label="筛选节点" />
      <n-select v-model:value="typeFilter" :options="typeOptions" aria-label="筛选操作类型" />
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
    </section>

    <div v-if="loadError" class="operations-page-error">{{ loadError }}</div>
    <section class="operations-table-surface">
      <div v-if="loading" class="operations-page-state"><n-spin :size="26" /></div>
      <div v-else-if="operations.length === 0" class="operations-page-state">没有匹配的操作记录</div>
      <div v-else class="operations-table-wrap">
        <table class="operations-table">
          <thead><tr><th>时间</th><th>节点</th><th>操作</th><th>序号 / 尝试</th><th>状态</th><th>耗时</th><th>结果摘要</th><th>操作人</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="operation in operations" :key="operation.id">
              <td>{{ formatDateTime(operation.created_at, false) }}</td>
              <td><button type="button" class="table-link" @click="emit('select-node', operation.node_id)">{{ operation.node_name }}</button></td>
              <td>{{ operationLabels[operation.type] }}</td>
              <td>#{{ operation.sequence }} / {{ operation.attempt }}</td>
              <td><n-tag :type="statusTag(operation.status).type" size="small" :bordered="false">{{ statusTag(operation.status).label }}</n-tag></td>
              <td>{{ duration(operation) }}</td>
              <td><span class="operation-summary-text" :class="{ error: operation.error_code }">{{ summary(operation) }}</span></td>
              <td>{{ operation.requested_by || "-" }}</td>
              <td>
                <div class="row-actions">
                  <n-tooltip v-if="operation.status === 'failed' || operation.status === 'expired'" trigger="hover">
                    <template #trigger>
                      <n-button circle quaternary size="small" aria-label="重试操作" :loading="retrying === operation.id" @click="retry(operation)">
                        <template #icon><n-icon><rotate-ccw /></n-icon></template>
                      </n-button>
                    </template>重试
                  </n-tooltip>
                  <n-tooltip trigger="hover">
                    <template #trigger>
                      <n-button circle quaternary size="small" aria-label="查看操作详情" @click="selected = operation">
                        <template #icon><n-icon><eye /></n-icon></template>
                      </n-button>
                    </template>详情
                  </n-tooltip>
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
          <n-tag :type="statusTag(selected.status).type" size="small" :bordered="false">{{ statusTag(selected.status).label }}</n-tag>
          <strong>{{ operationLabels[selected.type] }}</strong><span>#{{ selected.sequence }}</span>
        </div>
        <dl class="detail-list">
          <div><dt>节点</dt><dd>{{ selected.node_name }}</dd></div>
          <div><dt>尝试</dt><dd>第 {{ selected.attempt }} 次</dd></div>
          <div v-if="selected.target"><dt>目标 IP</dt><dd>{{ selected.target }}</dd></div>
          <div><dt>提交时间</dt><dd>{{ formatDateTime(selected.created_at, false) }}</dd></div>
          <div><dt>完成时间</dt><dd>{{ formatDateTime(selected.completed_at, false) }}</dd></div>
          <div><dt>耗时</dt><dd>{{ duration(selected) }}</dd></div>
          <div><dt>操作人</dt><dd>{{ selected.requested_by || "-" }}</dd></div>
          <div v-if="selected.rolled_back"><dt>回滚</dt><dd>已恢复最近可用配置</dd></div>
        </dl>
        <p v-if="selected.error_code" class="operation-error">{{ selected.error_code }}<span v-if="selected.error_message"> · {{ selected.error_message }}</span></p>
        <section v-if="selected.output" class="operation-detail-output"><h3>输出</h3><pre>{{ selected.output }}</pre></section>
      </n-drawer-content>
    </n-drawer>
  </main>
</template>
