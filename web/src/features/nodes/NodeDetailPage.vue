<script setup lang="ts">
import HintTooltip from "../../components/HintTooltip.vue";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Globe2,
  KeyRound,
  MapPin,
  Pencil,
  RefreshCw,
} from "@lucide/vue";
import { NButton, NIcon, NInputNumber, NProgress, NSelect, NSpin, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import MetricChart, { type ChartSeries } from "../../components/MetricChart.vue";
import NodeSignalRail from "../../components/NodeSignalRail.vue";
import StatusIndicator from "../../components/StatusIndicator.vue";
import {
  adapterLabels,
  formatBytes,
  formatPercent,
  formatRate,
  formatUptime,
  percent,
  progressStatus,
  relativeTime,
  severity,
} from "../../lib/format";
import type { MetricRange, NodeMetricSeries, NodeRecord } from "../../types";
import HostTelemetryPanel from "./HostTelemetryPanel.vue";
import NodeOperationsPanel from "./NodeOperationsPanel.vue";
import SUIAdapterPanel from "./SUIAdapterPanel.vue";

const props = defineProps<{ node: NodeRecord }>();
const message = useMessage();
const emit = defineEmits<{
  back: [];
  edit: [node: NodeRecord];
  enroll: [node: NodeRecord];
  changed: [];
  operations: [nodeId: string];
  "session-expired": [];
}>();

const metricRange = ref<MetricRange>("24h");
const metrics = ref<NodeMetricSeries>({ range: "24h", step_seconds: 60, samples: [] });
const metricsLoading = ref(false);
const metricsError = ref("");
const telemetryPanel = ref<InstanceType<typeof HostTelemetryPanel> | null>(null);
type TrafficUnit = "GiB" | "TiB";
const calibrationValue = ref(0);
const calibrationUnit = ref<TrafficUnit>("GiB");
const calibrationWorking = ref(false);
const trafficUnitOptions = [
  { label: "GiB", value: "GiB" },
  { label: "TiB", value: "TiB" },
];
const trafficUnitBytes: Record<TrafficUnit, number> = {
  GiB: 1024 ** 3,
  TiB: 1024 ** 4,
};
const ranges: { value: MetricRange; label: string }[] = [
  { value: "1h", label: "1 小时" },
  { value: "6h", label: "6 小时" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
];
const selectedRangeLabel = computed(() => ranges.find((item) => item.value === metricRange.value)?.label ?? "24 小时");
const memoryPercent = computed(() => percent(props.node.memory_used_bytes, props.node.memory_total_bytes));
const diskPercent = computed(() => percent(props.node.disk_used_bytes, props.node.disk_total_bytes));
const cycleProxyUsed = computed(
  () => props.node.traffic_cycle_upload_bytes + props.node.traffic_cycle_download_bytes,
);
const trafficRemaining = computed(() =>
  props.node.traffic_limit_bytes > 0
    ? Math.max(0, props.node.traffic_limit_bytes - props.node.traffic_used_bytes)
    : 0,
);
const trafficQuotaPercent = computed(() =>
  props.node.traffic_limit_bytes > 0
    ? percent(props.node.traffic_used_bytes, props.node.traffic_limit_bytes)
    : 0,
);
const trafficQuotaLabel = computed(() => {
  if (props.node.traffic_limit_bytes <= 0) return "不限额";
  const raw = (props.node.traffic_used_bytes / props.node.traffic_limit_bytes) * 100;
  return `${raw.toFixed(raw >= 10 ? 0 : 1)}%`;
});

function meterWidth(value: number) {
  return `${Math.max(0, Math.min(100, value))}%`;
}

function loadTone(value: number) {
  const level = severity(value);
  return level === "normal" ? "" : `is-${level}`;
}

const labels = computed(() => metrics.value.samples.map((sample) => sample.bucket_at));
const cpuSeries = computed<ChartSeries[]>(() => [{
  name: "CPU", color: "var(--hf-chart-primary)", values: metrics.value.samples.map((sample) => sample.cpu_percent),
}]);
const memorySeries = computed<ChartSeries[]>(() => [
  {
    name: "内存", color: "var(--hf-chart-primary)",
    values: metrics.value.samples.map((sample) => percent(sample.memory_used_bytes, sample.memory_total_bytes)),
  },
  {
    name: "Swap", color: "var(--hf-chart-tertiary)",
    values: metrics.value.samples.map((sample) => percent(sample.swap_used_bytes, sample.swap_total_bytes)),
  },
]);
const networkSeries = computed<ChartSeries[]>(() => [
  { name: "下行", color: "var(--hf-chart-primary)", values: metrics.value.samples.map((sample) => sample.network_rx_bps) },
  { name: "上行", color: "var(--hf-chart-secondary)", values: metrics.value.samples.map((sample) => sample.network_tx_bps) },
]);
const diskSeries = computed<ChartSeries[]>(() => [
  { name: "读取", color: "var(--hf-chart-primary)", values: metrics.value.samples.map((sample) => sample.disk_read_bytes_per_second) },
  { name: "写入", color: "var(--hf-chart-tertiary)", values: metrics.value.samples.map((sample) => sample.disk_write_bytes_per_second) },
]);

async function loadMetrics(silent = false) {
  if (!silent) metricsLoading.value = true;
  metricsError.value = "";
  try {
    metrics.value = await api.getNodeMetrics(props.node.id, metricRange.value);
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    metricsError.value = error instanceof APIError ? error.message : "历史指标加载失败。";
  } finally {
    metricsLoading.value = false;
  }
}

function refreshMonitoring() {
  void loadMetrics();
  void telemetryPanel.value?.refresh();
}

function endpointLabel(node: NodeRecord) {
  if (!node.public_host) return "未配置";
  const host = node.public_host.includes(":") ? `[${node.public_host}]` : node.public_host;
  return `${host}:${node.public_port}`;
}

function realityHandshakeLabel(node: NodeRecord) {
  if (!node.reality?.handshake_server) return "未配置";
  return `${node.reality.handshake_server}:${node.reality.handshake_port || 443}`;
}

function utcDateLabel(value: Date) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone: "UTC",
  }).format(value);
}

