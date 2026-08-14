<script setup lang="ts">
import { Waypoints } from "@lucide/vue";

defineProps<{ compact?: boolean }>();
</script>

<template>
  <div class="brand-mark" :class="{ 'brand-mark--compact': compact }" aria-label="PolyFleet">
    <span class="brand-mark__symbol" aria-hidden="true">
      <waypoints :size="20" :stroke-width="2.2" />
    </span>
    <span class="brand-mark__name">PolyFleet</span>
  </div>
</template>
