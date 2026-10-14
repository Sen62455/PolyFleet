<script setup lang="ts">
withDefaults(defineProps<{ type?: string; dot?: boolean }>(), { type: "default" });
const tones: Record<string, string> = { success: "signal", error: "critical", warning: "pressure", info: "accent", default: "neutral" };
</script>
<template>
  <span class="pill" :class="[`pill--${tones[type] ?? 'neutral'}`, { 'pill--dot': dot }]"><slot /></span>
</template>
