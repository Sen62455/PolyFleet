<script setup lang="ts">
import { computed } from "vue";
import type { TrafficReportPoint } from "../types";

const props = withDefaults(defineProps<{
  points: TrafficReportPoint[];
  previousPoints?: TrafficReportPoint[];
  capacityBytes?: number;
  label?: string;
}>(), {
  previousPoints: () => [],
  capacityBytes: 0,
  label: "30 日双向流量趋势",
});

const width = 1200;
const height = 278;
const inset = { top: 20, right: 22, bottom: 36, left: 66 };
const plotHeight = height - inset.top - inset.bottom;

const maxValue = computed(() => {
  const values = [...props.points, ...props.previousPoints]
    .flatMap((point) => [point.upload_bytes, point.download_bytes]);
  return Math.max(1, props.capacityBytes, ...values) * 1.12;
});

function pointX(index: number, length: number) {
  const span = Math.max(1, length - 1);
  return inset.left + (index / span) * (width - inset.left - inset.right);
}

function pointY(value: number) {
  return inset.top + (1 - value / maxValue.value) * plotHeight;
}

function polyline(points: TrafficReportPoint[], key: "upload_bytes" | "download_bytes") {
  return points.map((point, index) => `${pointX(index, points.length)},${pointY(point[key])}`).join(" ");
}

const ticks = computed(() => [0, 0.25, 0.5, 0.75, 1].map((ratio) => ({
  ratio,
  y: pointY(maxValue.value * ratio),
  label: formatCompact(maxValue.value * ratio),
})));

const dateLabels = computed(() => {
  if (!props.points.length) return [];
  const wanted = Math.min(8, props.points.length);
  return Array.from({ length: wanted }, (_, index) => {
    const pointIndex = Math.round((index / Math.max(1, wanted - 1)) * (props.points.length - 1));
    const date = new Date(props.points[pointIndex].bucket_at);
    return {
      x: pointX(pointIndex, props.points.length),
      label: `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`,
    };
  });
});

function formatCompact(value: number) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let scaled = value;
  let index = 0;
  while (scaled >= 1024 && index < units.length - 1) {
    scaled /= 1024;
    index += 1;
  }
  return `${scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`;
}
</script>

<template>
  <div class="traffic-trend" role="img" :aria-label="label">
    <svg :viewBox="`0 0 ${width} ${height}`" preserveAspectRatio="none" aria-hidden="true">
      <g class="traffic-trend__grid">
        <template v-for="tick in ticks" :key="tick.ratio">
          <line :x1="inset.left" :x2="width - inset.right" :y1="tick.y" :y2="tick.y" />
          <text :x="inset.left - 11" :y="tick.y + 4" text-anchor="end">{{ tick.label }}</text>
        </template>
      </g>
      <line v-if="capacityBytes > 0" class="traffic-trend__capacity" :x1="inset.left" :x2="width - inset.right" :y1="pointY(capacityBytes)" :y2="pointY(capacityBytes)" />
      <polyline v-if="previousPoints.length" class="traffic-trend__line traffic-trend__line--previous-download" :points="polyline(previousPoints, 'download_bytes')" />
      <polyline v-if="previousPoints.length" class="traffic-trend__line traffic-trend__line--previous-upload" :points="polyline(previousPoints, 'upload_bytes')" />
      <polyline v-if="points.length" class="traffic-trend__line traffic-trend__line--download" :points="polyline(points, 'download_bytes')" />
      <polyline v-if="points.length" class="traffic-trend__line traffic-trend__line--upload" :points="polyline(points, 'upload_bytes')" />
      <g class="traffic-trend__dates">
        <text v-for="item in dateLabels" :key="item.x" :x="item.x" :y="height - 9" text-anchor="middle">{{ item.label }}</text>
      </g>
    </svg>
    <div v-if="!points.length" class="traffic-trend__empty">暂无流量采样</div>
  </div>
</template>

<style scoped>
.traffic-trend { position: relative; width: 100%; aspect-ratio: 4.35 / 1; min-height: 238px; }
.traffic-trend svg { display: block; width: 100%; height: 100%; overflow: visible; }
.traffic-trend__grid line { stroke: var(--hf-line); stroke-width: 1; vector-effect: non-scaling-stroke; }
.traffic-trend__grid text,
.traffic-trend__dates text { fill: var(--hf-muted); font-family: var(--hf-data); font-size: 10px; }
.traffic-trend__line { fill: none; stroke-linecap: round; stroke-linejoin: round; stroke-width: 2; vector-effect: non-scaling-stroke; }
.traffic-trend__line--download { stroke: var(--hf-flow); }
.traffic-trend__line--upload { stroke: var(--hf-accent); }
.traffic-trend__line--previous-download { stroke: var(--hf-flow); stroke-dasharray: 5 5; opacity: 0.42; }
.traffic-trend__line--previous-upload { stroke: var(--hf-accent); stroke-dasharray: 5 5; opacity: 0.42; }
.traffic-trend__capacity { stroke: var(--hf-pressure); stroke-width: 1.25; stroke-dasharray: 7 5; vector-effect: non-scaling-stroke; }
.traffic-trend__empty { position: absolute; inset: 0; display: grid; place-items: center; color: var(--hf-muted); font-size: 12px; }
@media (max-width: 760px) { .traffic-trend { min-width: 720px; min-height: 210px; aspect-ratio: 3.4 / 1; } }
</style>
