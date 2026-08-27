<script setup lang="ts">
import { computed, h } from "vue";
import { Archive, KeyRound, MoreHorizontal, Pencil, ServerCog, Wifi } from "@lucide/vue";
import { NButton, NCheckbox, NDropdown, NIcon, NTooltip, type DropdownOption } from "naive-ui";
import MetricBar from "../../components/MetricBar.vue";
import StatusIndicator from "../../components/StatusIndicator.vue";
import {
  adapterLabels,
  formatBytes,
  formatPercent,
  formatRate,
  percent,
  relativeTime,
} from "../../lib/format";
import type { NodeRecord } from "../../types";

const props = withDefaults(defineProps<{ nodes: NodeRecord[]; selectedNodeIds?: string[] }>(), { selectedNodeIds: () => [] });
const emit = defineEmits<{
  select: [node: NodeRecord];
  action: [action: "edit" | "enroll" | "archive", node: NodeRecord];
  "update:selected-node-ids": [nodeIds: string[]];
}>();

const allSelected = computed(() => props.nodes.length > 0 && props.nodes.every((node) => props.selectedNodeIds.includes(node.id)));

function setSelected(nodeId: string, selected: boolean) {
  const next = new Set(props.selectedNodeIds);
  if (selected) next.add(nodeId);
  else next.delete(nodeId);
  emit("update:selected-node-ids", [...next]);
}

function setAll(selected: boolean) {
  emit("update:selected-node-ids", selected ? props.nodes.map((node) => node.id) : []);
}

function icon(component: typeof Pencil) {
  return () => h(NIcon, null, { default: () => h(component, { size: 16 }) });
}

const options: DropdownOption[] = [
  { label: "编辑", key: "edit", icon: icon(Pencil) },
  { label: "生成注册令牌", key: "enroll", icon: icon(KeyRound) },
  { type: "divider", key: "divider" },
  { label: "归档", key: "archive", icon: icon(Archive) },
];

function choose(key: string | number, node: NodeRecord) {
  emit("action", key as "edit" | "enroll" | "archive", node);
}
</script>

<template>
  <div class="node-table-wrap node-table-wrap--desktop">
    <table class="node-table">
      <thead>
        <tr>
          <th class="node-table__select"><n-checkbox :checked="allSelected" :indeterminate="selectedNodeIds.length > 0 && !allSelected" aria-label="选择全部节点" @update:checked="setAll" /></th>
          <th>节点</th>
          <th>状态</th>
          <th>资源</th>
          <th>网络</th>
          <th>用量</th>
          <th>最后上报</th>
          <th><span class="sr-only">操作</span></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="node in nodes"
          :key="node.id"
          tabindex="0"
          @click="emit('select', node)"
          @keydown.enter="emit('select', node)"
        >
          <td class="node-table__select" @click.stop @keydown.stop><n-checkbox :checked="selectedNodeIds.includes(node.id)" :aria-label="`选择 ${node.name}`" @update:checked="setSelected(node.id, $event)" /></td>
          <td>
            <div class="node-identity">
              <span class="node-identity__icon"><server-cog :size="18" aria-hidden="true" /></span>
              <span>
                <strong>{{ node.name }}</strong>
                <small>{{ [node.provider, node.region].filter(Boolean).join(" · ") || "未填写位置" }}</small>
              </span>
            </div>
          </td>
          <td>
            <status-indicator :status="node.status" />
            <span class="table-secondary">{{ adapterLabels[node.adapter_type] }}</span>
          </td>
          <td class="resource-cell">
            <metric-bar label="CPU" :value="node.cpu_percent" :display="formatPercent(node.cpu_percent)" />
            <metric-bar
              label="内存"
              :value="percent(node.memory_used_bytes, node.memory_total_bytes)"
              :display="formatBytes(node.memory_used_bytes)"
            />
            <metric-bar
              label="磁盘"
              :value="percent(node.disk_used_bytes, node.disk_total_bytes)"
              :display="formatPercent(percent(node.disk_used_bytes, node.disk_total_bytes))"
            />
          </td>
          <td>
            <span class="network-value network-value--down">↓ {{ formatRate(node.network_rx_bps) }}</span>
            <span class="network-value network-value--up">↑ {{ formatRate(node.network_tx_bps) }}</span>
          </td>
          <td>
            <span class="traffic-value">
              {{ formatBytes(node.traffic_used_bytes) }}<template v-if="node.traffic_limit_bytes"> / {{ formatBytes(node.traffic_limit_bytes) }}</template>
            </span>
            <span class="table-secondary online-inline" :class="{ 'online-inline--active': node.online_connections > 0 }">
              <wifi :size="12" aria-hidden="true" />{{ node.online_connections }} 个活跃连接
            </span>
          </td>
          <td>
            <span class="last-seen">{{ relativeTime(node.last_seen_at) }}</span>
            <span class="table-secondary">v{{ node.applied_version }} / {{ node.desired_version }}</span>
          </td>
          <td class="action-cell" @click.stop @keydown.stop>
            <n-dropdown trigger="click" :options="options" @select="choose($event, node)">
              <n-tooltip trigger="hover">
                <template #trigger>
                  <n-button quaternary circle :aria-label="`${node.name} 操作`">
                    <template #icon><n-icon><more-horizontal /></n-icon></template>
                  </n-button>
                </template>
                操作
              </n-tooltip>
            </n-dropdown>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="node-mobile-list">
    <article v-for="node in nodes" :key="node.id" class="node-mobile-item" @click="emit('select', node)">
      <header>
        <div @click.stop><n-checkbox :checked="selectedNodeIds.includes(node.id)" :aria-label="`选择 ${node.name}`" @update:checked="setSelected(node.id, $event)" /></div>
        <div class="node-identity">
          <span class="node-identity__icon"><server-cog :size="18" aria-hidden="true" /></span>
          <span>
            <strong>{{ node.name }}</strong>
            <small>{{ [node.provider, node.region].filter(Boolean).join(" · ") || adapterLabels[node.adapter_type] }}</small>
          </span>
        </div>
        <div @click.stop>
          <n-dropdown trigger="click" :options="options" @select="choose($event, node)">
            <n-button quaternary circle :aria-label="`${node.name} 操作`">
              <template #icon><n-icon><more-horizontal /></n-icon></template>
            </n-button>
          </n-dropdown>
        </div>
      </header>
      <div class="node-mobile-item__status">
        <status-indicator :status="node.status" />
        <span>{{ relativeTime(node.last_seen_at) }}</span>
      </div>
      <div class="node-mobile-item__metrics">
        <metric-bar label="CPU" :value="node.cpu_percent" :display="formatPercent(node.cpu_percent)" />
        <metric-bar
          label="内存"
          :value="percent(node.memory_used_bytes, node.memory_total_bytes)"
          :display="formatBytes(node.memory_used_bytes)"
        />
      </div>
      <footer>
        <span>↓ {{ formatRate(node.network_rx_bps) }}</span>
        <span>↑ {{ formatRate(node.network_tx_bps) }}</span>
        <span>配置 v{{ node.applied_version }} / {{ node.desired_version }}</span>
        <span><wifi :size="11" aria-hidden="true" /> {{ node.online_connections }} 个连接 · {{ formatBytes(node.traffic_used_bytes) }}</span>
      </footer>
    </article>
  </div>
</template>
