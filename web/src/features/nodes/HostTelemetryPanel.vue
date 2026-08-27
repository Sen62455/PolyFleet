<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Search } from "@lucide/vue";
import { NIcon, NInput, NSelect, NSpin, NTag } from "naive-ui";
import { api, APIError } from "../../api";
import { formatBytes, formatUptime, relativeTime } from "../../lib/format";
import type { NodeTelemetrySnapshot, SystemdServiceSnapshot } from "../../types";

const props = defineProps<{ nodeId: string }>();
const emit = defineEmits<{ "session-expired": [] }>();

type ProcessSort = "cpu" | "memory";
type ServiceFilter = "all" | "running" | "failed";
type ServiceSort = "name" | "cpu" | "memory";

const emptyTelemetry = (): NodeTelemetrySnapshot => ({
  supported: false,
  sampled_at: null,
  processes_available: false,
  processes_error_code: "",
  processes_total: 0,
  processes_truncated: false,
  processes_sampled_at: null,
  processes: [],
  services_available: false,
  services_error_code: "",
  services_total: 0,
  services_truncated: false,
  services_sampled_at: null,
  services: [],
});

const telemetry = ref<NodeTelemetrySnapshot>(emptyTelemetry());
const loading = ref(false);
const errorMessage = ref("");
const processSort = ref<ProcessSort>("cpu");
const serviceFilter = ref<ServiceFilter>("running");
const serviceSort = ref<ServiceSort>("name");
const serviceQuery = ref("");
const serviceSortOptions: { label: string; value: ServiceSort }[] = [
  { label: "按名称", value: "name" },
  { label: "按 CPU", value: "cpu" },
  { label: "按内存", value: "memory" },
];

const sortedProcesses = computed(() => [...telemetry.value.processes].sort((left, right) => {
  if (processSort.value === "memory") return right.rss_bytes - left.rss_bytes || right.cpu_percent - left.cpu_percent;
  return right.cpu_percent - left.cpu_percent || right.rss_bytes - left.rss_bytes;
}));

const filteredServices = computed(() => {
  const query = serviceQuery.value.trim().toLocaleLowerCase();
  const services = telemetry.value.services.filter((service) => {
    const matchesStatus = serviceFilter.value === "all"
      || (serviceFilter.value === "running" && service.active_state === "active")
      || (serviceFilter.value === "failed" && service.active_state === "failed");
    const matchesQuery = !query
      || service.unit.toLocaleLowerCase().includes(query)
      || (service.description ?? "").toLocaleLowerCase().includes(query);
    return matchesStatus && matchesQuery;
  });
  return services.sort((left, right) => {
    if (serviceSort.value === "cpu") return right.cpu_percent - left.cpu_percent || left.unit.localeCompare(right.unit);
    if (serviceSort.value === "memory") return right.memory_bytes - left.memory_bytes || left.unit.localeCompare(right.unit);
    return left.unit.localeCompare(right.unit);
  });
});

const failedServices = computed(() => telemetry.value.services.filter((service) => service.active_state === "failed").length);

function serviceTagType(service: SystemdServiceSnapshot) {
  if (service.active_state === "failed") return "error";
  if (service.active_state === "active") return "success";
  if (service.active_state === "activating" || service.active_state === "deactivating") return "warning";
  return "default";
}

function serviceStateLabel(value: string) {
  const labels: Record<string, string> = {
    active: "活动",
    inactive: "未活动",
    failed: "失败",
    activating: "启动中",
    deactivating: "停止中",
    reloading: "重载中",
    running: "运行中",
    exited: "已退出",
    dead: "已停止",
  };
  return (labels[value] ?? value) || "未知";
}

function formatWorkloadCPU(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0%";
  return `${value.toFixed(value >= 10 ? 0 : 1)}%`;
}

let refreshSequence = 0;
async function refresh(silent = false) {
  const sequence = ++refreshSequence;
  const nodeId = props.nodeId;
  if (!silent) loading.value = true;
  errorMessage.value = "";
  try {
    const snapshot = await api.getNodeTelemetry(nodeId);
    if (sequence === refreshSequence && nodeId === props.nodeId) telemetry.value = snapshot;
  } catch (error) {
    if (sequence !== refreshSequence || nodeId !== props.nodeId) return;
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    errorMessage.value = error instanceof APIError
      ? error.message
      : "进程与服务遥测加载失败。";
  } finally {
    if (sequence === refreshSequence) loading.value = false;
  }
}

