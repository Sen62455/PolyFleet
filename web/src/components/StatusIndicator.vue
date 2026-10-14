<script setup lang="ts">
import { computed } from "vue";
import StatusPill from "./StatusPill.vue";
import { statusLabels } from "../lib/format";
import type { NodeStatus } from "../types";

const props = withDefaults(defineProps<{ status: NodeStatus; showLabel?: boolean }>(), { showLabel: true });
const label = computed(() => statusLabels[props.status] ?? props.status);
const type = computed(() => ({ online: "success", stale: "warning", pending: "warning", offline: "error", degraded: "error", disabled: "default" })[props.status]);
</script>

<template>
  <status-pill v-if="showLabel" :type="type" dot>{{ label }}</status-pill>
  <span v-else class="status-indicator" :class="`status-indicator--${status}`" :aria-label="label">
    <span class="status-indicator__dot" aria-hidden="true" />
  </span>
</template>
