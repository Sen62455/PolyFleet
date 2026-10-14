<script setup lang="ts">
import { ref } from "vue";
import { NButton, NTooltip } from "naive-ui";
defineOptions({ inheritAttrs: false });
withDefaults(defineProps<{ label: string; tone?: "neutral" | "danger"; disabled?: boolean; loading?: boolean }>(), { tone: "neutral" });
const hovered = ref(false);
const focused = ref(false);
</script>

<template>
  <n-tooltip trigger="manual" role="tooltip" :show="hovered || focused">
    <template #trigger>
      <span class="icon-action-wrap" @mouseenter="hovered = true" @mouseleave="hovered = false" @focusin="focused = true" @focusout="focused = false" @keydown.esc="hovered = focused = false">
        <n-button v-bind="$attrs" class="icon-action" :class="`icon-action--${tone}`" quaternary circle size="small" :aria-label="label" :disabled="disabled" :loading="loading">
          <template #icon><slot /></template>
        </n-button>
      </span>
    </template>
    {{ label }}
  </n-tooltip>
</template>
