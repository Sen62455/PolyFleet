<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ArrowDown, ArrowUp, BarChart3, RefreshCw, Server, TrendingDown, TrendingUp, UsersRound, Zap } from "@lucide/vue";
import { NButton, NIcon, NSpin, NTooltip, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import StatCard from "../../components/StatCard.vue";
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

function share(value: number) {
  return report.value?.total_bytes ? (value / report.value.total_bytes) * 100 : 0;
}

function barWidth(total: number, list: { total_bytes: number }[]) {
  const max = Math.max(1, ...list.map((item) => item.total_bytes));
  return total > 0 ? `${Math.max(2, (total / max) * 100)}%` : "0%";
}

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
      <div><h2>流量报表</h2><p>按控制面已接收批次统计，上下行合计为双向用量</p></div>
      <div class="page-heading__actions"><div class="ops-segments"><button :class="{ 'is-active': range === '7d' }" type="button" @click="setRange('7d')">7 天</button><button :class="{ 'is-active': range === '30d' }" type="button" @click="setRange('30d')">30 天</button></div><n-tooltip trigger="hover"><template #trigger><n-button circle secondary :loading="refreshing" aria-label="刷新报表" @click="load"><template #icon><n-icon><refresh-cw /></n-icon></template></n-button></template>刷新</n-tooltip></div>
    </div>

    <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
<template v-else-if="report">
  <section class="stat-grid" aria-label="流量摘要">
    <stat-card label="双向总量">
      <template #icon><bar-chart3 :size="18" /></template>
      {{ formatBytes(report.total_bytes) }}
      <template #hint>
        较上期
        <span class="delta" :class="{ 'is-warning': change > 20 }">
          <trending-up v-if="change >= 0" :size="12" /><trending-down v-else :size="12" />
          {{ change >= 0 ? '+' : '' }}{{ change.toFixed(1) }}%
        </span>
      </template>
    </stat-card>
    <stat-card label="下行">
      <template #icon><arrow-down :size="18" /></template>
      {{ formatBytes(report.download_bytes) }}
      <template #hint>{{ share(report.download_bytes).toFixed(1) }}% 占比</template>
      <template #extra><span class="stat-card__bar"><b :style="{ width: `${share(report.download_bytes)}%` }" /></span></template>
    </stat-card>
    <stat-card label="上行" tone="upload">
      <template #icon><arrow-up :size="18" /></template>
      {{ formatBytes(report.upload_bytes) }}
      <template #hint>{{ share(report.upload_bytes).toFixed(1) }}% 占比</template>
      <template #extra><span class="stat-card__bar"><b :style="{ width: `${share(report.upload_bytes)}%` }" /></span></template>
    </stat-card>
    <stat-card label="单日峰值" tone="neutral" :muted="!peak.total">
      <template #icon><zap :size="18" /></template>
      {{ formatBytes(peak.total) }}
      <template #hint>{{ peak.date ? new Date(peak.date).toLocaleDateString('zh-CN') : '暂无采样' }}</template>
    </stat-card>
  </section>

  <section class="report-chart-surface">
    <header>
      <h3>{{ report.range === '30d' ? '30 天' : '7 天' }}双向流量轨迹</h3>
      <div class="report-legend"><span><i class="is-download" />下行</span><span><i class="is-upload" />上行</span></div>
    </header>
    <div class="report-chart-body">
      <traffic-trend-chart :points="report.daily" label="每日上下行流量趋势" />
    </div>
  </section>

  <div class="report-rank-grid">
    <section class="rank-surface">
      <header><span class="rank-icon"><users-round :size="16" /></span><h3>用户用量排行</h3></header>
      <ol v-if="report.top_users.length">
        <li v-for="(item, index) in report.top_users" :key="item.id">
          <b class="rank-no" :class="{ 'is-top': index < 3 }">{{ index + 1 }}</b>
          <span class="rank-main">
            <strong>{{ item.name }}</strong>
            <small><arrow-down class="is-down" :size="12" />{{ formatBytes(item.download_bytes) }}<arrow-up class="is-up" :size="12" />{{ formatBytes(item.upload_bytes) }}</small>
          </span>
          <span class="rank-total">{{ formatBytes(item.total_bytes) }}</span>
          <i class="rank-bar" aria-hidden="true"><b :style="{ width: barWidth(item.total_bytes, report.top_users) }" /></i>
        </li>
      </ol>
      <div v-else class="rank-empty">暂无可归属用户流量</div>
    </section>
    <section class="rank-surface">
      <header><span class="rank-icon"><server :size="16" /></span><h3>节点用量排行</h3></header>
      <ol v-if="report.top_nodes.length">
        <li v-for="(item, index) in report.top_nodes" :key="item.id">
          <b class="rank-no" :class="{ 'is-top': index < 3 }">{{ index + 1 }}</b>
          <span class="rank-main">
            <strong>{{ item.name }}</strong>
            <small><arrow-down class="is-down" :size="12" />{{ formatBytes(item.download_bytes) }}<arrow-up class="is-up" :size="12" />{{ formatBytes(item.upload_bytes) }}</small>
          </span>
          <span class="rank-total">{{ formatBytes(item.total_bytes) }}</span>
          <i class="rank-bar" aria-hidden="true"><b :style="{ width: barWidth(item.total_bytes, report.top_nodes) }" /></i>
        </li>
      </ol>
      <div v-else class="rank-empty">暂无节点流量批次</div>
    </section>
  </div>
</template>
  </main>
</template>

<style scoped>
.report-workspace { display: grid; gap: var(--hf-space-5); }

.report-chart-surface,
.rank-surface {
  overflow: hidden;
  border: 1px solid var(--hf-line);
  border-radius: var(--hf-radius-lg);
  background: var(--hf-surface);
  box-shadow: var(--hf-shadow-1);
}
.report-chart-surface > header,
.rank-surface > header {
  display: flex;
  min-height: 56px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px;
  border-bottom: 1px solid var(--hf-line);
}
.report-chart-surface h3,
.rank-surface h3 { margin: 0; color: var(--hf-ink); font-size: var(--fs-md); font-weight: 600; }
.report-chart-body { padding: 16px 20px 8px; }

.report-legend { display: flex; gap: 14px; color: var(--hf-muted); font-size: var(--fs-xs); }
.report-legend span { display: inline-flex; align-items: center; gap: 6px; }
.report-legend i { width: 8px; height: 8px; border-radius: 50%; }
.report-legend .is-download { background: var(--hf-chart-primary); }
.report-legend .is-upload { background: var(--hf-chart-secondary); }

.report-rank-grid { display: grid; grid-template-columns: 1fr 1fr; align-items: start; gap: var(--hf-space-5); }
.rank-surface > header { justify-content: flex-start; }
.rank-icon { display: grid; width: 28px; height: 28px; place-items: center; border-radius: var(--hf-radius-sm); background: var(--hf-accent-soft); color: var(--hf-accent); }
.rank-surface ol { margin: 0; padding: 0; list-style: none; }
.rank-surface li {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr) auto;
  grid-template-areas: "no main total" ". bar bar";
  align-items: center;
  gap: 8px 12px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--hf-line);
  transition: background 160ms var(--hf-ease);
}
.rank-surface li:last-child { border-bottom: 0; }
.rank-surface li:hover { background: var(--hf-hover); }

