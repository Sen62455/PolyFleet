<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NSpin } from "naive-ui";
import { api, APIError } from "./api";
import AuthView from "./features/auth/AuthView.vue";
import NodeDashboard from "./features/nodes/NodeDashboard.vue";
import BrandMark from "./components/BrandMark.vue";
import type { Session } from "./types";

type Screen = "loading" | "setup" | "login" | "dashboard";

const screen = ref<Screen>("loading");
const session = ref<Session | null>(null);
const bootstrapConfigured = ref(false);
const submitting = ref(false);
const errorMessage = ref("");

function friendlyError(error: unknown): string {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      bootstrap_rejected: "初始化令牌不正确。",
      already_initialized: "控制台已经完成初始化，请直接登录。",
      invalid_credentials: "用户名或密码不正确。",
      rate_limited: "尝试次数过多，请稍后再试。",
      origin_rejected: "请求来源未通过安全校验。",
    };
    return messages[error.code] ?? `操作失败：${error.message}`;
  }
  return "无法连接 PolyFleet 服务，请检查服务状态后重试。";
}

async function initialize() {
  screen.value = "loading";
  errorMessage.value = "";
  try {
    const setup = await api.setupStatus();
    bootstrapConfigured.value = setup.bootstrap_token_configured;
    if (setup.setup_required) {
      screen.value = "setup";
      return;
    }
    try {
      session.value = await api.session();
      screen.value = "dashboard";
    } catch (error) {
      if (error instanceof APIError && error.status === 401) {
        screen.value = "login";
        return;
      }
      throw error;
    }
  } catch (error) {
    errorMessage.value = friendlyError(error);
    screen.value = "login";
  }
}

async function submitAuth(payload: { username: string; password: string; bootstrapToken?: string }) {
  submitting.value = true;
  errorMessage.value = "";
  try {
    if (screen.value === "setup") {
      session.value = await api.bootstrap({
        bootstrap_token: payload.bootstrapToken ?? "",
        username: payload.username,
        password: payload.password,
      });
    } else {
      session.value = await api.login({ username: payload.username, password: payload.password });
    }
    screen.value = "dashboard";
  } catch (error) {
    errorMessage.value = friendlyError(error);
    if (error instanceof APIError && error.code === "already_initialized") {
      screen.value = "login";
    }
  } finally {
    submitting.value = false;
  }
}

async function logout() {
  try {
    await api.logout();
  } finally {
    session.value = null;
    screen.value = "login";
  }
}

function sessionExpired() {
  session.value = null;
  screen.value = "login";
  errorMessage.value = "登录已过期，请重新登录。";
}

onMounted(initialize);
</script>

<template>
  <main v-if="screen === 'loading'" class="boot-screen" aria-live="polite">
    <brand-mark />
    <n-spin :size="24" />
  </main>
  <auth-view
    v-else-if="screen === 'setup' || screen === 'login'"
    :mode="screen"
    :bootstrap-configured="bootstrapConfigured"
    :loading="submitting"
    :error-message="errorMessage"
    @submit="submitAuth"
    @retry="initialize"
  />
  <node-dashboard
    v-else-if="session"
    :session="session"
    @logout="logout"
    @session-expired="sessionExpired"
  />
</template>
