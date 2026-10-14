<script setup lang="ts">
import { CircleAlert, SearchX } from "@lucide/vue";
import { NButton, NSkeleton } from "naive-ui";
withDefaults(defineProps<{
  kind: "loading" | "empty" | "error";
  title?: string;
  hint?: string;
  actionLabel?: string;
  busy?: boolean;
  stats?: boolean;
}>(), { title: "", hint: "", actionLabel: "", stats: false, busy: false });
const emit = defineEmits<{ action: [] }>();
</script>

<template>
  <div class="surface-state" :class="`surface-state--${kind}`" :role="kind === 'error' ? 'alert' : 'status'" :aria-busy="kind === 'loading'">
    <template v-if="kind === 'loading'">
      <span class="sr-only">正在加载数据</span>
      <div v-if="stats" class="surface-state__stats" aria-hidden="true"><n-skeleton v-for="i in 4" :key="i" height="110px" :sharp="false" /></div>
      <div class="surface-state__rows" aria-hidden="true"><n-skeleton v-for="i in 4" :key="i" height="48px" :sharp="false" /></div>
    </template>
    <template v-else>
      <circle-alert v-if="kind === 'error'" :size="26" aria-hidden="true" /><search-x v-else :size="26" aria-hidden="true" />
      <strong>{{ title || (kind === 'error' ? '加载失败' : '没有匹配的结果') }}</strong>
      <small v-if="hint">{{ hint }}</small>
      <n-button v-if="actionLabel || kind === 'error'" size="small" secondary :loading="busy" @click="emit('action')">{{ actionLabel || '重试' }}</n-button>
    </template>
  </div>
</template>
