<script setup lang="ts">
import { computed, ref } from "vue";
import { Check, Leaf, Monitor, Moon, Sun } from "@lucide/vue";
import { NButton, NIcon, NPopover } from "naive-ui";
import {
  colorModePreference,
  setColorMode,
  type ColorModePreference,
} from "../color-mode";

const open = ref(false);
const options: Array<{ value: ColorModePreference; label: string; description: string }> = [
  { value: "system", label: "跟随系统", description: "随设备外观自动切换" },
  { value: "light", label: "明亮", description: "灰白高对比界面" },
  { value: "dark", label: "深色", description: "蓝灰低光控制台" },
  { value: "eye", label: "护眼", description: "柔和灰绿低对比界面" },
];
const activeOption = computed(() =>
  options.find((item) => item.value === colorModePreference.value) ?? options[0],
);

function select(preference: ColorModePreference) {
  setColorMode(preference);
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
  open.value = false;
}
</script>

<template>
  <n-popover
    v-model:show="open"
    trigger="click"
    placement="bottom-end"
    :show-arrow="false"
    class="theme-popover"
  >
    <template #trigger>
      <n-button
        quaternary
        circle
        class="theme-toggle"
        :aria-label="`显示模式：${activeOption.label}`"
        :aria-expanded="open"
      >
        <template #icon>
          <n-icon>
            <monitor v-if="colorModePreference === 'system'" />
            <sun v-else-if="colorModePreference === 'light'" />
            <moon v-else-if="colorModePreference === 'dark'" />
            <leaf v-else />
          </n-icon>
        </template>
      </n-button>
    </template>
    <div class="theme-menu" role="group" aria-label="显示模式">
      <header>
        <strong>显示模式</strong>
        <span>{{ activeOption.label }}</span>
      </header>
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        :aria-pressed="colorModePreference === option.value"
        :class="{ 'is-active': colorModePreference === option.value }"
        @click="select(option.value)"
      >
        <span><strong>{{ option.label }}</strong><small>{{ option.description }}</small></span>
        <check v-if="colorModePreference === option.value" :size="15" aria-hidden="true" />
      </button>
    </div>
  </n-popover>
</template>
