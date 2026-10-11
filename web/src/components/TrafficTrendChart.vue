<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from "vue";
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

type SeriesKey = "upload_bytes" | "download_bytes";

const uid = useId();
const root = ref<HTMLElement | null>(null);
const width = ref(960); // 真实像素宽度，由 ResizeObserver 回填
const height = 280;
const inset = { top: 16, right: 20, bottom: 32, left: 60 };
const plotHeight = height - inset.top - inset.bottom;
const baseY = inset.top + plotHeight;
const hoverIndex = ref<number | null>(null);
let observer: ResizeObserver | undefined;

onMounted(() => {
  const el = root.value;
  if (!el) return;
  const measure = (value: number) => {
    if (value > 0) width.value = Math.max(200, Math.round(value));
  };
  measure(el.clientWidth);
  if (typeof ResizeObserver === "undefined") return;
  observer = new ResizeObserver((entries) => measure(entries[0].contentRect.width));
  observer.observe(el);
});
onBeforeUnmount(() => observer?.disconnect());

const maxValue = computed(() => {
  const values = [...props.points, ...props.previousPoints]
    .flatMap((point) => [point.upload_bytes, point.download_bytes]);
  return Math.max(1, props.capacityBytes, ...values) * 1.12;
});

function pointX(index: number, length: number) {
  const span = Math.max(1, length - 1);
  return inset.left + (index / span) * (width.value - inset.left - inset.right);
}

function pointY(value: number) {
  return inset.top + (1 - value / maxValue.value) * plotHeight;
}

function polyline(points: TrafficReportPoint[], key: SeriesKey) {
  return points.map((point, index) => `${pointX(index, points.length)},${pointY(point[key])}`).join(" ");
}

function area(points: TrafficReportPoint[], key: SeriesKey) {
  if (points.length < 2) return "";
  const line = points.map((point, index) => `${pointX(index, points.length)},${pointY(point[key])}`).join(" L ");
  return `M ${pointX(0, points.length)},${baseY} L ${line} L ${pointX(points.length - 1, points.length)},${baseY} Z`;
}

const ticks = computed(() => [0, 0.25, 0.5, 0.75, 1].map((ratio) => ({
  ratio,
  y: pointY(maxValue.value * ratio),
  label: formatCompact(maxValue.value * ratio),
})));

const dateLabels = computed(() => {
  if (!props.points.length) return [];
  // 约每 84px 放一个日期，窄屏自动减少，避免互相重叠
  const fit = Math.floor((width.value - inset.left - inset.right) / 84);
  const wanted = Math.min(props.points.length, Math.max(2, Math.min(8, fit)));
  return Array.from({ length: wanted }, (_, index) => {
    const pointIndex = Math.round((index / Math.max(1, wanted - 1)) * (props.points.length - 1));
    return { x: pointX(pointIndex, props.points.length), label: formatDay(props.points[pointIndex].bucket_at) };
  });
});

const hover = computed(() => {
  const index = hoverIndex.value;
  const point = index === null ? undefined : props.points[index];
  if (index === null || !point) return null;
  const x = pointX(index, props.points.length);
  return {
    x,
    yDown: pointY(point.download_bytes),
    yUp: pointY(point.upload_bytes),
    down: formatCompact(point.download_bytes),
    up: formatCompact(point.upload_bytes),
    date: formatDay(point.bucket_at),
    left: Math.max(0, Math.min(width.value - Math.min(184, width.value), x - 92)),
  };
});

function onKeydown(event: KeyboardEvent) {
  if (!props.points.length) return;
  const current = hoverIndex.value ?? 0;
  const next = event.key === "ArrowRight" ? current + 1 : event.key === "ArrowLeft" ? current - 1 : event.key === "Home" ? 0 : event.key === "End" ? props.points.length - 1 : null;
  if (next === null) return;
  event.preventDefault();
  hoverIndex.value = Math.max(0, Math.min(props.points.length - 1, next));
}

function onMove(event: PointerEvent) {
  const length = props.points.length;
  if (!length || !root.value) return;
  const x = event.clientX - root.value.getBoundingClientRect().left;
  const ratio = (x - inset.left) / (width.value - inset.left - inset.right);
  hoverIndex.value = Math.min(length - 1, Math.max(0, Math.round(ratio * Math.max(1, length - 1))));
}

