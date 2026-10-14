<script setup lang="ts">
import StatusPill from "../../components/StatusPill.vue";
import { Bell, Check, RefreshCw } from "@lucide/vue";
import { NButton, NDrawer, NDrawerContent, NIcon, NSpin, NTag, NTooltip } from "naive-ui";
import { relativeTime } from "../../lib/format";
import type { AlertRecord } from "../../types";

defineProps<{
  show: boolean;
  alerts: AlertRecord[];
  loading: boolean;
  working: string;
}>();
const emit = defineEmits<{
  "update:show": [show: boolean];
  refresh: [];
  acknowledge: [alert: AlertRecord];
  "select-node": [nodeId: string];
}>();

const alertLabels: Record<AlertRecord["type"], string> = {
  offline: "节点离线",
  degraded: "节点降级",
  core_down: "核心停止",
  usage_error: "流量采集异常",
  sync_failed: "同步失败",
  sync_stuck: "同步超时",
  operation_failed: "运维操作失败",
  traffic_quota_warning: "节点流量接近额度",
  traffic_quota_exhausted: "节点流量额度已用尽",
};
</script>

<template>
  <n-drawer :show="show" width="min(440px, 100vw)" placement="right" @update:show="emit('update:show', $event)">
    <n-drawer-content title="告警" closable>
      <div v-if="loading" class="alert-drawer-state"><n-spin :size="24" /></div>
      <div v-else-if="alerts.length" class="alert-record-list">
        <article v-for="alert in alerts" :key="alert.id" class="alert-record">
          <header>
            <div>
              <strong>{{ alertLabels[alert.type] }}</strong>
              <button type="button" @click="emit('select-node', alert.node_id)">{{ alert.node_name }}</button>
            </div>
            <status-pill :type="alert.severity === 'critical' ? 'error' : 'warning'">
              {{ alert.severity === "critical" ? "严重" : "警告" }}
            </status-pill>
          </header>
          <p>{{ alert.message }}</p>
          <footer>
            <span>{{ relativeTime(alert.last_seen_at) }}</span>
            <span v-if="alert.status === 'acknowledged'">已确认</span>
            <n-tooltip v-else trigger="hover">
              <template #trigger>
                <n-button
                  circle
                  quaternary
                  size="small"
                  :loading="working === alert.id"
                  :aria-label="`确认 ${alert.node_name} 告警`"
                  @click="emit('acknowledge', alert)"
                >
                  <template #icon><n-icon><check /></n-icon></template>
                </n-button>
              </template>
              确认告警
            </n-tooltip>
          </footer>
        </article>
      </div>
      <div v-else class="alert-drawer-state alert-drawer-state--empty">
        <bell :size="26" :stroke-width="1.7" aria-hidden="true" />
        <span>当前没有活动告警</span>
      </div>

      <template #footer>
        <n-button secondary @click="emit('refresh')">
          <template #icon><n-icon><refresh-cw /></n-icon></template>
          刷新状态
        </n-button>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>
