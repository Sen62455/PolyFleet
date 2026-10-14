<script setup lang="ts">
defineProps<{
  label: string;
  tone?: 'accent' | 'signal' | 'pressure' | 'critical' | 'neutral' | 'upload';
  clickable?: boolean;
  muted?: boolean;
}>();
</script>

<template>
  <component
    :is="clickable ? 'button' : 'article'"
    class="stat-card"
    :class="[`stat-card--${tone ?? 'accent'}`, { 'stat-card--muted': muted }]"
    v-bind="clickable ? { type: 'button' } : {}"
  >
    <span class="stat-card__icon" aria-hidden="true"><slot name="icon" /></span>
    <div class="stat-card__body">
      <span class="stat-card__label">{{ label }}</span>
      <strong class="stat-card__value" :class="{ 'is-muted': muted }"><slot /></strong>
      <small v-if="$slots.hint" class="stat-card__hint"><slot name="hint" /></small>
      <div v-if="$slots.extra" class="stat-card__extra"><slot name="extra" /></div>
    </div>
  </component>
</template>
