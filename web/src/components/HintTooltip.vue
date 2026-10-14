<script setup lang="ts">
import { ref } from "vue";
import { NTooltip } from "naive-ui";
defineOptions({ inheritAttrs: false });
withDefaults(defineProps<{ disabled?: boolean }>(), { disabled: false });
const hovered = ref(false);
const focused = ref(false);
</script>
<template>
  <n-tooltip v-bind="$attrs" role="tooltip" :disabled="disabled" :show="!disabled && (hovered || focused)">
    <template #trigger><span class="hint-tooltip-trigger" @mouseenter="hovered = true" @mouseleave="hovered = false" @focusin="focused = true" @focusout="focused = false" @keydown.esc="hovered = focused = false"><slot name="trigger" /></span></template>
    <slot />
  </n-tooltip>
</template>
