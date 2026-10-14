<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  AlertTriangle,
  UsersRound,
  Network,
  Plus,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  GripVertical,
  Pencil,
  Search,
  Server,
} from "@lucide/vue";
import { NButton, NCheckbox, NIcon, NInput, NSelect, NSpin } from "naive-ui";
import StatCard from "../../components/StatCard.vue";
import MetricBar from "../../components/MetricBar.vue";
import TrafficTrendChart from "../../components/TrafficTrendChart.vue";
import { adapterLabels, formatBytes, formatPercent, formatRate, percent, relativeTime, severity, statusLabels } from "../../lib/format";
import type {
  AlertRecord,
  NodeAssetRecord,
  NodeMetricSeries,
  NodeRecord,
  SubscriptionOperationRecord,
  SubscriptionOperationStatus,
  TrafficReport,
} from "../../types";

const props = defineProps<{
  nodes: NodeRecord[];
  alerts: AlertRecord[];
  loading: boolean;
  error: string;
  trends: Record<string, NodeMetricSeries>;
  assets: Record<string, NodeAssetRecord>;
  trafficReport: TrafficReport | null;
  subscriptions: SubscriptionOperationRecord[];
}>();
const emit = defineEmits<{
  select: [node: NodeRecord];
  refresh: [];
  create: [];
  alerts: [];
  subscriptions: [];
  nodes: [nodeIDs: string[]];
  "edit-asset": [node: NodeRecord];
}>();

const CABINET_ORDER_KEY = "hyfleet:node-cabinet-order";
const CABINET_SORT_KEY = "hyfleet:node-cabinet-sort";
const nodeSearch = ref("");
const nodeStatus = ref("all");
const nodeSort = ref("custom");
const selectedNodeIDs = ref<string[]>([]);
const cabinetOrder = ref<string[]>([]);
const draggingID = ref("");
const dropTargetID = ref("");
const statusOptions = [
  { label: "全部状态", value: "all" },
  { label: "在线", value: "online" },
  { label: "需关注", value: "attention" },
];
const sortOptions = [
  { label: "自定义顺序", value: "custom" },
  { label: "最后心跳", value: "heartbeat" },
  { label: "节点名称", value: "name" },
  { label: "CPU 使用率", value: "cpu" },
  { label: "流量使用率", value: "traffic" },
];

