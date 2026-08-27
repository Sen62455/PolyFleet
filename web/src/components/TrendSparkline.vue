<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(defineProps<{
  values: number[];
  label: string;
  color?: string;
}>(), { color: "var(--hf-accent)" });

const width = 180;
const height = 48;
const padding = 3;
const points = computed(() => {
  if (props.values.length < 2) return "";
  const minimum = Math.min(...props.values);
  const maximum = Math.max(...props.values);
  const range = Math.max(1, maximum - minimum);
  return props.values.map((value, index) => {
    const x = padding + (index / (props.values.length - 1)) * (width - padding * 2);
    const y = height - padding - ((value - minimum) / range) * (height - padding * 2);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" ");
});
const areaPoints = computed(() => points.value
  ? `${padding},${height - padding} ${points.value} ${width - padding},${height - padding}`
  : "");
</script>

<template>
  <div class="trend-sparkline" role="img" :aria-label="label">
    <svg v-if="points" :viewBox="`0 0 ${width} ${height}`" preserveAspectRatio="none" aria-hidden="true">
      <polygon :points="areaPoints" :fill="color" opacity="0.08" />
      <polyline :points="points" :stroke="color" fill="none" vector-effect="non-scaling-stroke" />
    </svg>
    <span v-else>等待历史采样</span>
  </div>
</template>
