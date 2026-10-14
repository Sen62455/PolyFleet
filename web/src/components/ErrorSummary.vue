<script setup lang="ts">
import { computed, ref } from "vue";
import { NTooltip } from "naive-ui";
import { operationErrorText } from "../lib/format";
const props = defineProps<{ code?: string; message?: string }>();
const hovered = ref(false);
const focused = ref(false);
const raw = computed(() => [props.code, props.message].filter(Boolean).join(" · "));
</script>

<template>
  <n-tooltip role="tooltip" :show="hovered || focused" placement="top" :style="{ maxWidth: 'min(480px, 90vw)', overflowWrap: 'anywhere' }">
    <template #trigger>
      <span class="err-summary" tabindex="0" :title="raw" @mouseenter="hovered = true" @mouseleave="hovered = false" @focus="focused = true" @blur="focused = false" @keydown.esc="hovered = focused = false">
        <code v-if="code" class="err-code">{{ code }}</code>
        <span class="err-text">{{ operationErrorText(code, message) }}</span>
      </span>
    </template>
    {{ raw }}
  </n-tooltip>
</template>
