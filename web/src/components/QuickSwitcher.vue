<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { ArrowUpRight, CornerDownLeft, LayoutDashboard, Search, Server } from "@lucide/vue";
import { NIcon, NInput, NModal } from "naive-ui";
import { consolePages, type PageView } from "../lib/navigation";
import { statusLabels } from "../lib/format";
import type { NodeRecord } from "../types";

const props = defineProps<{ show: boolean; nodes: NodeRecord[]; loading: boolean; error: string }>();
const emit = defineEmits<{ "update:show": [value: boolean]; navigate: [view: PageView]; "select-node": [id: string] }>();
const query = ref("");
const selected = ref(0);
const input = ref<InstanceType<typeof NInput> | null>(null);
type Result = { id: string; label: string; hint: string; view?: PageView; nodeId?: string };
const matches = computed(() => {
  const terms = query.value.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const match = (text: string) => terms.every((term) => text.toLocaleLowerCase().includes(term));
  const pages: Result[] = consolePages.filter((page) => match(`${page.label} ${page.keywords}`))
    .map((page) => ({ id: `page-${page.key}`, label: page.label, hint: `${page.group} · 控制台页面`, view: page.key }));
  const nodes: Result[] = props.nodes.filter((node) => match(`${node.name} ${node.public_host} ${node.provider} ${node.region}`))
    .map((node) => ({ id: `node-${node.id}`, label: node.name, hint: [node.public_host, node.provider, node.region, statusLabels[node.status]].filter(Boolean).join(" · "), nodeId: node.id }));
  return [...pages, ...nodes];
});
const results = computed(() => matches.value.slice(0, 50));
watch(query, () => { selected.value = 0; });
watch(() => results.value.length, (length) => { selected.value = Math.min(selected.value, Math.max(0, length - 1)); });
watch(() => props.show, async (show) => {
  if (show) {
    query.value = "";
    selected.value = 0;
    await nextTick();
    input.value?.focus();
  }
});

function choose(result: Result) {
  emit("update:show", false);
  if (result.view) emit("navigate", result.view);
  else if (result.nodeId) emit("select-node", result.nodeId);
}

async function onKeydown(event: KeyboardEvent) {
  if (event.isComposing || !["ArrowDown", "ArrowUp", "Enter"].includes(event.key)) return;
  event.preventDefault();
  if (!results.value.length) return;
  if (event.key === "Enter") {
    choose(results.value[selected.value]);
    return;
  }
  selected.value = (selected.value + (event.key === "ArrowDown" ? 1 : -1) + results.value.length) % results.value.length;
  await nextTick();
  document.getElementById(`quick-result-${selected.value}`)?.scrollIntoView({ block: "nearest" });
}
</script>

<template>
  <n-modal :show="show" preset="card" title="快速跳转" class="quick-switcher compact-dialog" :bordered="false" :auto-focus="false" :style="{ width: 'min(640px, calc(100vw - 32px))', maxHeight: 'calc(100dvh - 32px)' }" @update:show="emit('update:show', $event)" @after-enter="input?.focus()">
    <n-input ref="input" v-model:value="query" size="large" clearable placeholder="搜索页面、节点名称、IP 或供应商" :input-props="{ role: 'combobox', 'aria-label': '快速跳转搜索', 'aria-autocomplete': 'list', 'aria-expanded': true, 'aria-controls': 'quick-search-results', 'aria-activedescendant': results.length ? `quick-result-${selected}` : undefined }" @keydown="onKeydown">
      <template #prefix><n-icon><search /></n-icon></template>
    </n-input>
    <div class="quick-switcher__caption" aria-live="polite">{{ query.trim() ? `找到 ${matches.length} 项${matches.length > 50 ? '，显示前 50 项，请继续输入缩小范围' : ''}` : '控制台页面与节点' }}<span v-if="loading"> · 正在加载节点</span><span v-else-if="error"> · 节点加载失败，请刷新后重试</span></div>
    <ul id="quick-search-results" class="quick-switcher__results" role="listbox" aria-label="跳转目标">
      <li v-for="(result, index) in results" :id="`quick-result-${index}`" :key="result.id" role="option" :aria-selected="index === selected" class="quick-switcher__result" :class="{ 'is-selected': index === selected }" @mousemove="selected = index" @mousedown.prevent @click="choose(result)">
        <span class="quick-switcher__icon"><layout-dashboard v-if="result.view" :size="18" aria-hidden="true" /><server v-else :size="18" aria-hidden="true" /></span>
        <span class="quick-switcher__text"><strong>{{ result.label }}</strong><small>{{ result.hint }}</small></span>
        <span class="quick-switcher__kind">{{ result.view ? '页面' : '节点' }}</span><arrow-up-right :size="15" aria-hidden="true" />
      </li>
    </ul>
    <div v-if="!results.length" class="quick-switcher__empty">没有匹配的页面或节点，试试名称、IP 或供应商。</div>
    <footer class="quick-switcher__footer"><span><kbd>↑</kbd><kbd>↓</kbd> 选择</span><span><corner-down-left :size="13" aria-hidden="true" /> 打开</span><span><kbd>Esc</kbd> 关闭</span></footer>
  </n-modal>
</template>

<style scoped>
.quick-switcher__caption { padding: 14px 2px 8px; color: var(--hf-muted); font-size: var(--fs-xs); }
.quick-switcher__results { max-height: max(80px, min(440px, calc(100dvh - 260px))); overflow-y: auto; margin: 0; padding: 0; list-style: none; }
.quick-switcher__result { display: flex; align-items: center; gap: 12px; margin: 2px 0; padding: 12px; border-radius: var(--hf-radius-md); color: var(--hf-muted); cursor: pointer; }
.quick-switcher__result.is-selected { background: var(--hf-accent-soft); color: var(--hf-accent); }
.quick-switcher__icon { display: grid; width: 34px; height: 34px; flex-shrink: 0; place-items: center; border: 1px solid var(--hf-line); border-radius: var(--hf-radius-sm); background: var(--hf-surface); }
.quick-switcher__text { display: grid; min-width: 0; flex: 1; gap: 2px; }
.quick-switcher__text strong, .quick-switcher__text small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.quick-switcher__text strong { color: var(--hf-ink); font-size: var(--fs-md); font-weight: 600; }
.quick-switcher__text small { color: var(--hf-muted); font-size: var(--fs-xs); }
.quick-switcher__kind { font-size: var(--fs-xs); }
.quick-switcher__result > svg { flex-shrink: 0; }
.quick-switcher__empty { padding: 32px 16px; color: var(--hf-muted); text-align: center; font-size: var(--fs-sm); }
.quick-switcher__footer { display: flex; align-items: center; gap: 18px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--hf-line); color: var(--hf-muted); font-size: var(--fs-xs); }
.quick-switcher__footer span { display: flex; align-items: center; gap: 4px; }
.quick-switcher__footer kbd { min-width: 18px; padding: 1px 4px; border: 1px solid var(--hf-line-strong); border-radius: 4px; background: var(--hf-surface-muted); font-family: inherit; text-align: center; }
</style>
