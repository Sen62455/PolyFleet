<script setup lang="ts">
import HintTooltip from "../../components/HintTooltip.vue";
import { Copy, KeyRound } from "@lucide/vue";
import { NButton, NIcon, NModal, useMessage } from "naive-ui";
import type { UserCredential } from "../../types";

defineProps<{
  show: boolean;
  title: string;
  credentials: UserCredential[];
}>();
const emit = defineEmits<{ "update:show": [show: boolean] }>();
const message = useMessage();

async function copyCredential(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    message.success("凭据已复制");
  } catch {
    message.error("复制失败，请手动选择凭据");
  }
}
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    :title="title"
    class="credential-modal"
    :bordered="false"
    @update:show="emit('update:show', $event)"
  >
    <div class="credential-notice">
      <key-round :size="18" aria-hidden="true" />
      <span>每个节点使用独立凭据，请按节点配置客户端。</span>
    </div>
    <div class="credential-list">
      <section v-for="item in credentials" :key="item.node_id" class="credential-item">
        <header>
          <div>
            <strong>{{ item.node_name }}</strong>
            <small>{{ item.credential_fingerprint }}</small>
          </div>
          <hint-tooltip trigger="hover">
            <template #trigger>
              <n-button circle quaternary :aria-label="`复制 ${item.node_name} 凭据`" @click="copyCredential(item.credential)">
                <template #icon><n-icon><copy /></n-icon></template>
              </n-button>
            </template>
            复制凭据
          </hint-tooltip>
        </header>
        <code>{{ item.credential }}</code>
      </section>
    </div>
    <template #footer>
      <div class="modal-actions">
        <n-button type="primary" @click="emit('update:show', false)">完成</n-button>
      </div>
    </template>
  </n-modal>
</template>