function readStoredList(key: string): string[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || "[]");
    return Array.isArray(parsed) ? parsed.filter((item) => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function persistCabinetOrder(ids: string[]) {
  cabinetOrder.value = ids;
  try {
    window.localStorage.setItem(CABINET_ORDER_KEY, JSON.stringify(ids));
  } catch {
    // Order still applies for this session.
  }
}

function syncCabinetOrder(ids: string[]) {
  const existing = cabinetOrder.value.filter((id) => ids.includes(id));
  const missing = ids.filter((id) => !existing.includes(id));
  const next = [...existing, ...missing];
  if (next.join("\0") !== cabinetOrder.value.join("\0")) persistCabinetOrder(next);
  return next;
}

try {
  const storedSort = window.localStorage.getItem(CABINET_SORT_KEY);
  if (sortOptions.some((item) => item.value === storedSort)) nodeSort.value = storedSort as typeof nodeSort.value;
  cabinetOrder.value = readStoredList(CABINET_ORDER_KEY);
} catch {
  nodeSort.value = "custom";
}

const online = computed(() => props.nodes.filter((node) => node.status === "online").length);
const onlinePercent = computed(() => props.nodes.length ? Math.round((online.value / props.nodes.length) * 100) : 0);
const currentRX = computed(() => props.nodes.reduce((total, node) => total + node.network_rx_bps, 0));
const currentTX = computed(() => props.nodes.reduce((total, node) => total + node.network_tx_bps, 0));
const activeConnections = computed(() => props.nodes.reduce((total, node) => total + node.online_connections, 0));
const onlineUsers = computed(() => props.nodes.reduce((total, node) => total + node.online_users, 0));
const trafficUsed = computed(() => props.nodes.reduce((total, node) => total + node.traffic_used_bytes, 0));
const trafficLimit = computed(() => props.nodes.reduce((total, node) => total + node.traffic_limit_bytes, 0));
const quotaPercent = computed(() => percent(trafficUsed.value, trafficLimit.value));
const dailyCapacityReference = computed(() => trafficLimit.value > 0 ? trafficLimit.value * 0.8 / 30 : 0);
const reportComparison = computed(() => {
  const current = props.trafficReport?.total_bytes ?? 0;
  const previous = props.trafficReport?.previous_total_bytes ?? 0;
  if (previous <= 0) return null;
  return ((current - previous) / previous) * 100;
});
const visibleSubscriptions = computed(() => props.subscriptions.slice(0, 5));
const filteredNodes = computed(() => {
  const query = nodeSearch.value.trim().toLocaleLowerCase();
  const filtered = props.nodes.filter((node) => {
    const matchesSearch = !query || [node.name, node.provider, node.region, node.public_host, adapterLabels[node.adapter_type]]
      .some((value) => value.toLocaleLowerCase().includes(query));
    const matchesStatus = nodeStatus.value === "all"
      || (nodeStatus.value === "attention" ? node.status !== "online" : node.status === nodeStatus.value);
    return matchesSearch && matchesStatus;
  });
  return [...filtered].sort((left, right) => {
    if (nodeSort.value === "name") return left.name.localeCompare(right.name, "zh-CN");
    if (nodeSort.value === "cpu") return right.cpu_percent - left.cpu_percent;
    if (nodeSort.value === "traffic") return percent(right.traffic_used_bytes, right.traffic_limit_bytes) - percent(left.traffic_used_bytes, left.traffic_limit_bytes);
    if (nodeSort.value === "heartbeat") return new Date(right.last_seen_at || 0).getTime() - new Date(left.last_seen_at || 0).getTime();
    const rank = new Map(cabinetOrder.value.map((id, index) => [id, index]));
    return (rank.get(left.id) ?? Number.MAX_SAFE_INTEGER) - (rank.get(right.id) ?? Number.MAX_SAFE_INTEGER);
  });
});

watch(nodeSort, (value) => {
  try {
    window.localStorage.setItem(CABINET_SORT_KEY, value);
  } catch {
    // Sort still applies for this session.
  }
});
watch(() => props.nodes.map((node) => node.id).join("\0"), (value) => {
  if (value) syncCabinetOrder(value.split("\0").filter(Boolean));
}, { immediate: true });

function endpointLabel(node: NodeRecord) {
  if (!node.public_host) return "未设置公网地址";
  const host = node.public_host.includes(":") && !node.public_host.startsWith("[") ? `[${node.public_host}]` : node.public_host;
  return `${host}:${node.public_port}`;
}
function memoryPercent(node: NodeRecord) { return percent(node.memory_used_bytes, node.memory_total_bytes); }
function diskPercent(node: NodeRecord) { return percent(node.disk_used_bytes, node.disk_total_bytes); }

function statusText(node: NodeRecord) {
  return statusLabels[node.status] ?? node.status;
}
function protocolShort(node: NodeRecord) {
  if (node.adapter_type === "native_hysteria2") return "HY2";
  if (node.adapter_type === "sing_box_vless_reality") return "Reality";
  if (node.adapter_type === "s_ui") return "S-UI";
  return "sing-box";
}
function assetDays(asset?: NodeAssetRecord) {
  if (!asset?.expires_at) return null;
  return Math.ceil((new Date(asset.expires_at).getTime() - Date.now()) / 86_400_000);
}
function assetExpiryLabel(asset?: NodeAssetRecord) {
  const days = assetDays(asset);
  if (days === null) return "未登记";
  if (days < 0) return `已到期 ${Math.abs(days)} 天`;
  if (days === 0) return "今天到期";
  return `剩余 ${days} 天`;
}
function assetExpiryClass(asset?: NodeAssetRecord) {
  const days = assetDays(asset);
  if (days === null) return "is-unset";
  if (days <= 7) return "is-critical";
  if (days <= 30) return "is-warning";
  return "is-neutral";
}
function shortDate(value: string | null | undefined) {
  if (!value) return "长期有效";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "时间未知";
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" }).format(date);
}
function dateRange(from?: string, to?: string) { return `${shortDate(from)} - ${shortDate(to)}`; }
function subscriptionExpiry(subscription: SubscriptionOperationRecord) {
  const values = [subscription.token_expires_at, subscription.user_expires_at].filter(Boolean) as string[];
  if (!values.length) return null;
  return values.sort((left, right) => new Date(left).getTime() - new Date(right).getTime())[0];
}
function expiryDetail(value: string | null) {
  if (!value) return "无固定到期日";
  const days = Math.ceil((new Date(value).getTime() - Date.now()) / 86_400_000);
  if (days < 0) return `已过期 ${Math.abs(days)} 天`;
  if (days === 0) return "今天到期";
  return `${days} 天后到期`;
}
const subscriptionLabels: Record<SubscriptionOperationStatus, string> = {
  active: "活跃", expiring: "临期", exhausted: "已耗尽", expired: "已过期", revoked: "已吊销", disabled: "已停用",
};
function toggleNode(nodeID: string, checked: boolean) {
  selectedNodeIDs.value = checked
    ? [...new Set([...selectedNodeIDs.value, nodeID])]
    : selectedNodeIDs.value.filter((id) => id !== nodeID);
}
function openCard(node: NodeRecord, event: MouseEvent) {
  const target = event.target as HTMLElement | null;
  if (target?.closest("input, button, a, .n-checkbox, .probe-card__grip")) return;
  if (draggingID.value) return;
  emit("select", node);
}

function onCardDragStart(node: NodeRecord, event: DragEvent) {
  if (nodeSort.value !== "custom") {
    event.preventDefault();
    return;
  }
  draggingID.value = node.id;
  event.dataTransfer?.setData("text/plain", node.id);
  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
}

function onCardDragOver(node: NodeRecord, event: DragEvent) {
  if (nodeSort.value !== "custom" || !draggingID.value || draggingID.value === node.id) return;
  event.preventDefault();
  dropTargetID.value = node.id;
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
}

function onCardDrop(node: NodeRecord, event: DragEvent) {
  event.preventDefault();
  const sourceID = draggingID.value || event.dataTransfer?.getData("text/plain") || "";
  dropTargetID.value = "";
  draggingID.value = "";
  if (!sourceID || sourceID === node.id) return;
  const ids = syncCabinetOrder(props.nodes.map((item) => item.id));
  const from = ids.indexOf(sourceID);
  const to = ids.indexOf(node.id);
  if (from < 0 || to < 0) return;
  ids.splice(from, 1);
  ids.splice(to, 0, sourceID);
  persistCabinetOrder(ids);
}

function onCardDragEnd() {
  draggingID.value = "";
  dropTargetID.value = "";
}
</script>

<template>
  <main class="workspace overview-workspace fleet-overview">
<section class="stat-grid" aria-label="舰队运行总账">
  <stat-card label="在线节点">
    <template #icon><server :size="18" /></template>
    {{ online }}<small>/ {{ nodes.length }}</small>
    <template #hint>{{ nodes.length ? `${onlinePercent}% 在线` : '等待首台节点接入' }}</template>
    <template #extra>
      <div class="stat-card__segments" aria-hidden="true">
        <i v-for="n in Math.min(nodes.length, 12)" :key="n" :class="n <= Math.round(online / nodes.length * Math.min(nodes.length, 12)) ? 'is-on' : 'is-off'" />
      </div>
    </template>
  </stat-card>

  <stat-card label="活跃会话" :muted="activeConnections === 0">
    <template #icon><users-round :size="18" /></template>
    {{ activeConnections }}
    <template #hint>{{ onlineUsers }} 位在线用户</template>
  </stat-card>

  <stat-card label="实时吞吐">
    <template #icon><network :size="18" /></template>
    <span class="flow flow--down flow--lg"><arrow-down :size="18" />{{ formatRate(currentRX) }}</span>
    <template #hint><span class="flow flow--up"><arrow-up :size="14" />{{ formatRate(currentTX) }}</span></template>
  </stat-card>

  <stat-card label="告警 / 流量" :tone="alerts.length ? 'critical' : 'signal'" :muted="alerts.length === 0" clickable @click="emit('alerts')">
    <template #icon><alert-triangle :size="18" /></template>
    {{ alerts.length }}<small>项告警</small>
    <template #hint>{{ trafficLimit ? `${formatPercent(quotaPercent)} · ${formatBytes(trafficUsed)} / ${formatBytes(trafficLimit)}` : '未设置总配额' }}</template>
    <template #extra>
      <i v-if="trafficLimit" class="stat-card__bar" :class="`is-${severity(quotaPercent)}`"><b :style="{ width: `${Math.min(100, quotaPercent)}%` }" /></i>
    </template>
  </stat-card>
</section>

    <div v-if="error" class="overview-error"><span>{{ error }}</span><n-button text type="error" @click="emit('refresh')">重新加载</n-button></div>
    <div v-if="loading" class="overview-loading"><n-spin :size="28" /></div>

    <template v-else>
      <section class="probe-rack" aria-label="节点机柜">
        <header class="probe-rack__toolbar">
          <div>
            <h2>节点机柜</h2>
            <small>{{ online }} 台在线 · {{ nodes.length - online }} 台需关注</small>
          </div>
          <div class="probe-rack__controls">
            <n-input v-model:value="nodeSearch" clearable size="small" placeholder="搜索节点">
              <template #prefix><n-icon><search /></n-icon></template>
            </n-input>
            <n-select v-model:value="nodeStatus" size="small" :options="statusOptions" />
            <n-select v-model:value="nodeSort" size="small" :options="sortOptions" />
            <n-button v-if="selectedNodeIDs.length" size="small" secondary @click="emit('nodes', selectedNodeIDs)">
              批量操作 ({{ selectedNodeIDs.length }})
            </n-button>
            <n-button type="primary" size="small" @click="emit('create')"><template #icon><n-icon><plus /></n-icon></template>添加节点</n-button>
          </div>
        </header>

        <div v-if="filteredNodes.length" class="probe-grid" :class="{ 'has-selection': selectedNodeIDs.length > 0 }">
          <article
            v-for="node in filteredNodes"
            :key="node.id"
            class="probe-card"
            :class="[
              `is-${node.status}`,
              { 'is-dragging': draggingID === node.id, 'is-drop-target': dropTargetID === node.id },
            ]"
            :draggable="nodeSort === 'custom'"
            @click="openCard(node, $event)"
            @dragstart="onCardDragStart(node, $event)"
            @dragover="onCardDragOver(node, $event)"
            @drop="onCardDrop(node, $event)"
            @dragend="onCardDragEnd"
          >
            <header class="probe-card__head">
              <button
                v-if="nodeSort === 'custom'"
                type="button"
                class="probe-card__grip"
                draggable="true"
                :aria-label="`拖动 ${node.name} 调整顺序`"
                @click.stop
                @dragstart.stop="onCardDragStart(node, $event)"
              >
                <grip-vertical :size="14" />
              </button>
              <n-checkbox
                :checked="selectedNodeIDs.includes(node.id)"
                :aria-label="`选择 ${node.name}`"
                @click.stop
                @update:checked="toggleNode(node.id, $event)"
              />
              <div class="probe-card__identity">
                <div class="probe-card__title-row">
                  <span class="probe-card__dot" />
                  <h3><button type="button" class="probe-card__open" :aria-label="`查看 ${node.name}`" @click.stop="emit('select', node)">{{ node.name }}</button></h3>
                  <b v-if="node.status !== 'online'" class="probe-card__status">{{ statusText(node) }}</b><span v-else class="sr-only">在线</span>
                </div>
                <small>{{ node.provider || "未登记服务商" }} · {{ node.region || "未登记地区" }}</small>
              </div>
              <div class="probe-card__badge">
                <strong>{{ protocolShort(node) }}</strong>
                <small>{{ node.core_version || node.core_name || "未上报" }}</small>
              </div>
            </header>

  <div class="rack-node__meters">
    <metric-bar label="CPU" :value="node.cpu_percent" :display="formatPercent(node.cpu_percent)" />
    <metric-bar label="内存" :value="memoryPercent(node)" :display="formatPercent(memoryPercent(node))" :detail="`${formatBytes(node.memory_used_bytes)} / ${formatBytes(node.memory_total_bytes)}`" />
    <metric-bar label="磁盘" :value="diskPercent(node)" :display="formatPercent(diskPercent(node))" :detail="`${formatBytes(node.disk_used_bytes)} / ${formatBytes(node.disk_total_bytes)}`" />
  </div>

  <dl class="rack-node__facts">
    <div><dt>网络</dt><dd><span class="flow flow--down"><arrow-down :size="12" />{{ formatRate(node.network_rx_bps) }}</span><span class="flow flow--up"><arrow-up :size="12" />{{ formatRate(node.network_tx_bps) }}</span></dd></div>
    <div><dt>会话</dt><dd>{{ node.online_connections }}<small> · {{ node.online_users }} 用户</small></dd></div>
    <div><dt>到期</dt><dd><span class="expiry-pill" :class="assetExpiryClass(assets[node.id])">{{ assetExpiryLabel(assets[node.id]) }}</span></dd></div>
  </dl>

  <div class="rack-node__quota">
    <div><span>本月流量（双向）</span><strong>{{ formatBytes(node.traffic_used_bytes) }}<small> / {{ node.traffic_limit_bytes ? formatBytes(node.traffic_limit_bytes) : '不限额' }}</small></strong></div>
    <i v-if="node.traffic_limit_bytes" class="rack-node__quota-bar" :class="`is-${severity(percent(node.traffic_used_bytes, node.traffic_limit_bytes))}`"><b :style="{ width: `${percent(node.traffic_used_bytes, node.traffic_limit_bytes)}%` }" /></i>
  </div>


            <footer class="probe-card__foot">
              <span class="mono">{{ endpointLabel(node) }}</span>
              <span>{{ relativeTime(node.last_seen_at) }}</span>
              <button type="button" @click.stop="emit('edit-asset', node)"><pencil :size="12" />资产</button>
            </footer>
          </article>
        </div>
        <div v-else class="overview-empty">
          <server :size="28" />
          <strong>{{ nodes.length ? "没有匹配的节点" : "尚未添加节点" }}</strong>
          <n-button v-if="!nodes.length" type="primary" size="small" @click="emit('create')">添加节点</n-button>
        </div>
      </section>

      <div class="fleet-aux">
        <section class="overview-traffic-panel" aria-label="30 日双向流量趋势">
          <header class="overview-panel-heading overview-traffic-panel__heading">
            <div>
              <h2>30 日双向流量趋势</h2>
              <small>{{ trafficReport ? dateRange(trafficReport.from, trafficReport.to) : "等待流量数据" }}</small>
            </div>
            <div class="traffic-summary">
              <span v-if="reportComparison !== null" :class="reportComparison <= 0 ? 'is-positive' : 'is-negative'">对比上月 {{ reportComparison > 0 ? "+" : "" }}{{ reportComparison.toFixed(1) }}%</span>
              <span>本期 <strong>{{ formatBytes(trafficReport?.total_bytes ?? 0) }}</strong></span>
            </div>
          </header>
          <div class="traffic-legend" aria-label="趋势图图例">
            <span><i class="is-download" />下行（当前）</span>
            <span><i class="is-upload" />上行（当前）</span>
            <span><i class="is-download is-previous" />下行（上周期）</span>
            <span><i class="is-upload is-previous" />上行（上周期）</span>
            <span v-if="dailyCapacityReference"><i class="is-capacity" />月度配额 80%（日均）</span>
          </div>
          <div class="overview-traffic-panel__chart">
            <traffic-trend-chart :points="trafficReport?.daily ?? []" :previous-points="trafficReport?.previous_daily ?? []" :capacity-bytes="dailyCapacityReference" />
          </div>
        </section>

        <section class="subscription-health" aria-label="订阅健康概览">
          <header class="overview-panel-heading">
            <div>
              <h2>订阅健康</h2>
              <small>{{ subscriptions.length }} 个凭据</small>
            </div>
            <n-button size="small" secondary @click="emit('subscriptions')">
              查看全部
              <template #icon><n-icon><chevron-right /></n-icon></template>
            </n-button>
          </header>
          <div class="subscription-health__scroller">
            <div class="subscription-health__table">
              <div class="subscription-health__head">
                <span>订阅 / 用户</span><span>状态</span><span>节点</span><span>到期</span><span>流量</span>
              </div>
              <button
                v-for="subscription in visibleSubscriptions"
                :key="subscription.token_id"
                class="subscription-health__row"
                type="button"
                @click="emit('subscriptions')"
              >
                <span class="subscription-health__identity">
                  <strong>{{ subscription.name || subscription.username }}</strong>
                  <small>{{ subscription.display_name || subscription.username }}</small>
                </span>
                <span><i class="subscription-status" :class="`is-${subscription.status}`">{{ subscriptionLabels[subscription.status] }}</i></span>
                <span class="subscription-health__nodes"><strong>{{ subscription.online_nodes }} / {{ subscription.assignment_count }}</strong></span>
                <span class="subscription-health__expiry" :class="{ 'is-critical': ['expired', 'expiring'].includes(subscription.status) }">
                  <strong>{{ shortDate(subscriptionExpiry(subscription)) }}</strong>
                </span>
                <span class="subscription-health__traffic">
                  <strong>{{ formatBytes(subscription.traffic_used_bytes) }} / {{ subscription.traffic_limit_bytes ? formatBytes(subscription.traffic_limit_bytes) : "不限" }}</strong>
                  <i><b :style="{ width: `${percent(subscription.traffic_used_bytes, subscription.traffic_limit_bytes)}%` }" /></i>
                </span>
              </button>
              <div v-if="!visibleSubscriptions.length" class="subscription-health__empty">尚无订阅凭据</div>
            </div>
          </div>
        </section>
      </div>
    </template>
  </main>
