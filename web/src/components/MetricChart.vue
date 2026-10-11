<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";

export interface ChartSeries {
  name: string;
  color: string;
  values: number[];
}

const props = withDefaults(defineProps<{
  series: ChartSeries[];
  labels: string[];
  label?: string;
  valueFormatter?: (value: number) => string;
  emptyLabel?: string;
}>(), {
  valueFormatter: (value: number) => value.toFixed(1),
  emptyLabel: "暂无历史数据",
});

const activeIndex = ref<number | null>(null);
const chartFocused = ref(false);
const announcement = ref("");
const chartRoot = ref<HTMLElement | null>(null);
const chartCanvas = ref<SVGSVGElement | null>(null);
const summaryId = `${useId()}-summary`;
const chartWidth = ref(640);
const height = 220;
const padding = { top: 18, right: 24, bottom: 38, left: 82 };
const plotWidth = computed(() => chartWidth.value - padding.left - padding.right);
const plotHeight = height - padding.top - padding.bottom;
let resizeObserver: ResizeObserver | null = null;

const pointCount = computed(() => Math.max(0, ...props.series.map((item) => item.values.length)));
const maximum = computed(() => {
  const values = props.series.flatMap((item) => item.values).filter(Number.isFinite);
  const found = Math.max(0, ...values);
  return found > 0 ? found * 1.08 : 1;
});
const lines = computed(() => props.series.map((item) => ({
  ...item,
  points: item.values.map((value, index) => `${x(index)},${y(value)}`).join(" "),
})));
const areaGradientId = `${useId()}-area`;
const firstArea = computed(() => {
  const first = lines.value[0];
  if (!first || first.values.length < 2) return null;
  const baseline = padding.top + plotHeight;
  const path = first.values.map((value, index) => `L ${x(index)},${y(value)}`).join(' ');
  return {
    color: first.color,
    d: `M ${x(0)},${baseline} ${path} L ${x(first.values.length - 1)},${baseline} Z`,
  };
});

const ticks = computed(() => [1, 0.75, 0.5, 0.25, 0].map((ratio) => ({
  ratio,
  value: maximum.value * ratio,
  y: padding.top + plotHeight * (1 - ratio),
})));
const seriesNames = computed(() => props.series.map((item) => item.name).filter(Boolean).join("、"));
const chartAriaLabel = computed(() => {
  const metric = props.label || seriesNames.value || "指标";
  return `${metric}历史折线图${seriesNames.value ? `，序列：${seriesNames.value}` : ""}`;
});
const chartSummary = computed(() => {
  if (pointCount.value === 0) return props.emptyLabel;
  const summaries = props.series.map((item) => {
    const values = item.values.filter(Number.isFinite);
    if (values.length === 0) return `${item.name}暂无有效数据`;
    const latest = values[values.length - 1];
    return `${item.name}最低 ${props.valueFormatter(Math.min(...values))}，最高 ${props.valueFormatter(Math.max(...values))}，最新 ${props.valueFormatter(latest)}`;
  });
  return `共 ${pointCount.value} 个时间点。${summaries.join("；")}。聚焦图表后可使用左右方向键逐点查看。`;
});

function x(index: number) {
  return padding.left + (pointCount.value <= 1 ? 0 : (index / (pointCount.value - 1)) * plotWidth.value);
}

function y(value: number) {
  return padding.top + plotHeight - (Math.max(0, value) / maximum.value) * plotHeight;
}

function describePoint(index: number) {
  const time = shortTime(props.labels[index]) || `第 ${index + 1} 个时间点`;
  const values = props.series.flatMap((item) => {
    const value = item.values[index];
    return Number.isFinite(value) ? [`${item.name} ${props.valueFormatter(value)}`] : [];
  });
  return `${time}，${values.length ? values.join("，") : "暂无有效数据"}`;
}

function selectIndex(index: number, announce = false) {
  if (pointCount.value === 0) return;
  activeIndex.value = Math.max(0, Math.min(pointCount.value - 1, index));
  if (announce) announcement.value = describePoint(activeIndex.value);
}

function move(event: MouseEvent | PointerEvent) {
  if (pointCount.value === 0) return;
  const bounds = (event.currentTarget as SVGElement).getBoundingClientRect();
  const relative = ((event.clientX - bounds.left) / bounds.width) * chartWidth.value;
  const ratio = Math.max(0, Math.min(1, (relative - padding.left) / plotWidth.value));
  selectIndex(Math.round(ratio * (pointCount.value - 1)));
}