function formatDay(value: string) {
  const date = new Date(value);
  return `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

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
  <div
    ref="root"
    class="traffic-trend"
    role="img"
    tabindex="0"
    @keydown="onKeydown"
    @focus="hoverIndex = points.length ? 0 : null"
    @blur="hoverIndex = null"
    :aria-label="label"
    @pointermove="onMove"
    @pointerleave="hoverIndex = null"
  >
    <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" aria-hidden="true">
      <defs>
        <linearGradient :id="`${uid}-down`" x1="0" y1="0" x2="0" y2="1">
          <stop class="traffic-trend__stop" offset="0" stop-opacity="0.2" />
          <stop class="traffic-trend__stop" offset="1" stop-opacity="0" />
        </linearGradient>
      </defs>
      <g class="traffic-trend__grid">
        <template v-for="tick in ticks" :key="tick.ratio">
          <line :class="{ 'is-base': tick.ratio === 0 }" :x1="inset.left" :x2="width - inset.right" :y1="tick.y" :y2="tick.y" />
          <text :x="inset.left - 12" :y="tick.y + 4" text-anchor="end">{{ tick.label }}</text>
        </template>
      </g>
      <line v-if="capacityBytes > 0" class="traffic-trend__capacity" :x1="inset.left" :x2="width - inset.right" :y1="pointY(capacityBytes)" :y2="pointY(capacityBytes)" />
      <polyline v-if="previousPoints.length" class="traffic-trend__line traffic-trend__line--previous-download" :points="polyline(previousPoints, 'download_bytes')" />
      <polyline v-if="previousPoints.length" class="traffic-trend__line traffic-trend__line--previous-upload" :points="polyline(previousPoints, 'upload_bytes')" />
      <path v-if="points.length > 1" class="traffic-trend__area" :d="area(points, 'download_bytes')" :fill="`url(#${uid}-down)`" />
      <polyline v-if="points.length" class="traffic-trend__line traffic-trend__line--download" :points="polyline(points, 'download_bytes')" />
      <polyline v-if="points.length" class="traffic-trend__line traffic-trend__line--upload" :points="polyline(points, 'upload_bytes')" />
      <g v-if="hover">
        <line class="traffic-trend__cursor" :x1="hover.x" :x2="hover.x" :y1="inset.top" :y2="baseY" />
        <circle class="traffic-trend__dot traffic-trend__dot--download" :cx="hover.x" :cy="hover.yDown" r="4.5" />
        <circle class="traffic-trend__dot traffic-trend__dot--upload" :cx="hover.x" :cy="hover.yUp" r="4.5" />
      </g>
      <g class="traffic-trend__dates">
        <text v-for="item in dateLabels" :key="item.x" :x="item.x" :y="height - 8" text-anchor="middle">{{ item.label }}</text>
      </g>
    </svg>
    <div v-if="hover" class="traffic-trend__tip" :style="{ left: `${hover.left}px` }" aria-live="polite">
      <strong>{{ hover.date }}</strong>
      <div><span><i class="is-download" />下行</span><b>{{ hover.down }}</b></div>
      <div><span><i class="is-upload" />上行</span><b>{{ hover.up }}</b></div>
    </div>
    <div v-if="!points.length" class="traffic-trend__empty">暂无流量采样</div>
  </div>
</template>

<style scoped>
.traffic-trend { position: relative; width: 100%; height: 280px; touch-action: pan-y; }
.traffic-trend svg { display: block; max-width: 100%; overflow: visible; }

.traffic-trend__grid line { stroke: var(--hf-line); stroke-width: 1; stroke-dasharray: 3 5; }
.traffic-trend__grid line.is-base { stroke: var(--hf-line-strong); stroke-dasharray: none; }
.traffic-trend__grid text,
.traffic-trend__dates text { fill: var(--hf-muted); font-size: 12px; font-variant-numeric: tabular-nums; }

.traffic-trend__stop { stop-color: var(--hf-chart-primary); }
.traffic-trend__area { stroke: none; }
.traffic-trend__line { fill: none; stroke-linecap: round; stroke-linejoin: round; stroke-width: 2; }
.traffic-trend__line--download { stroke: var(--hf-chart-primary); }
.traffic-trend__line--upload { stroke: var(--hf-chart-secondary); }
.traffic-trend__line--previous-download { stroke: var(--hf-chart-primary); stroke-dasharray: 5 5; opacity: 0.4; }
.traffic-trend__line--previous-upload { stroke: var(--hf-chart-secondary); stroke-dasharray: 5 5; opacity: 0.4; }
.traffic-trend__capacity { stroke: var(--hf-pressure); stroke-width: 1.25; stroke-dasharray: 7 5; }

.traffic-trend__cursor { stroke: var(--hf-line-strong); stroke-width: 1; }
.traffic-trend__dot { stroke: var(--hf-surface); stroke-width: 2; }
.traffic-trend__dot--download { fill: var(--hf-chart-primary); }
.traffic-trend__dot--upload { fill: var(--hf-chart-secondary); }

.traffic-trend__tip {
  position: absolute;
  top: 4px;
  z-index: 2;
  display: grid;
  width: 184px;
  max-width: 100%;
  box-sizing: border-box;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid var(--hf-line);
  border-radius: var(--hf-radius-md);
  background: var(--hf-surface-raised);
  box-shadow: var(--hf-shadow-2);
  color: var(--hf-ink);
  font-size: var(--fs-xs);
  pointer-events: none;

}
.traffic-trend__tip strong { font-size: var(--fs-sm); font-weight: 600; }
.traffic-trend__tip div { display: flex; align-items: center; justify-content: space-between; gap: 16px; color: var(--hf-muted); }
.traffic-trend__tip span { display: inline-flex; align-items: center; gap: 6px; }
.traffic-trend__tip i { width: 8px; height: 8px; border-radius: 50%; }
.traffic-trend__tip i.is-download { background: var(--hf-chart-primary); }
.traffic-trend__tip i.is-upload { background: var(--hf-chart-secondary); }
.traffic-trend__tip b { color: var(--hf-ink); font-weight: 600; font-variant-numeric: tabular-nums; }

.traffic-trend__empty { position: absolute; inset: 0; display: grid; place-items: center; color: var(--hf-muted); font-size: var(--fs-sm); }
</style>
