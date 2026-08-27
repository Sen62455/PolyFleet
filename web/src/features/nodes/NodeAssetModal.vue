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
    class="asset-modal"
    :title="`VPS 资产档案 · ${node?.name || ''}`"
    :mask-closable="!saving"
    @update:show="emit('update:show', $event)"
  >
    <n-form label-placement="top" @submit.prevent="submit">
      <div class="asset-modal__grid">
        <n-form-item label="套餐 / 规格">
          <n-input v-model:value="plan" maxlength="120" placeholder="例如 LAX.Pro.Pocket" />
        </n-form-item>
        <n-form-item label="续费周期（月）">
          <n-input-number v-model:value="renewalCycleMonths" :min="0" :max="120" />
        </n-form-item>
        <n-form-item label="购买日期">
          <n-date-picker v-model:value="purchasedAt" type="date" clearable />
        </n-form-item>
        <n-form-item label="到期日期">
          <n-date-picker v-model:value="expiresAt" type="date" clearable />
        </n-form-item>
      </div>
      <n-form-item label="自动续费">
        <n-switch v-model:value="autoRenew" />
      </n-form-item>
      <n-form-item label="资产备注">
        <n-input v-model:value="notes" type="textarea" :rows="3" maxlength="1000" placeholder="账单、续费或供应商备注" />
      </n-form-item>
      <div class="modal-actions">
        <n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" attr-type="submit" :loading="saving">保存资产档案</n-button>
      </div>
    </n-form>
  </n-modal>
</template>

<style scoped>
.asset-modal { width: min(650px, calc(100vw - 28px)); }
.asset-modal__grid { display: grid; grid-template-columns: 1.5fr 1fr; gap: 0 16px; }
@media (max-width: 620px) { .asset-modal__grid { grid-template-columns: 1fr; } }
</style>
