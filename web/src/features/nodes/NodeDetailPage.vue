<script setup lang="ts">
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
import { NButton, NIcon, NInputNumber, NProgress, NSelect, NSpin, NTooltip, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import MetricChart, { type ChartSeries } from "../../components/MetricChart.vue";
import NodeSignalRail from "../../components/NodeSignalRail.vue";
import StatusIndicator from "../../components/StatusIndicator.vue";
import TrendSparkline from "../../components/TrendSparkline.vue";
import {
  adapterLabels,
  formatBytes,
  formatPercent,
  formatRate,
  formatUptime,
  percent,
  relativeTime,
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
const kpiTrends = ref<NodeMetricSeries>({ range: "6h", step_seconds: 60, samples: [] });
const telemetryPanel = ref<InstanceType<typeof HostTelemetryPanel> | null>(null);
type DetailSection = "performance" | "runtime" | "configuration";
const activeSection = ref<DetailSection>("performance");
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

function kpiTrendValues(metric: "cpu" | "memory" | "disk") {
  const samples = kpiTrends.value.samples;
  if (metric === "cpu") return samples.map((s) => s.cpu_percent);
  if (metric === "memory") return samples.map((s) => percent(s.memory_used_bytes, s.memory_total_bytes));
  return samples.map((s) => percent(s.disk_used_bytes, s.disk_total_bytes));
}

async function loadMetrics(silent = false) {
  if (!silent) metricsLoading.value = true;
  metricsError.value = "";
  try {
    metrics.value = await api.getNodeMetrics(props.node.id, metricRange.value);
    if (!silent) {
      if (metricRange.value === "6h") {
        kpiTrends.value = metrics.value;
      } else {
        api.getNodeMetrics(props.node.id, "6h")
          .then((series) => { kpiTrends.value = series; })
          .catch((error: unknown) => {
            if (error instanceof APIError && error.status === 401) emit("session-expired");
          });
      }
    }
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

const detailSections: Array<{ key: DetailSection; elementID: string }> = [
  { key: "performance", elementID: "node-performance" },
  { key: "runtime", elementID: "node-runtime" },
  { key: "configuration", elementID: "node-configuration" },
];

let refreshTimer: number | undefined;
let detailScrollRoot: HTMLElement | null = null;
let detailScrollFrame: number | undefined;

function syncActiveSection() {
  detailScrollFrame = undefined;
  if (!detailScrollRoot) return;

  const activationLine = detailScrollRoot.getBoundingClientRect().top + 72;
  let nextSection: DetailSection = "performance";
  for (const section of detailSections) {
    const element = document.getElementById(section.elementID);
    if (element && element.getBoundingClientRect().top <= activationLine) nextSection = section.key;
  }
  activeSection.value = nextSection;
}

function scheduleSectionSync() {
  if (detailScrollFrame !== undefined) return;
  detailScrollFrame = window.requestAnimationFrame(syncActiveSection);
}

function markActiveSection(section: DetailSection) {
  activeSection.value = section;
}

onMounted(() => {
  void loadMetrics();
  detailScrollRoot = document.querySelector<HTMLElement>(".console-content > .n-layout-scroll-container");
  detailScrollRoot?.addEventListener("scroll", scheduleSectionSync, { passive: true });
  window.requestAnimationFrame(syncActiveSection);
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") void loadMetrics(true);
  }, 30_000);
});
onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
  detailScrollRoot?.removeEventListener("scroll", scheduleSectionSync);
  if (detailScrollFrame !== undefined) window.cancelAnimationFrame(detailScrollFrame);
});
watch(metricRange, () => void loadMetrics());
watch(() => props.node.id, () => void loadMetrics());
</script>