function trafficCycleLabel(node: NodeRecord) {
  if (!node.traffic_cycle_started_at) return "等待首批流量数据";
  const start = new Date(node.traffic_cycle_started_at);
  if (!Number.isFinite(start.getTime())) return "周期时间未知";
  const nextMonth = start.getUTCMonth() + 1;
  const lastDay = new Date(Date.UTC(start.getUTCFullYear(), nextMonth + 1, 0)).getUTCDate();
  const end = new Date(Date.UTC(
    start.getUTCFullYear(),
    nextMonth,
    Math.min(node.traffic_reset_day, lastDay),
  ));
  return `${utcDateLabel(start)} - ${utcDateLabel(end)} UTC`;
}

async function calibrateTraffic() {
  calibrationWorking.value = true;
  try {
    const bytes = Math.round(calibrationValue.value * trafficUnitBytes[calibrationUnit.value]);
    await api.calibrateNodeTraffic(props.node.id, bytes);
    emit("changed");
    message.success("运营商流量已校准");
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    message.error(error instanceof APIError ? error.message : "流量校准失败。");
  } finally {
    calibrationWorking.value = false;
  }
}

let refreshTimer: number | undefined;
onMounted(() => {
  void loadMetrics();
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") void loadMetrics(true);
  }, 30_000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
watch(metricRange, () => void loadMetrics());
watch(() => props.node.id, () => void loadMetrics());
</script>

<template>
  <main class="workspace node-workbench">
    <header class="node-workbench__hero">
      <n-button quaternary circle aria-label="返回节点" @click="emit('back')">
        <template #icon><n-icon><arrow-left /></n-icon></template>
      </n-button>
      <div class="node-workbench__identity">
        <div class="node-workbench__title">
          <status-indicator :status="node.status" />
          <h1>{{ node.name }}</h1>
          <small>{{ adapterLabels[node.adapter_type] }}</small>
        </div>
        <div class="node-workbench__meta">
          <span><map-pin :size="14" />{{ [node.provider, node.region].filter(Boolean).join(" · ") || "未设置位置" }}</span>
          <span class="mono"><globe-2 :size="14" />{{ endpointLabel(node) }}</span>
          <span>心跳 {{ relativeTime(node.last_seen_at) }}</span>
          <span>运行 {{ formatUptime(node.uptime_seconds) }}</span>
        </div>
      </div>
      <div class="node-workbench__actions">
        <hint-tooltip trigger="hover">
          <template #trigger>
            <n-button circle secondary aria-label="刷新监控" :loading="metricsLoading" @click="refreshMonitoring">
              <template #icon><n-icon><refresh-cw /></n-icon></template>
            </n-button>
          </template>
          刷新
        </hint-tooltip>
        <n-button secondary @click="emit('edit', node)">
          <template #icon><n-icon><pencil /></n-icon></template>编辑
        </n-button>
        <n-button type="primary" @click="emit('enroll', node)">
          <template #icon><n-icon><key-round /></n-icon></template>注册 Agent
        </n-button>
      </div>
    </header>

    <article class="workbench-card workbench-card--rail">
      <node-signal-rail :node="node" compact />
    </article>

    <section class="node-workbench__split">
      <article class="workbench-card">
        <header>
          <h2>当前负载</h2>
          <small>{{ node.online_connections }} 连接 · {{ node.online_users }} 用户</small>
        </header>
        <div class="probe-meters">
          <div class="probe-meter" :class="loadTone(node.cpu_percent)">
            <span>CPU</span>
            <i><b :style="{ width: meterWidth(node.cpu_percent) }" /></i>
            <strong>{{ formatPercent(node.cpu_percent) }}</strong>
            <em>{{ node.cpu_cores || "-" }} 核 · 负载 {{ node.load_1.toFixed(2) }}</em>
          </div>
          <div class="probe-meter" :class="loadTone(memoryPercent)">
            <span>内存</span>
            <i><b :style="{ width: meterWidth(memoryPercent) }" /></i>
            <strong>{{ formatPercent(memoryPercent) }}</strong>
            <em>{{ formatBytes(node.memory_used_bytes) }} / {{ formatBytes(node.memory_total_bytes) }}</em>
          </div>
          <div class="probe-meter" :class="loadTone(diskPercent)">
            <span>磁盘</span>
            <i><b :style="{ width: meterWidth(diskPercent) }" /></i>
            <strong>{{ formatPercent(diskPercent) }}</strong>
            <em>{{ formatBytes(node.disk_used_bytes) }} / {{ formatBytes(node.disk_total_bytes) }}</em>
          </div>
        </div>
        <dl class="probe-facts">
          <div><dt>下行</dt><dd><arrow-down :size="12" />{{ formatRate(node.network_rx_bps) }}</dd></div>
          <div><dt>上行</dt><dd><arrow-up :size="12" />{{ formatRate(node.network_tx_bps) }}</dd></div>
          <div><dt>流量</dt><dd>{{ formatBytes(node.traffic_used_bytes) }} / {{ node.traffic_limit_bytes ? formatBytes(node.traffic_limit_bytes) : "不限" }}</dd></div>
          <div><dt>出口</dt><dd>{{ node.exit_proxy ? node.exit_proxy.name : "节点直出" }}</dd></div>
          <div><dt>负载 5 / 15</dt><dd>{{ node.load_5.toFixed(2) }} / {{ node.load_15.toFixed(2) }}</dd></div>
          <div><dt>Swap</dt><dd>{{ formatBytes(node.swap_used_bytes) }} / {{ formatBytes(node.swap_total_bytes) }}</dd></div>
          <div><dt>运行时间</dt><dd>{{ formatUptime(node.uptime_seconds) }}</dd></div>
          <div><dt>Agent</dt><dd>{{ node.agent_version || "尚未注册" }}</dd></div>
        </dl>
      </article>

      <article class="workbench-card workbench-card--ops">
        <node-operations-panel
          v-if="node.agent_installation_id"
          :node="node"
          compact
          @view-all="emit('operations', node.id)"
          @changed="emit('changed')"
          @session-expired="emit('session-expired')"
        />
        <div v-else class="ops-empty">
          <strong>尚未注册 Agent</strong>
          <span>注册后可探测核心、重启、备份配置和 Ping。</span>
          <n-button type="primary" size="small" @click="emit('enroll', node)">注册 Agent</n-button>
        </div>
      </article>
    </section>

    <section class="workbench-card" aria-label="节点性能">
      <header class="monitor-workspace__heading">
        <div>
          <h2>性能轨迹</h2>
          <span>{{ selectedRangeLabel }}<template v-if="metrics.step_seconds > 60"> · {{ Math.round(metrics.step_seconds / 60) }} 分钟聚合</template></span>
        </div>
        <div class="range-segment" aria-label="监控时间范围">
          <button
            v-for="item in ranges"
            :key="item.value"
            type="button"
            :class="{ active: metricRange === item.value }"
            :aria-pressed="metricRange === item.value"
            @click="metricRange = item.value"
          >{{ item.label }}</button>
        </div>
      </header>
      <div v-if="metricsError" class="monitor-error">{{ metricsError }}</div>
      <div v-if="metricsLoading && metrics.samples.length === 0" class="monitor-loading"><n-spin :size="26" /></div>
      <section v-else class="monitor-grid monitor-ledger">
        <article class="monitor-panel monitor-panel--cpu">
          <header><h2>CPU 使用率</h2><span>{{ formatPercent(node.cpu_percent) }}</span></header>
          <metric-chart label="CPU 使用率" :series="cpuSeries" :labels="labels" :value-formatter="(value) => `${value.toFixed(0)}%`" />
        </article>
        <article class="monitor-panel monitor-panel--memory">
          <header><h2>内存与 Swap</h2><span>{{ formatBytes(node.memory_used_bytes) }} / {{ formatBytes(node.memory_total_bytes) }}</span></header>
          <metric-chart label="内存与 Swap" :series="memorySeries" :labels="labels" :value-formatter="(value) => `${value.toFixed(0)}%`" />
        </article>
        <article class="monitor-panel monitor-panel--network">
          <header><h2>网络速率</h2><span>{{ formatRate(node.network_rx_bps + node.network_tx_bps) }}</span></header>
          <metric-chart label="网络速率" :series="networkSeries" :labels="labels" :value-formatter="formatRate" />
        </article>
        <article class="monitor-panel monitor-panel--disk">
          <header><h2>磁盘 I/O</h2><span>{{ formatBytes(node.disk_read_bytes_per_second + node.disk_write_bytes_per_second) }}/s</span></header>
          <metric-chart label="磁盘 I/O" :series="diskSeries" :labels="labels" :value-formatter="(value) => `${formatBytes(value)}/s`" />
        </article>
      </section>
    </section>

    <section class="node-workbench__split">
      <article class="workbench-card">
        <header>
          <h2>系统信息</h2>
          <small :class="{ mono: node.hostname }">{{ node.hostname || "尚未上报主机名" }}</small>
        </header>
        <dl class="detail-list detail-list--two">
          <div><dt>操作系统</dt><dd>{{ [node.os_name, node.os_version].filter(Boolean).join(" ") || "尚未上报" }}</dd></div>
          <div><dt>架构 / 内核</dt><dd class="mono">{{ [node.architecture || "-", node.kernel_version].filter(Boolean).join(" · ") }}</dd></div>
          <div><dt>Agent / 核心</dt><dd><span :class="{ mono: node.agent_version }">{{ node.agent_version || "尚未注册" }}</span> · <span :class="{ mono: node.core_version }">{{ [node.core_name, node.core_version].filter(Boolean).join(" ") || "尚未上报" }}</span></dd></div>
          <div><dt>负载 1 / 5 / 15</dt><dd>{{ node.load_1.toFixed(2) }} / {{ node.load_5.toFixed(2) }} / {{ node.load_15.toFixed(2) }}</dd></div>
          <div><dt>累计收发</dt><dd>{{ formatBytes(node.network_rx_bytes_total) }} / {{ formatBytes(node.network_tx_bytes_total) }}</dd></div>
          <div><dt>在线 / 未识别</dt><dd>{{ node.online_users }} 用户 · {{ node.online_unknown_users }} 未识别</dd></div>
          <div>
            <dt>出口代理</dt>
            <dd>{{ node.exit_proxy ? `${node.exit_proxy.name} · ${node.exit_proxy.host}:${node.exit_proxy.port}` : "节点直出" }}</dd>
          </div>
          <template v-if="node.adapter_type === 'sing_box_vless_reality'">
            <div><dt>SNI / 握手</dt><dd>{{ node.sni || "未配置" }} · {{ realityHandshakeLabel(node) }}</dd></div>
            <div><dt>Reality 身份</dt><dd>{{ node.reality?.public_key && node.reality?.short_id ? `已应用 · 第 ${node.reality.applied_key_generation} 代` : "等待节点生成" }}</dd></div>
          </template>
          <template v-else>
            <div><dt>证书 / 公钥固定</dt><dd>{{ node.tls_cert_fingerprint ? "证书已配置" : "证书未配置" }} · {{ node.tls_public_key_sha256 ? "公钥已配置" : "公钥未配置" }}</dd></div>
          </template>
        </dl>
      </article>

      <article class="workbench-card node-traffic-budget">
        <header>
          <h2>月流量额度</h2>
          <small>{{ trafficCycleLabel(node) }}</small>
        </header>
        <div class="node-traffic-budget__summary">
          <strong>{{ formatBytes(node.traffic_used_bytes) }}</strong>
          <span>/ {{ node.traffic_limit_bytes > 0 ? formatBytes(node.traffic_limit_bytes) : "不限额" }}</span>
          <b>{{ trafficQuotaLabel }}</b>
        </div>
        <n-progress
          v-if="node.traffic_limit_bytes > 0"
          type="line"
          :percentage="trafficQuotaPercent"
          :show-indicator="false"
          :status="progressStatus(trafficQuotaPercent)"
        />
        <dl class="detail-list detail-list--two">
          <div><dt>周期上传 / 下载</dt><dd>{{ formatBytes(node.traffic_cycle_upload_bytes) }} / {{ formatBytes(node.traffic_cycle_download_bytes) }}</dd></div>
          <div><dt>双向合计 / 剩余</dt><dd>{{ formatBytes(cycleProxyUsed) }} / {{ node.traffic_limit_bytes > 0 ? formatBytes(trafficRemaining) : "不限额" }}</dd></div>
          <div><dt>运营商校准</dt><dd>{{ node.traffic_calibration_bytes === null ? "尚未校准" : formatBytes(node.traffic_calibration_bytes) }}</dd></div>
          <div><dt>最近校准</dt><dd>{{ relativeTime(node.traffic_calibrated_at) }}</dd></div>
        </dl>
        <div class="node-traffic-budget__calibration">
          <n-input-number
            v-model:value="calibrationValue"
            :min="0"
            :max="8388607"
            :precision="2"
            aria-label="运营商本周期已用流量"
            placeholder="运营商本周期已用量"
          />
          <n-select
            v-model:value="calibrationUnit"
            :options="trafficUnitOptions"
            :consistent-menu-width="false"
            aria-label="运营商流量单位"
          />
          <n-button type="primary" :loading="calibrationWorking" @click="calibrateTraffic">
            校准用量
          </n-button>
        </div>
      </article>
    </section>

    <host-telemetry-panel
      ref="telemetryPanel"
      :node-id="node.id"
      @session-expired="emit('session-expired')"
    />

    <s-u-i-adapter-panel
      v-if="node.adapter_type === 's_ui'"
      :node="node"
      @changed="emit('changed')"
      @session-expired="emit('session-expired')"
    />
  </main>
</template>

<style scoped>
.node-workbench {
  display: grid;
  gap: 16px;
}

.node-workbench__hero {
  display: flex;
  min-height: 64px;
  align-items: center;
  gap: 12px;
}

.node-workbench__identity {
  min-width: 0;
  flex: 1;
}

.node-workbench__title {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.node-workbench__title h1 {
  overflow: hidden;
  margin: 0;
  min-width: 0;
  font-size: 28px;
  font-weight: 650;
  letter-spacing: -0.03em;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-workbench__title small {
  flex: 0 0 auto;
  color: var(--hf-muted);
  font-size: 12px;
  font-weight: 600;
}

.node-workbench__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 16px;
  margin-top: 6px;
  color: var(--hf-muted);
  font-size: 12px;
}

.node-workbench__meta span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.node-workbench__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
}

.node-workbench__split {
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(0, 0.95fr);
  gap: 16px;
  align-items: stretch;
}

.node-workbench__split > .workbench-card {
  display: flex;
  min-height: 0;
  flex-direction: column;
  height: 100%;
}

.workbench-card {
  min-width: 0;
  padding: 16px 18px;
  border: 1px solid var(--hf-line);
  border-radius: var(--hf-radius);
  background: var(--hf-surface);
  box-shadow: var(--hf-shadow);
}

.workbench-card--rail {
  padding: 0;
}

.workbench-card > header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.workbench-card > header h2,
.node-workbench :deep(.monitor-workspace__heading h2) {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.workbench-card > header small,
.node-workbench :deep(.monitor-workspace__heading span) {
  color: var(--hf-muted);
  font-size: 12px;
}

.node-workbench :deep(.monitor-workspace__heading) {
  min-height: 0;
  margin: 0 0 14px;
}

.probe-meters {
  display: grid;
  align-content: start;
  gap: 14px;
}

.probe-meter {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: baseline;
  gap: 6px 10px;
}

.probe-meter span {
  color: var(--hf-muted);
  font-size: 12px;
  font-weight: 600;
}

.probe-meter i {
  grid-column: 1 / -1;
  grid-row: 2;
  display: block;
  height: 7px;
  overflow: hidden;
  border-radius: 99px;
  background: color-mix(in srgb, var(--hf-line-strong) 80%, transparent);
}

.probe-meter i b {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--hf-accent);
}

.probe-meter.is-warning i b {
  background: var(--hf-pressure);
}

.probe-meter.is-critical i b {
  background: var(--hf-critical);
}

.probe-meter strong {
  grid-column: 3;
  grid-row: 1;
  font-family: var(--hf-data);
  font-size: 12px;
  font-weight: 620;
  text-align: right;
}

.probe-meter em {
  grid-column: 2 / 3;
  grid-row: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  margin: 0;
  color: var(--hf-muted);
  font-family: var(--hf-data);
  font-size: 12px;
  font-style: normal;
}

.probe-facts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 16px;
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--hf-line);
}

