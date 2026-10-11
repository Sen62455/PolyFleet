<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import {
  NButton,
  NDatePicker,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch,
  type FormInst,
  type FormRules,
} from "naive-ui";
import type { NodeRecord, UserInput, UserRecord } from "../../types";
import { adapterLabels } from "../../lib/format";

const props = defineProps<{
  show: boolean;
  user: UserRecord | null;
  assignableNodes: NodeRecord[];
  saving: boolean;
}>();

const emit = defineEmits<{
  "update:show": [show: boolean];
  submit: [input: UserInput];
}>();

interface UserFormModel {
  username: string;
  displayName: string;
  notes: string;
  enabled: boolean;
  expiresAt: number | null;
  nodeIds: string[];
  trafficLimitGiB: number;
}

const formRef = ref<FormInst | null>(null);
const form = reactive<UserFormModel>({
  username: "",
  displayName: "",
  notes: "",
  enabled: true,
  expiresAt: null,
  nodeIds: [],
  trafficLimitGiB: 0,
});

const title = computed(() => (props.user ? "编辑用户" : "添加用户"));
const nodeOptions = computed(() =>
  props.assignableNodes.map((node) => ({
    label: `${node.name} · ${adapterLabels[node.adapter_type]}`,
    value: node.id,
    disabled: !node.enabled,
  })),
);
const rules: FormRules = {
  username: [
    { required: true, message: "请输入用户名", trigger: "blur" },
    {
      pattern: /^[A-Za-z0-9._-]{3,32}$/,
      message: "使用 3 到 32 位字母、数字、点、下划线或连字符",
      trigger: ["blur", "input"],
    },
  ],
};

watch(
  () => [props.show, props.user] as const,
  ([show, user]) => {
    if (!show) return;
    form.username = user?.username ?? "";
    form.displayName = user?.display_name ?? "";
    form.notes = user?.notes ?? "";
    form.enabled = user?.enabled ?? true;
    form.expiresAt = user?.expires_at ? new Date(user.expires_at).getTime() : null;
    form.nodeIds = [];
    form.trafficLimitGiB = (user?.traffic_limit_bytes ?? 0) / 1024 ** 3;
    formRef.value?.restoreValidation();
  },
  { immediate: true },
);

async function submit() {
  try {
    await formRef.value?.validate();
  } catch {
    return;
  }
  emit("submit", {
    username: form.username.trim(),
    display_name: form.displayName.trim(),
    notes: form.notes.trim(),
    enabled: form.enabled,
    expires_at: form.expiresAt === null ? null : new Date(form.expiresAt).toISOString(),
    traffic_limit_bytes: Math.round(form.trafficLimitGiB * 1024 ** 3),
    node_ids: props.user ? [] : form.nodeIds,
  });
}
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    :title="title"
    class="user-form-modal compact-dialog"
    :bordered="false"
    :mask-closable="!saving"
    :close-on-esc="!saving"
    :segmented="{ content: true, footer: true }"
    :style="{ width: 'min(640px, calc(100vw - 32px))' }"
    content-style="max-height: min(58vh, 420px); overflow-y: auto;"
    @update:show="emit('update:show', $event)"
  >
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="top" class="compact-form">
      <div class="form-grid">
        <n-form-item label="用户名" path="username">
          <n-input v-model:value="form.username" maxlength="32" placeholder="例如：alice" autofocus />
        </n-form-item>
        <n-form-item label="显示名称" path="displayName">
          <n-input v-model:value="form.displayName" maxlength="64" placeholder="可选" />
        </n-form-item>
      </div>
      <div class="form-grid">
        <n-form-item label="到期时间" path="expiresAt">
          <n-date-picker
            v-model:value="form.expiresAt"
            type="datetime"
            clearable
            style="width: 100%"
            placeholder="永不过期"
          />
        </n-form-item>
        <n-form-item label="全局流量额度（GiB）" path="trafficLimitGiB">
          <n-input-number
            v-model:value="form.trafficLimitGiB"
            :min="0"
            :max="8388607"
            :precision="2"
            :step="10"
            style="width: 100%"
            placeholder="0（不限额）"
          />
        </n-form-item>
      </div>
      <n-form-item v-if="!user" label="初始节点" path="nodeIds">
        <n-select
          v-model:value="form.nodeIds"
          multiple
          clearable
          :options="nodeOptions"
          placeholder="可稍后分配"
        />
        <template v-if="assignableNodes.length === 0" #feedback>
          当前没有可分配的受管节点。
        </template>
      </n-form-item>
      <n-form-item label="备注" path="notes">
        <n-input
          v-model:value="form.notes"
          type="textarea"
          :autosize="{ minRows: 2, maxRows: 4 }"
          maxlength="500"
          show-count
          placeholder="可选"
        />
      </n-form-item>
      <div class="switch-row">
        <div>
          <strong>启用用户</strong>
          <span>停用会在已分配节点上拒绝新的鉴权</span>
        </div>
        <n-switch v-model:value="form.enabled" aria-label="启用用户" />
      </div>
    </n-form>
    <template #footer>
      <div class="modal-actions">
        <n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="saving" @click="submit">
          {{ user ? "保存" : "添加" }}
        </n-button>
      </div>
    </template>
  </n-modal>
</template>
