<script setup lang="ts">
import { computed } from 'vue';
import { severity as loadSeverity } from '../lib/format';

const props = defineProps<{
  label: string;
  value: number;
  display: string;
  detail?: string;
}>();

const normalized = computed(() => Math.max(0, Math.min(100, props.value)));
const severity = computed(() => loadSeverity(normalized.value));
</script>

<template>
  <div class="metric-bar">
    <div class="metric-bar__label">
      <span class="metric-bar__name">{{ label }}</span>
      <span v-if="detail" class="metric-bar__detail">{{ detail }}</span>
      <span class="metric-bar__value">{{ display }}</span>
    </div>
    <div class="metric-bar__track" aria-hidden="true">
      <span :class="`metric-bar__fill metric-bar__fill--${severity}`" :style="{ width: `${normalized}%` }" />
    </div>
  </div>
</template>
