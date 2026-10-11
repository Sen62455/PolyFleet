<script setup lang="ts">
import { ref, watch } from "vue";
import {
  NButton,
  NDatePicker,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSwitch,
} from "naive-ui";
import type { NodeAssetInput, NodeAssetRecord, NodeRecord } from "../../types";

const props = defineProps<{
  show: boolean;
  node: NodeRecord | null;
  asset: NodeAssetRecord | null;
  saving: boolean;
}>();
const emit = defineEmits<{
  "update:show": [show: boolean];
  submit: [input: NodeAssetInput];
}>();

const plan = ref("");
const purchasedAt = ref<number | null>(null);
const expiresAt = ref<number | null>(null);
const renewalCycleMonths = ref(12);
const autoRenew = ref(false);
const notes = ref("");

watch(() => props.show, (show) => {
  if (!show) return;
  plan.value = props.asset?.plan ?? "";
  purchasedAt.value = props.asset?.purchased_at ? new Date(props.asset.purchased_at).getTime() : null;
  expiresAt.value = props.asset?.expires_at ? new Date(props.asset.expires_at).getTime() : null;
  renewalCycleMonths.value = props.asset?.renewal_cycle_months || 12;
  autoRenew.value = props.asset?.auto_renew ?? false;
  notes.value = props.asset?.notes ?? "";
});

function iso(value: number | null) {
  return value === null ? null : new Date(value).toISOString();
}

function submit() {
  emit("submit", {
    plan: plan.value.trim(),
    purchased_at: iso(purchasedAt.value),
    expires_at: iso(expiresAt.value),
    renewal_cycle_months: renewalCycleMonths.value,
    auto_renew: autoRenew.value,
    notes: notes.value.trim(),
  });
}
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    class="node-asset-modal compact-dialog"
    :title="`VPS 资产档案 · ${node?.name || ''}`"
    :bordered="false"
    :mask-closable="!saving"
    :segmented="{ content: true, footer: true }"
    :style="{ width: 'min(720px, calc(100vw - 40px))' }"
    @update:show="emit('update:show', $event)"
  >
    <n-form class="compact-form asset-form" label-placement="top" @submit.prevent="submit">
      <div class="asset-form__layout">
        <section>
          <n-form-item label="套餐 / 规格">
            <n-input v-model:value="plan" maxlength="120" placeholder="例如 LAX.Pro.Pocket" />
          </n-form-item>
          <div class="form-grid">
            <n-form-item label="购买日期">
              <n-date-picker v-model:value="purchasedAt" type="date" clearable style="width: 100%" />
            </n-form-item>
            <n-form-item label="到期日期">
              <n-date-picker v-model:value="expiresAt" type="date" clearable style="width: 100%" />
            </n-form-item>
          </div>
          <n-form-item label="续费周期（月）">
            <n-input-number v-model:value="renewalCycleMonths" :min="0" :max="120" style="width: 100%" />
          </n-form-item>
          <div class="switch-row switch-row--compact">
            <div><strong>自动续费</strong></div>
            <n-switch v-model:value="autoRenew" aria-label="自动续费" />
          </div>
        </section>
        <section>
          <n-form-item label="资产备注">
            <n-input
              v-model:value="notes"
              type="textarea"
              :autosize="{ minRows: 12, maxRows: 16 }"
              maxlength="1000"
              show-count
              placeholder="套餐配置、账单号、续费备注等，可按行书写"
            />
          </n-form-item>
        </section>
      </div>
    </n-form>
    <template #footer>
      <div class="modal-actions">
        <n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" attr-type="submit" :loading="saving" @click="submit">保存资产档案</n-button>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.asset-form__layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.05fr);
  gap: 4px 24px;
}

.asset-form__layout :deep(.n-input--textarea) {
  min-height: 240px;
}

.asset-form__layout :deep(textarea) {
  min-height: 240px;
  line-height: 1.55;
}

@media (max-width: 640px) {
  .asset-form__layout {
    grid-template-columns: 1fr;
  }
}
</style>