function syncCanvasWidth() {
  const measured = chartCanvas.value?.getBoundingClientRect().width;
  if (measured && Number.isFinite(measured)) chartWidth.value = Math.max(280, Math.round(measured));
}

function focusChart() {
  chartFocused.value = true;
  selectIndex(activeIndex.value ?? 0, true);
}

function blurChart() {
  chartFocused.value = false;
  activeIndex.value = null;
}

function clearPointer() {
  if (!chartFocused.value) activeIndex.value = null;
}

function handleKeydown(event: KeyboardEvent) {
  const current = activeIndex.value ?? 0;
  let next: number | null = null;
  if (event.key === "ArrowLeft") next = current - 1;
  if (event.key === "ArrowRight") next = current + 1;
  if (event.key === "Home") next = 0;
  if (event.key === "End") next = pointCount.value - 1;
  if (next === null) return;
  event.preventDefault();
  selectIndex(next, true);
}

function shortTime(value: string | undefined) {
  if (!value) return "";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
}

onMounted(() => {
  window.addEventListener("resize", syncCanvasWidth, { passive: true });
  if (typeof ResizeObserver !== "undefined" && chartRoot.value) {
    resizeObserver = new ResizeObserver(syncCanvasWidth);
    resizeObserver.observe(chartRoot.value);
  }
  void nextTick(syncCanvasWidth);
});

onBeforeUnmount(() => {
  resizeObserver?.disconnect();
  window.removeEventListener("resize", syncCanvasWidth);
});

watch(pointCount, () => void nextTick(syncCanvasWidth));
</script>

<template>
  <div ref="chartRoot" class="metric-chart" @mouseleave="clearPointer">
    <div v-if="pointCount === 0" class="metric-chart__empty">{{ emptyLabel }}</div>
    <svg
      v-else
      ref="chartCanvas"
      class="metric-chart__canvas"
      :viewBox="`0 0 ${chartWidth} ${height}`"
      role="img"
      tabindex="0"
      focusable="true"
      :aria-label="chartAriaLabel"
      :aria-describedby="summaryId"
      @focus="focusChart"
      @blur="blurChart"
      @keydown="handleKeydown"
      @pointerdown="move"
      @pointermove="move"
    >
      <g v-for="tick in ticks" :key="tick.ratio">
        <line :x1="padding.left" :x2="chartWidth - padding.right" :y1="tick.y" :y2="tick.y" class="metric-chart__grid" />
        <text :x="padding.left - 7" :y="tick.y + 4" text-anchor="end" class="metric-chart__axis-label">
          {{ valueFormatter(tick.value) }}
        </text>
      </g>
<defs v-if="firstArea">
  <linearGradient :id="areaGradientId" x1="0" y1="0" x2="0" y2="1">
    <stop offset="0%" :style="{ stopColor: firstArea.color, stopOpacity: 0.22 }" />
    <stop offset="100%" :style="{ stopColor: firstArea.color, stopOpacity: 0 }" />
  </linearGradient>
</defs>
<path v-if="firstArea" :d="firstArea.d" :fill="`url(#${areaGradientId})`" />
      <polyline
        v-for="line in lines"
        :key="line.name"
        :points="line.points"
        :stroke="line.color"
        pathLength="1"
        class="metric-chart__line"
      />
      <g v-if="activeIndex !== null">
        <line :x1="x(activeIndex)" :x2="x(activeIndex)" :y1="padding.top" :y2="padding.top + plotHeight" class="metric-chart__cursor" />
        <circle
          v-for="line in lines"
          :key="line.name"
          :cx="x(activeIndex)"
          :cy="y(line.values[activeIndex] ?? 0)"
          r="3.5"
          :fill="line.color"
          class="metric-chart__point"
        />
      </g>
      <text :x="padding.left" :y="height - 7" class="metric-chart__time">{{ shortTime(labels[0]) }}</text>
      <text :x="chartWidth - padding.right" :y="height - 7" text-anchor="end" class="metric-chart__time">
        {{ shortTime(labels[labels.length - 1]) }}
      </text>
    </svg>
    <div v-if="activeIndex !== null" class="metric-chart__tooltip">
      <strong>{{ shortTime(labels[activeIndex]) }}</strong>
      <span v-for="line in lines" :key="line.name">
        <i :style="{ background: line.color }" />{{ line.name }} {{ valueFormatter(line.values[activeIndex] ?? 0) }}
      </span>
    </div>
    <p :id="summaryId" class="sr-only">{{ chartSummary }}</p>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
  </div>
</template>
