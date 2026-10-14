<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ArrowUpRight, CalendarClock, ChevronDown, CircleAlert, Download, FileQuestion, Package, RefreshCw, Search, Server } from "@lucide/vue";
import { NAlert, NButton, NDropdown, NIcon, NInput, NPagination, NSelect, NSpin, NTooltip, useMessage } from "naive-ui";
import StatCard from "../../components/StatCard.vue";
import { downloadText, localDate } from "../../lib/export";
import type { NodeAssetRecord, NodeRecord } from "../../types";
import { assetCSV, assetRows, expiryLabel, expiryTone, matchesRenewal, renewalCalendar, type RenewalFilter } from "./assets";

const props = defineProps<{
  nodes: NodeRecord[];
  assets: Record<string, NodeAssetRecord>;
  loading: boolean;
  refreshing: boolean;
  error: string;
}>();
const emit = defineEmits<{
  refresh: [];
  create: [];
  select: [node: NodeRecord];
  "edit-asset": [node: NodeRecord];
}>();
const message = useMessage();
const query = ref("");
const filter = ref<RenewalFilter>("all");
const provider = ref<string | null>(null);
const today = ref(new Date());
const page = ref(1);
const pageSize = 20;
const rows = computed(() => assetRows(props.nodes, props.assets, today.value));
const count = (value: RenewalFilter) => rows.value.filter((row) => matchesRenewal(row.days, value)).length;
const filterOptions = computed(() => [
  { label: `全部到期状态 · ${rows.value.length}`, value: "all" },
  { label: `已过期 · ${count("overdue")}`, value: "overdue" },
  { label: `7 天内到期 · ${count("7d")}`, value: "7d" },
  { label: `30 天内到期 · ${count("30d")}`, value: "30d" },
  { label: `未设置日期 · ${count("missing")}`, value: "missing" },
]);
const providerOptions = computed(() => [...new Set(props.nodes.map((node) => node.provider))]
  .sort((a, b) => a.localeCompare(b, "zh-CN"))
  .map((value) => ({ label: value || "未登记供应商", value })));
const filteredRows = computed(() => {
  const terms = query.value.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
  return rows.value.filter(({ node, asset, days }) => {
    const text = [node.name, node.public_host, node.provider, node.region, asset?.plan, asset?.notes].join(" ").toLocaleLowerCase();
    return matchesRenewal(days, filter.value) && (provider.value === null || provider.value === node.provider)
      && terms.every((term) => text.includes(term));
  });
});
const visibleRows = computed(() => filteredRows.value.slice((page.value - 1) * pageSize, page.value * pageSize));
const calendarCount = computed(() => filteredRows.value.filter((row) => row.days !== null && row.days >= 0).length);
const exportOptions = computed(() => [
  { label: `导出当前筛选的 ${filteredRows.value.length} 条`, key: "scope", disabled: true },
  { label: `资产清单 CSV（${filteredRows.value.length} 台）`, key: "csv" },
  { label: `续费日历 ICS（${calendarCount.value} 项）`, key: "calendar", disabled: !calendarCount.value },
]);

watch([query, filter, provider], () => { page.value = 1; });
watch(() => filteredRows.value.length, (length) => { page.value = Math.min(page.value, Math.max(1, Math.ceil(length / pageSize))); });

function selectFilter(value: RenewalFilter) {
  filter.value = value;
  query.value = "";
  provider.value = null;
}

function exportAssets(key: string) {
  if (props.loading || props.error || !filteredRows.value.length) return;
  const date = localDate(new Date());
  if (key === "csv") {
    downloadText(`polyfleet-assets-${date}.csv`, assetCSV(filteredRows.value));
    message.success(`已导出 ${filteredRows.value.length} 台资产`);
  } else if (key === "calendar" && calendarCount.value) {
    downloadText(`polyfleet-renewals-${date}.ics`, renewalCalendar(filteredRows.value, window.location.origin + window.location.pathname), "text/calendar;charset=utf-8");
    message.success(`已导出 ${calendarCount.value} 项到期日期，可导入日历查看提醒`);
  }
}

let dateTimer: number | undefined;
onMounted(() => { dateTimer = window.setInterval(() => { today.value = new Date(); }, 60_000); });
onBeforeUnmount(() => window.clearInterval(dateTimer));
</script>