.rank-no {
  grid-area: no;
  display: grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border-radius: var(--hf-radius-sm);
  background: var(--hf-hover);
  color: var(--hf-muted);
  font-size: var(--fs-xs);
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.rank-no.is-top { background: var(--hf-accent-soft); color: var(--hf-accent); }
.rank-main { grid-area: main; display: grid; min-width: 0; gap: 2px; }
.rank-main strong { overflow: hidden; color: var(--hf-ink); font-size: var(--fs-md); font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.rank-main small { display: flex; align-items: center; gap: 4px; color: var(--hf-muted); font-size: var(--fs-xs); font-variant-numeric: tabular-nums; }
.rank-main small .is-down { color: var(--hf-chart-primary); }
.rank-main small .is-up { margin-left: 8px; color: var(--hf-upload-ink); }
.rank-total { grid-area: total; color: var(--hf-ink); font-size: var(--fs-md); font-weight: 650; font-variant-numeric: tabular-nums; }
.rank-bar { grid-area: bar; display: block; height: 4px; overflow: hidden; border-radius: var(--hf-radius-pill); background: var(--hf-line); }
.rank-bar b { display: block; height: 100%; border-radius: inherit; background: var(--hf-accent); }
.rank-empty { display: grid; min-height: 180px; place-items: center; color: var(--hf-muted); font-size: var(--fs-sm); }

@media (max-width: 900px) { .report-rank-grid { grid-template-columns: 1fr; } }
</style>