let refreshTimer: number | undefined;
onMounted(() => {
  void refresh();
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") void refresh(true);
  }, 60_000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
watch(() => props.nodeId, () => {
  telemetry.value = emptyTelemetry();
  void refresh();
});

defineExpose({ refresh });
</script>

<template>
  <section class="telemetry-workspace" aria-label="进程与 systemd 服务">
    <header class="telemetry-heading">
      <div>
        <h2>进程与服务</h2>
        <span v-if="telemetry.sampled_at">Agent 上报于 {{ relativeTime(telemetry.sampled_at) }}</span>
        <span v-else>等待 Agent 上报详细遥测</span>
      </div>
      <n-tag v-if="telemetry.supported" size="small" :bordered="false">
        {{ telemetry.processes.length }} 个进程 · {{ telemetry.services.length }} 个服务
      </n-tag>
    </header>

    <div v-if="errorMessage" class="telemetry-notice telemetry-notice--error">
      {{ errorMessage }}，已有主机指标不受影响。
    </div>
    <div v-if="loading && !telemetry.sampled_at" class="telemetry-loading"><n-spin :size="24" /></div>
    <div v-else-if="!telemetry.supported" class="telemetry-notice">
      当前 Agent 尚未上报进程与 systemd 服务快照。
    </div>

    <template v-else>
      <section class="telemetry-section" aria-labelledby="top-processes-title">
        <header>
          <div>
            <h3 id="top-processes-title">主要进程</h3>
            <span>
              显示 {{ telemetry.processes.length }} / {{ telemetry.processes_total || telemetry.processes.length }} 项
              <template v-if="telemetry.processes_truncated"> · 已按资源占用截取</template>
              · 数据 {{ relativeTime(telemetry.processes_sampled_at ?? telemetry.sampled_at) }}
              · CPU 单核为 100%
            </span>
          </div>
          <div class="status-segment" aria-label="进程排序">
            <button type="button" :class="{ active: processSort === 'cpu' }" :aria-pressed="processSort === 'cpu'" @click="processSort = 'cpu'">按 CPU</button>
            <button type="button" :class="{ active: processSort === 'memory' }" :aria-pressed="processSort === 'memory'" @click="processSort = 'memory'">按内存</button>
          </div>
        </header>
        <div v-if="!telemetry.processes_available" class="telemetry-notice telemetry-notice--inline">
          进程采集不可用<span v-if="telemetry.processes_error_code">（{{ telemetry.processes_error_code }}）</span>
        </div>
        <div v-if="telemetry.processes_available && sortedProcesses.length === 0" class="telemetry-notice telemetry-notice--inline">本次采样没有进程数据。</div>
        <div v-if="sortedProcesses.length > 0" class="telemetry-table-wrap">
          <table class="telemetry-table telemetry-table--processes">
            <thead><tr><th>进程</th><th>PID</th><th>systemd 单元</th><th>CPU</th><th>内存</th><th>运行时间</th></tr></thead>
            <tbody>
              <tr v-for="process in sortedProcesses" :key="process.pid">
                <td><strong>{{ process.name || "未知进程" }}</strong></td>
                <td>{{ process.pid }}</td>
                <td :title="process.unit">{{ process.unit || "-" }}</td>
                <td>{{ formatWorkloadCPU(process.cpu_percent) }}</td>
                <td>{{ formatBytes(process.rss_bytes) }}</td>
                <td>{{ formatUptime(process.uptime_seconds) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="telemetry-section" aria-labelledby="systemd-services-title">
        <header>
          <div>
            <h3 id="systemd-services-title">Systemd 服务</h3>
            <span>
              显示 {{ filteredServices.length }} / {{ telemetry.services_total || telemetry.services.length }} 项
              <template v-if="telemetry.services_truncated"> · 已截取</template>
              · 失败 {{ failedServices }} 项
              · 数据 {{ relativeTime(telemetry.services_sampled_at ?? telemetry.sampled_at) }}
            </span>
          </div>
          <div class="telemetry-service-filters">
            <div class="status-segment" aria-label="服务状态筛选">
              <button type="button" :class="{ active: serviceFilter === 'all' }" :aria-pressed="serviceFilter === 'all'" @click="serviceFilter = 'all'">全部</button>
              <button type="button" :class="{ active: serviceFilter === 'running' }" :aria-pressed="serviceFilter === 'running'" @click="serviceFilter = 'running'">活动</button>
              <button type="button" :class="{ active: serviceFilter === 'failed' }" :aria-pressed="serviceFilter === 'failed'" @click="serviceFilter = 'failed'">失败</button>
            </div>
            <n-select v-model:value="serviceSort" :options="serviceSortOptions" size="small" aria-label="systemd 服务排序" />
            <n-input v-model:value="serviceQuery" clearable size="small" placeholder="筛选服务" aria-label="筛选 systemd 服务">
              <template #prefix><n-icon><search /></n-icon></template>
            </n-input>
          </div>
        </header>
        <div v-if="!telemetry.services_available" class="telemetry-notice telemetry-notice--inline">
          systemd 服务采集不可用<span v-if="telemetry.services_error_code">（{{ telemetry.services_error_code }}）</span>
        </div>
        <div v-if="telemetry.services_available && filteredServices.length === 0" class="telemetry-notice telemetry-notice--inline">没有符合条件的 systemd 服务。</div>
        <div v-if="filteredServices.length > 0" class="telemetry-table-wrap telemetry-table-wrap--services">
          <table class="telemetry-table telemetry-table--services">
            <thead><tr><th>服务</th><th>状态</th><th>子状态</th><th>CPU</th><th title="Agent 本次运行期间的峰值">CPU 峰值</th><th>内存</th><th>内存峰值</th><th title="当前任务数 / systemd 自动重启次数">任务 / 重启</th><th>主 PID</th></tr></thead>
            <tbody>
              <tr v-for="service in filteredServices" :key="service.unit">
                <td><strong :title="service.unit">{{ service.unit }}</strong><small>{{ service.description || "-" }}</small></td>
                <td><n-tag :type="serviceTagType(service)" size="small" :bordered="false">{{ serviceStateLabel(service.active_state) }}</n-tag></td>
                <td>{{ serviceStateLabel(service.sub_state) }}</td>
                <td>{{ formatWorkloadCPU(service.cpu_percent) }}</td>
                <td>{{ formatWorkloadCPU(service.cpu_peak_percent) }}</td>
                <td>{{ formatBytes(service.memory_bytes) }}</td>
                <td>{{ formatBytes(service.memory_peak_bytes) }}</td>
                <td>{{ service.tasks }} / {{ service.restarts }}</td>
                <td>{{ service.main_pid || "-" }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </section>
</template>