<template>
  <main class="workspace operations-workspace assets-workspace">
    <div class="page-heading page-heading--console">
      <div><h2>资产与续费</h2><p>集中管理 VPS 档案，按到期日期安排续费</p></div>
      <div class="page-heading__actions">
        <n-tooltip trigger="hover"><template #trigger>
          <n-button circle secondary :loading="refreshing" aria-label="刷新资产" @click="emit('refresh')"><template #icon><n-icon><refresh-cw /></n-icon></template></n-button>
        </template>刷新</n-tooltip>
        <n-dropdown trigger="click" :options="exportOptions" @select="exportAssets">
          <n-button secondary :disabled="loading || !!error || !filteredRows.length">
            <template #icon><n-icon><download /></n-icon></template>导出筛选结果<chevron-down :size="14" class="asset-export-chevron" />
          </n-button>
        </n-dropdown>
      </div>
    </div>

    <n-alert v-if="error" type="error" :show-icon="false"><div class="alert-row"><span>{{ error }}</span><n-button text type="error" @click="emit('refresh')">重新加载</n-button></div></n-alert>
    <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
    <template v-else>
      <section class="stat-grid" aria-label="资产摘要">
        <stat-card label="全部资产" clickable :aria-pressed="filter === 'all'" @click="selectFilter('all')">
          <template #icon><package :size="18" /></template>{{ rows.length }}<template #hint>已接入的 VPS 节点</template>
        </stat-card>
        <stat-card label="已过期" tone="critical" clickable :muted="!count('overdue')" :aria-pressed="filter === 'overdue'" @click="selectFilter('overdue')">
          <template #icon><circle-alert :size="18" /></template>{{ count('overdue') }}<template #hint>核对续费状态并更新日期</template>
        </stat-card>
        <stat-card label="30 天内到期" tone="pressure" clickable :muted="!count('30d')" :aria-pressed="filter === '30d'" @click="selectFilter('30d')">
          <template #icon><calendar-clock :size="18" /></template>{{ count('30d') }}<template #hint>其中 {{ count('7d') }} 台在 7 天内到期</template>
        </stat-card>
        <stat-card label="未设置日期" tone="neutral" clickable :muted="!count('missing')" :aria-pressed="filter === 'missing'" @click="selectFilter('missing')">
          <template #icon><file-question :size="18" /></template>{{ count('missing') }}<template #hint>补全档案后可加入续费日历</template>
        </stat-card>
      </section>

      <section class="asset-surface" aria-label="资产清单">
        <div class="asset-toolbar">
          <n-input v-model:value="query" clearable placeholder="搜索节点、地址、套餐或备注" :input-props="{ 'aria-label': '搜索资产' }" class="asset-search">
            <template #prefix><n-icon><search /></n-icon></template>
          </n-input>
          <n-select v-model:value="provider" clearable :options="providerOptions" placeholder="全部供应商" aria-label="供应商筛选" class="asset-provider" />
          <n-select v-model:value="filter" :options="filterOptions" aria-label="到期状态筛选" class="asset-filter" />
          <span class="asset-toolbar__summary">匹配 {{ filteredRows.length }} / {{ rows.length }} · 按到期日升序</span>
        </div>

        <div v-if="!rows.length && !error" class="surface-state surface-state--empty">
          <server :size="28" aria-hidden="true" /><strong>添加节点后即可管理资产档案</strong><n-button type="primary" @click="emit('create')">添加节点</n-button>
        </div>
        <div v-else-if="!filteredRows.length" class="surface-state surface-state--empty">
          <search :size="28" aria-hidden="true" /><strong>{{ error ? '资产暂时不可用' : '没有匹配的资产' }}</strong>
          <n-button v-if="!error" secondary @click="selectFilter('all')">清除筛选</n-button>
        </div>
        <table v-else class="asset-table">
          <thead><tr><th scope="col">节点 / 供应商</th><th scope="col">套餐与备注</th><th scope="col">到期日期</th><th scope="col">续费记录</th><th scope="col" class="asset-actions-heading">操作</th></tr></thead>
          <tbody>
            <tr v-for="{ node, asset, days } in visibleRows" :key="node.id">
              <td class="asset-node-cell">
                <div class="asset-node-heading"><button type="button" class="asset-node-link" :title="node.name" @click="emit('select', node)"><strong>{{ node.name }}</strong><arrow-up-right :size="14" aria-hidden="true" /></button><span class="chip asset-provider-chip" :title="`${node.provider || '未登记供应商'} · ${node.region || '未登记地区'}`">{{ node.provider || '未登记供应商' }} · {{ node.region || '未登记地区' }}</span></div>
                <span class="asset-address mono" :title="node.public_host">{{ node.public_host }}</span>
              </td>
              <td data-label="套餐与备注" class="asset-plan-cell">
                <strong :title="asset?.plan">{{ asset?.plan || '未填写套餐' }}</strong>
                <span class="asset-note" :title="asset?.notes">{{ asset?.notes || '暂无备注' }}</span>
              </td>
              <td data-label="到期日期" class="asset-expiry-cell">
                <span class="pill" :class="`pill--${expiryTone(days)}`">{{ expiryLabel(days) }}</span>
                <time v-if="asset?.expires_at && days !== null" :datetime="asset.expires_at">{{ localDate(asset.expires_at) }}</time>
              </td>
              <td data-label="续费记录" class="asset-renewal-cell">
                <strong>{{ asset ? (asset.auto_renew ? '自动续费' : '手动续费') : '未登记' }}</strong>
                <span>{{ asset?.renewal_cycle_months ? `每 ${asset.renewal_cycle_months} 个月` : '周期未设置' }}</span>
              </td>
              <td class="asset-actions"><n-button size="small" secondary :aria-label="`编辑 ${node.name} 的资产档案`" @click="emit('edit-asset', node)">编辑档案</n-button></td>
            </tr>
          </tbody>
        </table>

        <footer v-if="filteredRows.length > pageSize" class="asset-list-footer">
          <n-pagination v-if="filteredRows.length > pageSize" v-model:page="page" :page-size="pageSize" :item-count="filteredRows.length" :page-slot="5" size="small" />
        </footer>
      </section>
      <div class="asset-calendar-note"><calendar-clock :size="16" aria-hidden="true" /><p>续费日历包含今天及以后的到期日期，提前 7 天提醒。日期修改后需重新导出；自动续费状态仅记录供应商侧设置。</p></div>
    </template>
  </main>