<template>
  <main class="workspace node-detail-page">
    <div class="node-detail-page__breadcrumb">
      <n-button quaternary circle aria-label="返回节点" @click="emit('back')">
        <template #icon><n-icon><arrow-left /></n-icon></template>
      </n-button>
      <span>节点</span><b>/</b><strong>{{ node.name }}</strong>
    </div>
    <header class="node-detail-header">
      <div class="node-detail-header__identity">
        <div class="node-detail-header__eyebrow">
          <status-indicator :status="node.status" />
          <span>{{ adapterLabels[node.adapter_type] }}</span>
        </div>
        <h1>{{ node.name }}</h1>
        <div class="node-detail-header__meta">
          <span><map-pin :size="14" aria-hidden="true" />{{ [node.provider, node.region].filter(Boolean).join(" · ") || "未设置位置" }}</span>
          <span><globe-2 :size="14" aria-hidden="true" />{{ endpointLabel(node) }}</span>
          <span>Agent {{ node.agent_version || "未注册" }}</span>
        </div>
      </div>
      <div class="node-detail-header__actions">
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button circle secondary aria-label="刷新监控" :loading="metricsLoading" @click="refreshMonitoring">
              <template #icon><n-icon><refresh-cw /></n-icon></template>
            </n-button>
          </template>
          刷新监控
        </n-tooltip>
        <n-button secondary @click="emit('edit', node)">
          <template #icon><n-icon><pencil /></n-icon></template>编辑
        </n-button>
        <n-button type="primary" @click="emit('enroll', node)">
          <template #icon><n-icon><key-round /></n-icon></template>注册 Agent
        </n-button>
      </div>
      <node-signal-rail :node="node" />
    </header>

    <nav class="node-detail-nav" aria-label="节点详情导航">
      <a
        href="#node-performance"
        :class="{ 'is-active': activeSection === 'performance' }"
        :aria-current="activeSection === 'performance' ? 'location' : undefined"
        @click="markActiveSection('performance')"
      >性能轨迹</a>
      <a
        href="#node-runtime"
        :class="{ 'is-active': activeSection === 'runtime' }"
        :aria-current="activeSection === 'runtime' ? 'location' : undefined"
        @click="markActiveSection('runtime')"
      >进程与服务</a>
      <a
        href="#node-configuration"
        :class="{ 'is-active': activeSection === 'configuration' }"
        :aria-current="activeSection === 'configuration' ? 'location' : undefined"
        @click="markActiveSection('configuration')"
      >配置与运维</a>
    </nav>

    <section id="node-performance" class="node-performance-layout" aria-label="节点性能">
      <div class="monitor-workspace node-performance-main" aria-label="历史监控">
        <header class="monitor-workspace__heading">
          <div>
            <h2>性能轨迹</h2>
            <span>{{ selectedRangeLabel }}<template v-if="metrics.step_seconds > 60"> · {{ Math.round(metrics.step_seconds / 60) }} 分钟聚合</template></span>
          </div>
          <div class="monitor-toolbar">
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
      </div>

      <aside class="node-resource-rail" aria-label="当前资源">
        <header class="node-resource-rail__heading">
          <div><h2>当前资源</h2><span>心跳 {{ relativeTime(node.last_seen_at) }}</span></div>
          <status-indicator :status="node.status" :show-label="false" />
        </header>
        <section class="host-kpis">
          <article class="host-kpi host-kpi--primary">
            <header><span>CPU 使用率</span><small>{{ node.cpu_cores || "-" }} 核</small></header>
            <strong>{{ formatPercent(node.cpu_percent) }}</strong>
            <div class="host-kpi__meter" aria-hidden="true"><i :style="{ width: meterWidth(node.cpu_percent) }" /></div>
            <trend-sparkline :values="kpiTrendValues('cpu')" :label="`${node.name} CPU 六小时趋势`" />
            <small>1 分钟负载 {{ node.load_1.toFixed(2) }}</small>
          </article>
          <article class="host-kpi host-kpi--primary">
            <header><span>内存占用</span><small>{{ formatBytes(node.memory_total_bytes) }}</small></header>
            <strong>{{ formatPercent(memoryPercent) }}</strong>
            <div class="host-kpi__meter" aria-hidden="true"><i :style="{ width: meterWidth(memoryPercent) }" /></div>
            <trend-sparkline :values="kpiTrendValues('memory')" :label="`${node.name} 内存六小时趋势`" color="var(--hf-flow)" />
            <small>{{ formatBytes(node.memory_used_bytes) }} · Swap {{ formatBytes(node.swap_used_bytes) }}</small>
          </article>
          <article class="host-kpi host-kpi--primary">
            <header><span>根分区</span><small>{{ formatBytes(node.disk_total_bytes) }}</small></header>
            <strong>{{ formatPercent(diskPercent) }}</strong>
            <div class="host-kpi__meter" aria-hidden="true"><i :style="{ width: meterWidth(diskPercent) }" /></div>
            <trend-sparkline :values="kpiTrendValues('disk')" :label="`${node.name} 磁盘六小时趋势`" color="var(--hf-pressure)" />
            <small>{{ formatBytes(node.disk_used_bytes) }} 已用</small>
          </article>
          <article class="host-kpi host-kpi--context">
            <header><span>当前网络</span><small>接收 / 发送</small></header>
            <strong><arrow-down :size="14" aria-hidden="true" />{{ formatRate(node.network_rx_bps) }}</strong>
            <small><arrow-up :size="13" aria-hidden="true" />{{ formatRate(node.network_tx_bps) }}</small>
          </article>
          <article class="host-kpi host-kpi--context">
            <header><span>运行时间</span><small>自上次启动</small></header>
            <strong>{{ formatUptime(node.uptime_seconds) }}</strong>
            <small>Agent {{ node.agent_version || "未注册" }}</small>
          </article>
        </section>
      </aside>
    </section>

    <host-telemetry-panel
      id="node-runtime"
      ref="telemetryPanel"
      :node-id="node.id"
      @session-expired="emit('session-expired')"
    />

    <section id="node-configuration" class="node-detail-lower">
      <div class="node-detail-column">
        <section class="detail-band">
          <h2>底层与累计指标</h2>
          <dl class="detail-list detail-list--two">
            <div><dt>CPU 核心</dt><dd>{{ node.cpu_cores || "-" }}</dd></div>
            <div><dt>负载（1 / 5 / 15 分钟）</dt><dd>{{ node.load_1.toFixed(2) }} / {{ node.load_5.toFixed(2) }} / {{ node.load_15.toFixed(2) }}</dd></div>
            <div><dt>磁盘读取 / 写入</dt><dd>{{ formatBytes(node.disk_read_bytes_per_second) }}/s / {{ formatBytes(node.disk_write_bytes_per_second) }}/s</dd></div>
            <div><dt>网卡接收 / 发送</dt><dd>{{ formatRate(node.network_rx_bps) }} / {{ formatRate(node.network_tx_bps) }}</dd></div>
            <div><dt>累计接收</dt><dd>{{ formatBytes(node.network_rx_bytes_total) }}</dd></div>
            <div><dt>累计发送</dt><dd>{{ formatBytes(node.network_tx_bytes_total) }}</dd></div>
          </dl>
        </section>
        <section class="detail-band">
          <h2>系统信息</h2>
          <dl class="detail-list detail-list--two">
            <div><dt>主机名</dt><dd>{{ node.hostname || "尚未上报" }}</dd></div>
            <div><dt>操作系统</dt><dd>{{ [node.os_name, node.os_version].filter(Boolean).join(" ") || "尚未上报" }}</dd></div>
            <div><dt>架构</dt><dd>{{ node.architecture || "-" }}</dd></div>
            <div><dt>内核</dt><dd>{{ node.kernel_version || "尚未上报" }}</dd></div>
            <div><dt>Agent</dt><dd>{{ node.agent_version || "尚未注册" }}</dd></div>
            <div><dt>核心</dt><dd>{{ [node.core_name, node.core_version].filter(Boolean).join(" ") || "尚未上报" }}</dd></div>
            <div><dt>适配方式</dt><dd>{{ adapterLabels[node.adapter_type] }}</dd></div>
            <div><dt>订阅端点</dt><dd>{{ endpointLabel(node) }}</dd></div>
            <template v-if="node.adapter_type === 'sing_box_vless_reality'">
              <div><dt>协议与传输</dt><dd>VLESS / TCP / Reality</dd></div>
              <div><dt>SNI / 伪装域名</dt><dd>{{ node.sni || "未配置" }}</dd></div>
              <div><dt>Reality 握手目标</dt><dd>{{ realityHandshakeLabel(node) }}</dd></div>
              <div><dt>Reality 身份</dt><dd>{{ node.reality?.public_key && node.reality?.short_id ? `已应用 · 第 ${node.reality.applied_key_generation} 代` : "等待节点生成" }}</dd></div>
              <div><dt>目标身份代际</dt><dd>{{ node.reality ? `第 ${node.reality.key_generation} 代${node.reality.key_generation !== node.reality.applied_key_generation ? " · 等待应用" : ""}` : "未配置" }}</dd></div>
              <div><dt>身份应用版本</dt><dd>{{ node.reality?.material_applied_version || "尚未应用" }}</dd></div>
            </template>
            <template v-else>
              <div><dt>证书固定</dt><dd>{{ node.tls_cert_fingerprint ? "已配置" : "未配置" }}</dd></div>
              <div><dt>公钥固定</dt><dd>{{ node.tls_public_key_sha256 ? "已配置" : "未配置" }}</dd></div>
            </template>
          </dl>
        </section>
        <section class="detail-band detail-band--user-traffic">
          <div class="detail-section__heading">
            <h2>用户流量与在线</h2>
          </div>
          <dl class="detail-list detail-list--two">
            <div><dt>在线用户 / 活跃连接</dt><dd>{{ node.online_users }} / {{ node.online_connections }}</dd></div>
            <div><dt>未识别用户</dt><dd>{{ node.online_unknown_users }}</dd></div>
            <div><dt>代理上传</dt><dd>{{ formatBytes(node.traffic_upload_bytes) }}</dd></div>
            <div><dt>代理下载</dt><dd>{{ formatBytes(node.traffic_download_bytes) }}</dd></div>
            <div><dt>最近流量上报</dt><dd>{{ relativeTime(node.traffic_last_report_at) }}</dd></div>
            <div><dt>最近在线采样</dt><dd>{{ relativeTime(node.online_sampled_at) }}</dd></div>
          </dl>
        </section>
        <section class="detail-band node-traffic-budget">
          <div class="detail-section__heading">
            <h2>月流量额度</h2>
            <span>{{ trafficCycleLabel(node) }}</span>
          </div>
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
            :status="node.traffic_used_bytes >= node.traffic_limit_bytes ? 'error' : 'success'"
          />
          <dl class="detail-list detail-list--two">
            <div><dt>周期代理上传</dt><dd>{{ formatBytes(node.traffic_cycle_upload_bytes) }}</dd></div>
            <div><dt>周期代理下载</dt><dd>{{ formatBytes(node.traffic_cycle_download_bytes) }}</dd></div>
            <div><dt>双向代理合计</dt><dd>{{ formatBytes(cycleProxyUsed) }}</dd></div>
            <div><dt>剩余额度</dt><dd>{{ node.traffic_limit_bytes > 0 ? formatBytes(trafficRemaining) : "不限额" }}</dd></div>
            <div><dt>运营商校准值</dt><dd>{{ node.traffic_calibration_bytes === null ? "尚未校准" : formatBytes(node.traffic_calibration_bytes) }}</dd></div>
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
        </section>
      </div>
      <div class="node-detail-column">
        <node-operations-panel
          v-if="node.agent_installation_id"
          :node="node"
          compact
          @view-all="emit('operations', node.id)"
          @changed="emit('changed')"
          @session-expired="emit('session-expired')"
        />
        <s-u-i-adapter-panel
          v-if="node.adapter_type === 's_ui'"
          :node="node"
          @changed="emit('changed')"
          @session-expired="emit('session-expired')"
        />
      </div>
    </section>
  </main>
</template>