</template>

<style scoped>
.fleet-overview { display: grid; align-content: start; gap: 24px; }
.probe-rack { min-width: 0; }
.probe-rack__toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 0 0 16px; }
.probe-rack__toolbar h2 { margin: 0; font-size: 20px; font-weight: 650; }
.probe-rack__toolbar small { display: block; margin-top: 4px; color: var(--hf-muted); font-size: 13px; }
.probe-rack__controls { display: flex; min-width: 0; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.probe-rack__controls .n-input { width: 200px; }
.probe-rack__controls .n-select { width: 136px; }
.probe-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 380px), 1fr)); gap: 16px; }
.probe-card { display: flex; min-width: 0; flex-direction: column; border: 1px solid var(--hf-line); border-radius: var(--hf-radius-lg); background: var(--hf-surface); box-shadow: var(--hf-shadow-1); cursor: pointer; transition: border-color 160ms, box-shadow 160ms; }
.probe-card:hover { border-color: var(--hf-line-strong); box-shadow: var(--hf-shadow-2); }
.probe-card.is-online { --probe-state: var(--hf-signal); }
.probe-card.is-pending, .probe-card.is-stale { --probe-state: var(--hf-pressure); box-shadow: var(--hf-shadow-1), inset 0 3px var(--hf-pressure); }
.probe-card.is-offline, .probe-card.is-degraded, .probe-card.is-disabled { --probe-state: var(--hf-critical); box-shadow: var(--hf-shadow-1), inset 0 3px var(--hf-critical); }
.probe-card.is-dragging { opacity: .55; }
.probe-card.is-drop-target { outline: 2px solid var(--hf-accent); }
.probe-card__head { display: grid; grid-template-columns: 18px 18px minmax(0, 1fr) auto; align-items: start; gap: 8px; padding: 18px 18px 16px; }
.probe-card__head:not(:has(.probe-card__grip)) { grid-template-columns: 18px minmax(0, 1fr) auto; }
.probe-card__grip { display: grid; width: 18px; height: 24px; padding: 0; place-items: center; border: 0; border-radius: 6px; background: transparent; color: var(--hf-muted); cursor: grab; }
.probe-card__grip:active { cursor: grabbing; }
.probe-card__title-row { display: flex; min-width: 0; align-items: center; flex-wrap: wrap; gap: 6px; }
.probe-card__dot { width: 8px; height: 8px; flex: 0 0 8px; border-radius: 50%; background: var(--probe-state); }
.probe-card.is-online .probe-card__dot { animation: hf-pulse 2.4s ease-out infinite; }
.probe-card__identity { min-width: 0; }
.probe-card__identity h3 { min-width: 0; flex: 1; margin: 0; font-size: 16px; line-height: 1.4; }
.probe-card__open { display: block; width: 100%; overflow: hidden; padding: 0; border: 0; background: transparent; color: var(--hf-ink); font: inherit; font-weight: 600; text-align: left; white-space: nowrap; text-overflow: ellipsis; cursor: pointer; }
.probe-card__identity > small { display: block; margin-top: 5px; overflow: hidden; color: var(--hf-muted); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.probe-card__status { padding: 2px 7px; border-radius: 999px; background: var(--hf-critical-soft); color: var(--hf-critical-deep); font-size: 12px; font-weight: 600; }
.is-pending .probe-card__status, .is-stale .probe-card__status { background: var(--hf-pressure-soft); color: var(--hf-pressure-deep); }
.probe-card__badge { text-align: right; }
.probe-card__badge strong { display: inline-block; padding: 3px 9px; border-radius: 999px; background: var(--hf-accent-soft); color: var(--hf-accent); font-size: 12px; font-weight: 600; }
.probe-card__badge small { display: block; max-width: 100px; margin-top: 5px; overflow: hidden; color: var(--hf-muted); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.probe-card__foot { display: flex; align-items: center; gap: 10px; margin-top: auto; padding: 10px 18px; border-top: 1px solid var(--hf-line); color: var(--hf-muted); font-size: 12px; }
.probe-card__foot span { overflow: hidden; min-width: 0; white-space: nowrap; text-overflow: ellipsis; }
.probe-card__foot span:first-child { flex: 1; }
.probe-card__foot button { display: inline-flex; flex-shrink: 0; align-items: center; gap: 4px; padding: 4px 6px; border: 0; border-radius: 8px; background: transparent; color: var(--hf-muted); cursor: pointer; font: inherit; }
.probe-card__foot button:hover { color: var(--hf-accent); background: var(--hf-hover); }
.fleet-aux { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr); align-items: start; gap: 20px; }
.fleet-aux > section { min-width: 0; }
.fleet-aux .subscription-health__table { min-width: 600px; }
@media (max-width: 1280px) { .fleet-aux { grid-template-columns: 1fr; } }
@media (max-width: 860px) {
  .probe-rack__toolbar { align-items: flex-start; flex-direction: column; }
  .probe-rack__controls { justify-content: flex-start; width: 100%; }
  .probe-rack__controls .n-input { flex: 1 0 100%; width: 100%; }
  .probe-rack__controls .n-select { flex: 1; }
}
@media (prefers-reduced-motion: reduce) { .probe-card.is-online .probe-card__dot { animation: none; } }
</style>
