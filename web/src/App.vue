<script setup lang="ts">
import { computed } from "vue";
import { darkTheme, NConfigProvider, NDialogProvider, NGlobalStyle, NMessageProvider } from "naive-ui";
import AppController from "./AppController.vue";
import { colorMode } from "./color-mode";
import { darkThemeOverrides, eyeThemeOverrides, lightThemeOverrides } from "./theme";

const naiveTheme = computed(() => (colorMode.value === "dark" ? darkTheme : null));
const activeThemeOverrides = computed(() =>
  colorMode.value === "dark" ? darkThemeOverrides : colorMode.value === "eye" ? eyeThemeOverrides : lightThemeOverrides,
);
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="activeThemeOverrides">
    <n-global-style />
    <n-dialog-provider>
      <n-message-provider placement="top-right">
        <app-controller />
      </n-message-provider>
    </n-dialog-provider>
  </n-config-provider>
</template>