</template>

<style scoped>
.assets-workspace { display: grid; gap: var(--hf-space-5); }
.asset-export-chevron { margin-left: 8px; }
.asset-surface { min-width: 0; overflow: hidden; border: 1px solid var(--hf-line); border-radius: var(--hf-radius-lg); background: var(--hf-surface); box-shadow: var(--hf-shadow-1); }
.asset-toolbar { display: flex; flex-wrap: wrap; gap: 12px; padding: 20px 20px 12px; }
.asset-search { flex: 1 1 240px; }
.asset-provider { width: 172px; }
.asset-filter { width: 200px; }
.asset-list-caption, .asset-list-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; padding: 0 20px 16px; color: var(--hf-muted); font-size: var(--fs-xs); }
.asset-list-caption strong { color: var(--hf-ink); font-weight: 600; }
.asset-table { width: 100%; table-layout: fixed; border-collapse: collapse; font-size: var(--fs-sm); }
.asset-table th { padding: 10px 20px; border-block: 1px solid var(--hf-line); background: var(--hf-surface-muted); color: var(--hf-muted); text-align: left; font-size: var(--fs-xs); font-weight: 600; }
.asset-table th:nth-child(1) { width: 27%; }
.asset-table th:nth-child(2) { width: 25%; }
.asset-table th:nth-child(3) { width: 20%; }
.asset-table th:nth-child(4) { width: 15%; }
.asset-table th:nth-child(5) { width: 13%; }
.asset-table td { padding: 16px 20px; border-bottom: 1px solid var(--hf-line); vertical-align: middle; }
.asset-table tr:last-child td { border-bottom: 0; }
.asset-table tbody tr:hover { background: var(--hf-hover); }
.asset-table td > strong { display: block; overflow: hidden; color: var(--hf-ink); text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.asset-node-link { display: inline-flex; max-width: 100%; align-items: center; gap: 6px; padding: 0; border: 0; background: none; color: var(--hf-ink); text-align: left; cursor: pointer; font-size: var(--fs-md); }
.asset-node-link strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.asset-node-link svg { flex-shrink: 0; color: var(--hf-subtle); }
.asset-node-link:hover, .asset-node-link:hover svg { color: var(--hf-accent); }
.asset-node-link:focus-visible { outline: 2px solid var(--hf-accent); outline-offset: 4px; border-radius: 2px; }
.asset-address, .asset-note { display: block; overflow: hidden; margin-top: 4px; color: var(--hf-muted); text-overflow: ellipsis; white-space: nowrap; font-size: var(--fs-xs); }
.asset-meta { display: flex; align-items: center; gap: 6px; margin-top: 6px; color: var(--hf-muted); font-size: var(--fs-xs); }
.asset-meta > span:not(.asset-status) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.asset-meta b { font-weight: 400; }
.asset-status { width: 6px; height: 6px; flex-shrink: 0; border-radius: 50%; background: var(--hf-subtle); }
.asset-status.is-online { background: var(--hf-signal); }
.asset-status.is-offline, .asset-status.is-degraded { background: var(--hf-critical); }
.asset-status.is-stale, .asset-status.is-pending { background: var(--hf-pressure); }
.asset-expiry-cell time, .asset-renewal-cell > span { display: block; margin-top: 5px; color: var(--hf-muted); font-size: var(--fs-xs); }
.asset-actions, .asset-table .asset-actions-heading { text-align: right; }
.asset-list-footer { padding-block: 14px; border-top: 1px solid var(--hf-line); }
.asset-calendar-note { display: flex; align-items: flex-start; gap: 8px; color: var(--hf-muted); font-size: var(--fs-xs); line-height: 1.7; }
.asset-calendar-note svg { flex-shrink: 0; margin-top: 2px; }
.asset-calendar-note p { margin: 0; }
@media (max-width: 1100px) {
  .asset-table th, .asset-table td { padding-inline: 12px; }
  .asset-table th:nth-child(1) { width: 26%; }
  .asset-table th:nth-child(2) { width: 23%; }
  .asset-table th:nth-child(3) { width: 22%; }
  .asset-table th:nth-child(5) { width: 14%; }
}
@media (max-width: 700px) {
  .asset-toolbar { gap: 10px; padding: 16px 16px 12px; }
  .asset-search { flex-basis: 100%; }
  .asset-provider, .asset-filter { flex: 1; min-width: 0; }
  .asset-list-caption { align-items: flex-start; flex-direction: column; gap: 4px; padding-inline: 16px; }
  .asset-table, .asset-table tbody { display: block; }
  .asset-table thead { display: none; }
  .asset-table tr { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 14px; padding: 16px; border-top: 1px solid var(--hf-line); }
  .asset-table td { min-width: 0; padding: 0; border: 0; }
  .asset-node-cell { grid-column: 1; grid-row: 1; }
  .asset-actions { grid-column: 2; grid-row: 1; }
  .asset-plan-cell { grid-column: 1 / -1; }
  .asset-table td[data-label]::before { display: block; margin-bottom: 6px; color: var(--hf-muted); font-size: var(--fs-xs); content: attr(data-label); }
  .asset-renewal-cell { text-align: right; }
  .asset-list-footer { padding-inline: 16px; }
}

.asset-toolbar { align-items: center; padding-bottom: 16px; }
.asset-toolbar__summary { margin-left: auto; color: var(--hf-muted); font-size: var(--fs-xs); }
.asset-node-heading { display: flex; align-items: center; gap: 8px; min-width: 0; }
.asset-node-heading .asset-node-link { min-width: 0; flex: 1; }
.asset-provider-chip { display: block; max-width: 48%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 2px 5px; border: 0; background: var(--hf-hover); color: var(--hf-muted); }
.asset-note { display: -webkit-box; white-space: normal; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow-wrap: anywhere; }
@media (max-width: 700px) {
  .asset-toolbar__summary { width: 100%; margin-left: 0; }
  .asset-node-cell { grid-column: 1 / -1; }
  .asset-actions { grid-row: auto; grid-column: 1 / -1; }
}
</style>
