<script setup lang="ts">
import { computed, ref } from "vue";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  Cpu,
  HardDrive,
  MemoryStick,
  Network,
  Pencil,
  Search,
  Server,
  UsersRound,
} from "@lucide/vue";
import { NButton, NCheckbox, NIcon, NInput, NSelect, NSpin } from "naive-ui";
import TrafficTrendChart from "../../components/TrafficTrendChart.vue";
import TrendSparkline from "../../components/TrendSparkline.vue";
import { adapterLabels, formatBytes, formatPercent, formatRate, percent, relativeTime } from "../../lib/format";
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

const nodeSearch = ref("");
const nodeStatus = ref("all");
const nodeSort = ref("heartbeat");
const selectedNodeIDs = ref<string[]>([]);
const statusOptions = [
  { label: "全部状态", value: "all" },
  { label: "在线", value: "online" },
  { label: "需关注", value: "attention" },
];
const sortOptions = [
  { label: "最后心跳", value: "heartbeat" },
  { label: "节点名称", value: "name" },
  { label: "CPU 使用率", value: "cpu" },
  { label: "流量使用率", value: "traffic" },
];

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
    return new Date(right.last_seen_at || 0).getTime() - new Date(left.last_seen_at || 0).getTime();
  });
});

function endpointLabel(node: NodeRecord) {
  if (!node.public_host) return "未设置公网地址";
  const host = node.public_host.includes(":") && !node.public_host.startsWith("[") ? `[${node.public_host}]` : node.public_host;
  return `${host}:${node.public_port}`;
}
function memoryPercent(node: NodeRecord) { return percent(node.memory_used_bytes, node.memory_total_bytes); }
function diskPercent(node: NodeRecord) { return percent(node.disk_used_bytes, node.disk_total_bytes); }
function trendValues(node: NodeRecord, metric: "cpu" | "memory" | "network") {
  const samples = props.trends[node.id]?.samples ?? [];
  return samples.map((sample) => metric === "cpu"
    ? sample.cpu_percent
    : metric === "memory"
      ? percent(sample.memory_used_bytes, sample.memory_total_bytes)
      : sample.network_rx_bps + sample.network_tx_bps);
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
  return "is-healthy";
}
function assetProgress(asset?: NodeAssetRecord) {
  if (!asset?.purchased_at || !asset.expires_at) return 0;
  const start = new Date(asset.purchased_at).getTime();
  const end = new Date(asset.expires_at).getTime();
  if (end <= start) return 100;
  return Math.min(100, Math.max(0, ((Date.now() - start) / (end - start)) * 100));
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
</script>

<template>
  <main class="workspace overview-workspace">
    <section class="fleet-register" aria-label="舰队运行总账">
      <div class="fleet-register__primary">
        <span><server :size="15" />在线节点</span>
        <div><strong>{{ online }}</strong><b>/ {{ nodes.length }}</b></div>
        <p>{{ nodes.length ? `${onlinePercent}% 在线` : "等待首台节点接入" }}</p>
      </div>
      <div class="fleet-register__cell">
        <span><users-round :size="15" />活跃会话</span>
        <strong>{{ activeConnections }}</strong>
        <small>{{ onlineUsers }} 位在线用户</small>
      </div>
      <div class="fleet-register__cell fleet-register__cell--network">
        <span><network :size="15" />实时吞吐</span>
        <strong><arrow-down :size="14" />{{ formatRate(currentRX) }}</strong>
        <small><arrow-up :size="13" />{{ formatRate(currentTX) }}</small>
      </div>
      <button class="fleet-register__cell fleet-register__cell--uptime" type="button" @click="emit('alerts')">
        <span><alert-triangle :size="15" />告警 / 流量</span>
        <strong :class="{ 'is-danger': alerts.length }">{{ alerts.length }} 项<span v-if="trafficLimit">/ {{ formatBytes(trafficLimit) }}</span></strong>
        <small>{{ trafficLimit ? `${formatPercent(quotaPercent)} · ${formatBytes(trafficUsed)} 已用` : "未设置总配额" }}</small>
      </button>
    </section>

    <div v-if="error" class="overview-error"><span>{{ error }}</span><n-button text type="error" @click="emit('refresh')">重新加载</n-button></div>
    <div v-if="loading" class="overview-loading"><n-spin :size="28" /></div>

    <template v-else>
      <section class="overview-traffic-panel" aria-label="30 日双向流量趋势">
        <header class="overview-panel-heading overview-traffic-panel__heading">
          <div><h2>30 日双向流量趋势</h2><small>{{ trafficReport ? dateRange(trafficReport.from, trafficReport.to) : "等待流量数据" }}</small></div>
          <div class="traffic-summary">
            <span v-if="reportComparison !== null" :class="reportComparison <= 0 ? 'is-positive' : 'is-negative'">对比上月 {{ reportComparison > 0 ? '+' : '' }}{{ reportComparison.toFixed(1) }}%</span>
            <span>本期双向 <strong>{{ formatBytes(trafficReport?.total_bytes ?? 0) }}</strong></span>
            <span v-if="trafficLimit">容量参考 80% <strong>{{ formatBytes(trafficLimit * 0.8) }} / {{ formatBytes(trafficLimit) }}</strong></span>
          </div>
        </header>
        <div class="traffic-legend" aria-label="趋势图图例">
          <span><i class="is-download" />下行（当前）</span><span><i class="is-upload" />上行（当前）</span>
          <span><i class="is-download is-previous" />下行（上周期）</span><span><i class="is-upload is-previous" />上行（上周期）</span>
          <span v-if="dailyCapacityReference"><i class="is-capacity" />月度配额 80%（日均）</span>
        </div>
        <div class="overview-traffic-panel__chart">
          <traffic-trend-chart :points="trafficReport?.daily ?? []" :previous-points="trafficReport?.previous_daily ?? []" :capacity-bytes="dailyCapacityReference" />
        </div>
      </section>

      <section class="subscription-health" aria-label="订阅健康概览">
        <header class="overview-panel-heading">
          <div><h2>订阅健康概览</h2><small>{{ subscriptions.length }} 个订阅凭据 · 双向流量统计</small></div>
          <n-button size="small" secondary @click="emit('subscriptions')">查看全部<template #icon><n-icon><chevron-right /></n-icon></template></n-button>
        </header>
        <div class="subscription-health__scroller">
          <div class="subscription-health__table">
            <div class="subscription-health__head"><span>订阅 / 用户</span><span>状态</span><span>节点覆盖</span><span>到期</span><span>本周期双向流量</span><span>操作</span></div>
            <button v-for="subscription in visibleSubscriptions" :key="subscription.token_id" class="subscription-health__row" type="button" @click="emit('subscriptions')">
              <span class="subscription-health__identity"><strong>{{ subscription.name || subscription.username }}</strong><small>{{ subscription.display_name || subscription.username }} · {{ subscription.token_prefix }}...</small></span>
              <span><i class="subscription-status" :class="`is-${subscription.status}`">{{ subscriptionLabels[subscription.status] }}</i></span>
              <span class="subscription-health__nodes"><strong>{{ subscription.online_nodes }} / {{ subscription.assignment_count }}</strong><small>在线 / 已分配</small></span>
              <span class="subscription-health__expiry" :class="{ 'is-critical': ['expired', 'expiring'].includes(subscription.status) }"><strong>{{ shortDate(subscriptionExpiry(subscription)) }}</strong><small>{{ expiryDetail(subscriptionExpiry(subscription)) }}</small></span>
              <span class="subscription-health__traffic"><strong>{{ formatBytes(subscription.traffic_used_bytes) }} / {{ subscription.traffic_limit_bytes ? formatBytes(subscription.traffic_limit_bytes) : '不限量' }}</strong><i><b :style="{ width: `${percent(subscription.traffic_used_bytes, subscription.traffic_limit_bytes)}%` }" /></i><small>{{ subscription.traffic_limit_bytes ? `${formatPercent(percent(subscription.traffic_used_bytes, subscription.traffic_limit_bytes))} 已用` : '未设置配额' }}</small></span>
              <span class="subscription-health__action">管理<chevron-right :size="14" /></span>
            </button>
            <div v-if="!visibleSubscriptions.length" class="subscription-health__empty">尚无订阅凭据</div>
          </div>
        </div>
      </section>

      <section class="node-rack" aria-label="节点机柜">
        <header class="node-rack__toolbar">
          <div><h2>节点机柜 <b>({{ nodes.length }})</b></h2><small>{{ online }} ONLINE · {{ nodes.length - online }} ATTENTION</small></div>
          <div class="node-rack__controls">
            <n-input v-model:value="nodeSearch" clearable size="small" placeholder="搜索节点"><template #prefix><n-icon><search /></n-icon></template></n-input>
            <n-select v-model:value="nodeStatus" size="small" :options="statusOptions" />
            <n-select v-model:value="nodeSort" size="small" :options="sortOptions" />
            <n-button v-if="selectedNodeIDs.length" size="small" secondary @click="emit('nodes', selectedNodeIDs)">批量操作 ({{ selectedNodeIDs.length }})</n-button>
          </div>
        </header>

        <div v-if="filteredNodes.length" class="node-rack__grid">
          <article v-for="node in filteredNodes" :key="node.id" class="rack-node" :class="`is-${node.status}`">
            <header class="rack-node__header">
              <n-checkbox :checked="selectedNodeIDs.includes(node.id)" :aria-label="`选择 ${node.name}`" @update:checked="toggleNode(node.id, $event)" />
              <div class="rack-node__identity"><span class="rack-node__status"><i />{{ node.status === 'online' ? '在线' : node.status === 'pending' ? '待连接' : '需关注' }}</span><h3>{{ node.name }}</h3><small>{{ node.provider || '未登记服务商' }} · {{ node.region || endpointLabel(node) }}</small></div>
              <div class="rack-node__protocol"><span>Core / 协议</span><strong>{{ node.core_name || 'Core 未上报' }}</strong><small>{{ adapterLabels[node.adapter_type] }} · {{ node.core_version || '版本未知' }}</small></div>
              <button class="rack-node__open" type="button" :aria-label="`查看 ${node.name}`" @click="emit('select', node)"><chevron-right :size="18" /></button>
            </header>

            <div class="rack-node__lifetime" :class="assetExpiryClass(assets[node.id])">
              <span>资产有效期</span><i><b :style="{ width: `${assetProgress(assets[node.id])}%` }" /></i><strong>{{ assetExpiryLabel(assets[node.id]) }}</strong>
            </div>

            <div class="rack-node__metrics">
              <div><span><cpu :size="13" />CPU</span><strong>{{ formatPercent(node.cpu_percent) }}</strong><trend-sparkline :values="trendValues(node, 'cpu')" :label="`${node.name} CPU 六小时趋势`" /></div>
              <div><span><memory-stick :size="13" />内存</span><strong>{{ formatPercent(memoryPercent(node)) }}</strong><small>{{ formatBytes(node.memory_used_bytes) }} / {{ formatBytes(node.memory_total_bytes) }}</small><trend-sparkline :values="trendValues(node, 'memory')" :label="`${node.name} 内存六小时趋势`" color="var(--hf-flow)" /></div>
              <div><span><hard-drive :size="13" />磁盘</span><strong>{{ formatPercent(diskPercent(node)) }}</strong><small>{{ formatBytes(node.disk_used_bytes) }} / {{ formatBytes(node.disk_total_bytes) }}</small></div>
              <div><span><network :size="13" />网络（实时）</span><strong>↓ {{ formatRate(node.network_rx_bps) }}</strong><small>↑ {{ formatRate(node.network_tx_bps) }}</small><trend-sparkline :values="trendValues(node, 'network')" :label="`${node.name} 网络六小时趋势`" color="var(--hf-pressure)" /></div>
            </div>

            <dl class="rack-node__ledger">
              <div><dt>月度流量（双向）</dt><dd>{{ formatBytes(node.traffic_used_bytes) }} / {{ node.traffic_limit_bytes ? formatBytes(node.traffic_limit_bytes) : '不限量' }}</dd><i><b :style="{ width: `${percent(node.traffic_used_bytes, node.traffic_limit_bytes)}%` }" /></i></div>
              <div><dt>配额重置</dt><dd>每月 {{ node.traffic_reset_day }} 日</dd><small>{{ formatPercent(percent(node.traffic_used_bytes, node.traffic_limit_bytes)) }} 已用</small></div>
              <div><dt>VPS 到期</dt><dd :class="assetExpiryClass(assets[node.id])">{{ assetExpiryLabel(assets[node.id]) }}</dd><small>{{ shortDate(assets[node.id]?.expires_at) }}</small></div>
              <div><dt>续费周期</dt><dd>{{ assets[node.id]?.renewal_cycle_months ? `每 ${assets[node.id].renewal_cycle_months} 个月` : '未设置' }}</dd><small>{{ assets[node.id]?.auto_renew ? '自动续费' : '手动续费' }}</small></div>
              <button type="button" @click="emit('edit-asset', node)"><pencil :size="13" />编辑资产</button>
            </dl>

            <footer><span>IP：{{ endpointLabel(node) }}</span><span>系统：{{ [node.os_name, node.os_version, node.architecture].filter(Boolean).join(' ') || '未上报' }}</span><span>最后心跳：{{ relativeTime(node.last_seen_at) }}</span><span>创建：{{ shortDate(node.created_at) }}</span></footer>
          </article>
        </div>
        <div v-else class="overview-empty"><server :size="28" /><strong>{{ nodes.length ? '没有匹配的节点' : '尚未添加节点' }}</strong><n-button v-if="!nodes.length" type="primary" size="small" @click="emit('create')">添加节点</n-button></div>
      </section>
    </template>
  </main>
</template>
