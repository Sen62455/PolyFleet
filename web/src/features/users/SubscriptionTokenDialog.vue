<script setup lang="ts">
import HintTooltip from "../../components/HintTooltip.vue";
import { computed } from "vue";
import { Copy as CopyIcon, KeyRound } from "@lucide/vue";
import { NButton, NIcon, NModal, useMessage } from "naive-ui";
import type { IssuedSubscriptionToken } from "../../types";

const props = defineProps<{ show: boolean; issued: IssuedSubscriptionToken | null }>();
const emit = defineEmits<{ "update:show": [show: boolean] }>();
const message = useMessage();

const links = computed(() => {
  const result: Array<{ label: string; value: string }> = [];
  if (!props.issued) return result;
  const values: Array<[string, string | undefined]> = [
    ["HY2 URI", props.issued.urls.uri],
    ["Base64", props.issued.urls.base64],
    ["Clash Meta", props.issued.urls.clash],
    ["sing-box", props.issued.urls.sing_box],
  ];
  for (const [label, value] of values) {
    if (value) result.push({ label, value });
  }
  return result;
});

async function copyText(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value);
    message.success(`${label}已复制`);
  } catch {
    message.error("复制失败，请手动选择内容");
  }
}
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    :title="issued?.subscription.name || '订阅 Token'"
    class="subscription-token-modal"
    :bordered="false"
    @update:show="emit('update:show', $event)"
  >
    <div v-if="issued" class="credential-notice">
      <key-round :size="18" aria-hidden="true" />
      <span>完整 Token 与订阅地址仅显示本次；关闭后需通过轮换生成新地址。</span>
    </div>
    <div v-if="issued" class="subscription-secret subscription-ledger-row subscription-ledger-row--secret">
      <header>
        <strong>Token</strong>
        <hint-tooltip trigger="hover">
          <template #trigger>
            <n-button circle quaternary aria-label="复制订阅 Token" @click="copyText(issued.token, 'Token')">
              <template #icon><n-icon><copy-icon /></n-icon></template>
            </n-button>
          </template>
          复制 Token
        </hint-tooltip>
      </header>
      <code>{{ issued.token }}</code>
    </div>
    <div v-if="issued" class="subscription-link-list subscription-ledger-list">
      <section
        v-for="item in links"
        :key="item.label"
        class="subscription-link-item subscription-ledger-row subscription-ledger-row--link"
      >
        <header>
          <strong>{{ item.label }}</strong>
          <hint-tooltip trigger="hover">
            <template #trigger>
              <n-button circle quaternary :aria-label="`复制 ${item.label} 订阅地址`" @click="copyText(item.value, `${item.label} 地址`)">
                <template #icon><n-icon><copy-icon /></n-icon></template>
              </n-button>
            </template>
            复制地址
          </hint-tooltip>
        </header>
        <code>{{ item.value }}</code>
      </section>
    </div>
    <template #footer>
      <div class="modal-actions">
        <n-button type="primary" @click="emit('update:show', false)">完成</n-button>
      </div>
    </template>
  </n-modal>
</template>
