<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ArrowDown, ArrowUp, BarChart3, RefreshCw, Server, UsersRound } from "@lucide/vue";
import { NButton, NIcon, NSpin, NTooltip, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import TrafficTrendChart from "../../components/TrafficTrendChart.vue";
import { formatBytes } from "../../lib/format";
import type { TrafficReport } from "../../types";

const emit = defineEmits<{ "session-expired": [] }>();
const message = useMessage();
const report = ref<TrafficReport | null>(null);
const range = ref<"7d" | "30d">("30d");
const loading = ref(true);
const refreshing = ref(false);

const change = computed(() => {
  if (!report.value) return 0;
  if (!report.value.previous_total_bytes) return report.value.total_bytes ? 100 : 0;
  return ((report.value.total_bytes - report.value.previous_total_bytes) / report.value.previous_total_bytes) * 100;
});
const peak = computed(() => report.value?.daily.reduce((best, point) => {
  const total = point.upload_bytes + point.download_bytes;
  return total > best.total ? { date: point.bucket_at, total } : best;
}, { date: "", total: 0 }) ?? { date: "", total: 0 });

async function load() {
  if (refreshing.value) return;
  refreshing.value = true;
  loading.value = report.value === null;
  try {
    report.value = await api.trafficReport(range.value);
  } catch (error) {
    if (error instanceof APIError && error.status === 401) emit("session-expired");
    else message.error(error instanceof APIError ? error.message : "流量报表加载失败。");
  } finally {
    refreshing.value = false;
    loading.value = false;
  }
}

function setRange(value: "7d" | "30d") {
  range.value = value;
  void load();
}

onMounted(() => load());
</script>

<template>
  <main class="workspace operations-workspace report-workspace">
    <div class="page-heading page-heading--console">
      <div><span class="page-eyebrow">TRAFFIC EVIDENCE</span><h2>流量报表</h2><p>按控制面已接收批次统计，上下行合计为双向用量</p></div>
      <div class="page-heading__actions"><div class="ops-segments"><button :class="{ 'is-active': range === '7d' }" type="button" @click="setRange('7d')">7 天</button><button :class="{ 'is-active': range === '30d' }" type="button" @click="setRange('30d')">30 天</button></div><n-tooltip trigger="hover"><template #trigger><n-button circle secondary :loading="refreshing" aria-label="刷新报表" @click="load"><template #icon><n-icon><refresh-cw /></n-icon></template></n-button></template>刷新</n-tooltip></div>
    </div>

    <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
    <template v-else-if="report">
      <section class="ops-register report-register" aria-label="流量摘要">
        <div><span><bar-chart3 :size="15" />双向总量</span><strong>{{ formatBytes(report.total_bytes) }}</strong><small :class="{ 'is-warning': change > 20 }">较上期 {{ change >= 0 ? '+' : '' }}{{ change.toFixed(1) }}%</small></div>
        <div><span><arrow-down :size="15" />下行</span><strong>{{ formatBytes(report.download_bytes) }}</strong><small>{{ report.total_bytes ? ((report.download_bytes / report.total_bytes) * 100).toFixed(1) : 0 }}% 占比</small></div>
        <div><span><arrow-up :size="15" />上行</span><strong>{{ formatBytes(report.upload_bytes) }}</strong><small>{{ report.total_bytes ? ((report.upload_bytes / report.total_bytes) * 100).toFixed(1) : 0 }}% 占比</small></div>
        <div><span><refresh-cw :size="15" />单日峰值</span><strong>{{ formatBytes(peak.total) }}</strong><small>{{ peak.date ? new Date(peak.date).toLocaleDateString('zh-CN') : '暂无采样' }}</small></div>
      </section>

      <section class="report-chart-surface">
        <header><div><span>DAILY TRANSFER</span><h3>{{ report.range === '30d' ? '30 天' : '7 天' }}双向流量轨迹</h3></div><div class="report-legend"><span><i class="is-download" />下行</span><span><i class="is-upload" />上行</span></div></header>
        <traffic-trend-chart :points="report.daily" label="每日上下行流量趋势" />
      </section>

      <div class="report-rank-grid">
        <section class="rank-surface">
          <header><users-round :size="16" /><div><span>TOP USERS</span><h3>用户用量排行</h3></div></header>
          <ol v-if="report.top_users.length"><li v-for="(item, index) in report.top_users" :key="item.id"><b>{{ String(index + 1).padStart(2, '0') }}</b><span><strong>{{ item.name }}</strong><small>↓ {{ formatBytes(item.download_bytes) }} · ↑ {{ formatBytes(item.upload_bytes) }}</small></span><em>{{ formatBytes(item.total_bytes) }}</em></li></ol>
          <div v-else class="rank-empty">暂无可归属用户流量</div>
        </section>
        <section class="rank-surface">
          <header><server :size="16" /><div><span>TOP NODES</span><h3>节点用量排行</h3></div></header>
          <ol v-if="report.top_nodes.length"><li v-for="(item, index) in report.top_nodes" :key="item.id"><b>{{ String(index + 1).padStart(2, '0') }}</b><span><strong>{{ item.name }}</strong><small>↓ {{ formatBytes(item.download_bytes) }} · ↑ {{ formatBytes(item.upload_bytes) }}</small></span><em>{{ formatBytes(item.total_bytes) }}</em></li></ol>
          <div v-else class="rank-empty">暂无节点流量批次</div>
        </section>
      </div>
    </template>
  </main>
</template>

<style scoped>
.report-workspace { display: grid; gap: 20px; }
.report-chart-surface, .rank-surface { border: 1px solid var(--hf-line-strong); background: var(--hf-surface); }
.report-chart-surface > header, .rank-surface > header { display: flex; align-items: center; justify-content: space-between; min-height: 64px; padding: 12px 18px; border-bottom: 1px solid var(--hf-line); }
.report-chart-surface > header span, .rank-surface > header span { color: var(--hf-muted); font-family: var(--hf-data); font-size: 9px; }
.report-chart-surface h3, .rank-surface h3 { margin: 4px 0 0; color: var(--hf-ink); font-size: 14px; }
.report-chart-surface :deep(.traffic-trend) { padding: 12px 18px 8px 4px; }
.report-legend { display: flex; gap: 14px; color: var(--hf-muted); font-size: 10px; }
.report-legend span { display: flex; align-items: center; gap: 6px; }
.report-legend i { width: 18px; height: 2px; }.report-legend .is-download { background: var(--hf-flow); }.report-legend .is-upload { background: var(--hf-accent); }
.report-rank-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
.rank-surface > header { justify-content: flex-start; gap: 10px; }
.rank-surface ol { margin: 0; padding: 0; list-style: none; }
.rank-surface li { display: grid; min-height: 64px; grid-template-columns: 32px minmax(0, 1fr) auto; align-items: center; gap: 10px; padding: 9px 18px; border-bottom: 1px solid var(--hf-line); }
.rank-surface li:last-child { border-bottom: 0; }.rank-surface li b { color: var(--hf-muted); font-family: var(--hf-data); font-size: 10px; }.rank-surface li span strong,.rank-surface li span small { display: block; }.rank-surface li span strong { color: var(--hf-ink); font-size: 12px; }.rank-surface li span small { margin-top: 5px; color: var(--hf-muted); font-family: var(--hf-data); font-size: 9px; }.rank-surface li em { color: var(--hf-ink-soft); font-family: var(--hf-data); font-size: 11px; font-style: normal; }.rank-empty { display: grid; min-height: 180px; place-items: center; color: var(--hf-muted); font-size: 11px; }
@media (max-width: 900px) { .report-rank-grid { grid-template-columns: 1fr; } }
</style>