.probe-facts dt {
  color: var(--hf-muted);
  font-size: 12px;
}

.probe-facts dd {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 3px 0 0;
  font-family: var(--hf-data);
  font-size: 12px;
}

.ops-empty {
  display: grid;
  flex: 1;
  min-height: 180px;
  place-content: center;
  justify-items: center;
  gap: 8px;
  text-align: center;
}

.ops-empty strong {
  font-size: 14px;
}

.ops-empty span {
  color: var(--hf-muted);
  font-size: 12px;
}

.node-workbench :deep(.operations-panel.detail-section) {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  padding: 0;
  border: 0;
}

.node-workbench :deep(.operations-panel .detail-section__heading) {
  margin-bottom: 10px;
}

.node-workbench :deep(.operations-panel .detail-section__heading h2) {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.node-workbench :deep(.operations-toolbar) {
  margin-bottom: 10px;
}

.node-workbench :deep(.ping-tool) { display: block; padding: 14px 0 0; margin-bottom: 12px; border: 0; border-top: 1px solid var(--hf-line); border-radius: 0; background: transparent; }

.node-workbench :deep(.ping-tool__identity span),
.node-workbench :deep(.ping-tool > small) {
  display: block;
}

.node-workbench :deep(.ping-tool__controls) {
  flex: 1;
  min-width: 0;
  flex-direction: row;
  align-items: center;
}

.node-workbench :deep(.operation-list) {
  flex: 1;
  min-height: 0;
  margin-top: auto;
  overflow-y: auto;
  border-top: 1px solid var(--hf-line);
}

.node-workbench :deep(.operation-item) {
  padding: 8px 0;
}

.node-workbench :deep(.node-signal-rail--compact) {
  padding: 0;
}

.node-workbench :deep(.node-signal-rail) {
  gap: 0;
}

.node-workbench :deep(.node-signal-rail__stage) { min-height: 0; padding: 16px 18px; border: 0; border-radius: 0; background: transparent; }

.node-workbench :deep(.node-signal-rail__stage > span) {
  background: transparent;
}

.node-workbench :deep(.telemetry-workspace) {
  margin-top: 0;
  padding: 16px 18px;
  border: 1px solid var(--hf-line);
  border-radius: var(--hf-radius);
  background: var(--hf-surface);
  box-shadow: var(--hf-shadow);
}

.node-workbench :deep(.telemetry-heading) {
  min-height: 0;
  margin-bottom: 12px;
}

.node-workbench :deep(.telemetry-section) {
  border-radius: 12px;
}

.node-workbench :deep(.monitor-panel) {
  overflow: hidden;
  border-radius: 12px;
  box-shadow: none;
}

.node-workbench :deep(.range-segment) {
  overflow: hidden;
  border-radius: var(--hf-radius-pill);
}

.node-traffic-budget :deep(.n-progress) {
  margin: 0 0 16px;
}

.node-traffic-budget .detail-list {
  margin-top: 2px;
}

.node-traffic-budget__summary {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
}

.node-traffic-budget__summary strong {
  font-family: var(--hf-data);
  font-size: 28px;
  font-weight: 620;
  letter-spacing: -0.03em;
}

.node-traffic-budget__summary span,
.node-traffic-budget__summary b {
  color: var(--hf-muted);
  font-size: 12px;
}

.node-traffic-budget__calibration {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 88px auto;
  gap: 8px;
  margin-top: 14px;
}

@media (max-width: 1080px) {
  .node-workbench__split {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 720px) {
  .node-workbench__hero {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .node-workbench__actions {
    width: 100%;
    justify-content: flex-end;
  }

  .node-traffic-budget__calibration {
    grid-template-columns: 1fr;
  }
}
</style>
