<script setup lang="ts">
import { ref } from "vue";
import { NPopover } from "naive-ui";
import type { UserAssignment } from "../types";
defineProps<{ assignments: UserAssignment[] }>();
const hovered = ref(false);
const focused = ref(false);
</script>

<template>
  <div v-if="assignments.length" class="assignment-inline" @click.stop @keydown.stop>
    <span class="chip" :title="assignments[0].node_name">{{ assignments[0].node_name }}</span>
    <n-popover v-if="assignments.length > 1" :show="hovered || focused" placement="bottom-start" :style="{ maxWidth: 'min(360px, 90vw)' }">
      <template #trigger>
        <button type="button" class="chip chip--more" :aria-label="`查看全部 ${assignments.length} 个节点`" :aria-expanded="hovered || focused" @mouseenter="hovered = true" @mouseleave="hovered = false" @focus="focused = true" @blur="focused = false" @keydown.esc="hovered = focused = false">+{{ assignments.length - 1 }}</button>
      </template>
      <ul class="assignment-popover"><li v-for="assignment in assignments" :key="assignment.id">{{ assignment.node_name }}</li></ul>
    </n-popover>
  </div>
  <span v-else class="table-muted">未分配</span>
</template>
