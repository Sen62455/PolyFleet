<script setup lang="ts">
import HintTooltip from "../../components/HintTooltip.vue";
import { computed, ref, watch } from "vue";
import { Check, Copy as CopyIcon, KeyRound } from "@lucide/vue";
import { NAlert, NButton, NIcon, NModal, NSpin, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import type { EnrollmentToken, NodeRecord } from "../../types";

const props = defineProps<{ show: boolean; node: NodeRecord | null }>();
const emit = defineEmits<{ "update:show": [show: boolean]; "session-expired": [] }>();
const message = useMessage();
const loading = ref(false);
const errorMessage = ref("");
const token = ref<EnrollmentToken | null>(null);
const copied = ref<"token" | "config" | null>(null);

const serviceDefaults = {
  native_hysteria2: { core: "hysteria", unit: "hysteria-server.service" },
  sing_box_vless_reality: { core: "sing-box", unit: "hyfleet-sing-box-reality.service" },
  standalone_sing_box: { core: "sing-box", unit: "sing-box.service" },
  s_ui: { core: "sing-box", unit: "s-ui.service" },
};

const agentConfig = computed(() => {
  if (!props.node) return "";
  const defaults = serviceDefaults[props.node.adapter_type];
  return [
    `server_url: ${window.location.origin}`,
    `node_name: ${props.node.name}`,
    `adapter_type: ${props.node.adapter_type}`,
    `core_name: ${defaults.core}`,
    `service_unit: ${defaults.unit}`,
    "state_path: /var/lib/hyfleet/agent-state.json",
    "heartbeat_every: 15s",
    "desired_every: 10s",
  ].join("\n");
});

async function loadToken() {
  if (!props.node) return;
  loading.value = true;
  errorMessage.value = "";
  token.value = null;
  try {
    token.value = await api.createEnrollmentToken(props.node.id);
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    errorMessage.value = error instanceof APIError ? error.message : "注册令牌生成失败。";
  } finally {
    loading.value = false;
  }
}

async function copyText(value: string, target: "token" | "config") {
  try {
    await navigator.clipboard.writeText(value);
    copied.value = target;
    window.setTimeout(() => {
      if (copied.value === target) copied.value = null;
    }, 1800);
  } catch {
    message.error("复制失败，请手动选择文本。 ");
  }
}

function expiresAt(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value));
}

watch(
  () => props.show,
  (show) => {
    if (show) loadToken();
    else {
      token.value = null;
      copied.value = null;
    }
  },
);
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    title="注册 Agent"
    class="enrollment-modal"
    :bordered="false"
    :mask-closable="!loading"
    @update:show="emit('update:show', $event)"
  >
    <div v-if="loading" class="enrollment-loading"><n-spin :size="28" /></div>
    <n-alert v-else-if="errorMessage" type="error" :show-icon="false">
      <div class="alert-row">
        <span>{{ errorMessage }}</span>
        <n-button text type="error" @click="loadToken">重试</n-button>
      </div>
    </n-alert>
    <template v-else-if="token && node">
      <n-alert type="warning" :show-icon="false" class="enrollment-warning">
        此令牌仅显示一次，并在 {{ expiresAt(token.expires_at) }} 失效。生成新令牌会使旧令牌失效。
      </n-alert>

      <section class="code-section">
        <div class="code-section__header">
          <div><key-round :size="16" aria-hidden="true" /><strong>环境变量</strong></div>
          <hint-tooltip trigger="hover">
            <template #trigger>
              <n-button
                quaternary
                circle
                :aria-label="copied === 'token' ? '已复制令牌' : '复制令牌'"
                @click="copyText(`HYFLEET_ENROLLMENT_TOKEN=${token.enrollment_token}`, 'token')"
              >
                <template #icon><n-icon><check v-if="copied === 'token'" /><copy-icon v-else /></n-icon></template>
              </n-button>
            </template>
            {{ copied === "token" ? "已复制" : "复制" }}
          </hint-tooltip>
        </div>
        <pre>HYFLEET_ENROLLMENT_TOKEN={{ token.enrollment_token }}</pre>
      </section>

      <section class="code-section">
        <div class="code-section__header">
          <strong>agent.yaml</strong>
          <hint-tooltip trigger="hover">
            <template #trigger>
              <n-button
                quaternary
                circle
                :aria-label="copied === 'config' ? '已复制配置' : '复制配置'"
                @click="copyText(agentConfig, 'config')"
              >
                <template #icon><n-icon><check v-if="copied === 'config'" /><copy-icon v-else /></n-icon></template>
              </n-button>
            </template>
            {{ copied === "config" ? "已复制" : "复制" }}
          </hint-tooltip>
        </div>
        <pre>{{ agentConfig }}</pre>
      </section>
    </template>
    <template #footer>
      <div class="modal-actions">
        <n-button @click="emit('update:show', false)">关闭</n-button>
      </div>
    </template>
  </n-modal>
</template>
