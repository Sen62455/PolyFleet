<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { CalendarClock, Gauge, KeyRound, RefreshCw, Search as SearchIcon, ShieldOff, UsersRound } from "@lucide/vue";
import {
  NButton,
  NDatePicker,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSpin,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import { api, APIError } from "../../api";
import { formatBytes, relativeTime } from "../../lib/format";
import type { SubscriptionOperationRecord, SubscriptionOperationStatus } from "../../types";

const emit = defineEmits<{ "session-expired": [] }>();
const message = useMessage();
const dialog = useDialog();
const subscriptions = ref<SubscriptionOperationRecord[]>([]);
const total = ref(0);
const loading = ref(true);
const refreshing = ref(false);
const search = ref("");
const status = ref<"all" | SubscriptionOperationStatus>("all");
const offset = ref(0);
const pageSize = 50;
const editing = ref<SubscriptionOperationRecord | null>(null);
const saving = ref(false);
const tokenExpiresAt = ref<number | null>(null);
const userExpiresAt = ref<number | null>(null);
const quotaGiB = ref<number | null>(null);

const filters: Array<{ value: "all" | SubscriptionOperationStatus; label: string }> = [
  { value: "all", label: "全部" },
  { value: "active", label: "活跃" },
  { value: "expiring", label: "即将到期" },
  { value: "exhausted", label: "额度用尽" },
  { value: "expired", label: "已到期" },
  { value: "revoked", label: "已吊销" },
];

const activeCount = computed(() => subscriptions.value.filter((item) => item.status === "active").length);
const attentionCount = computed(() => subscriptions.value.filter((item) => ["expiring", "exhausted", "expired"].includes(item.status)).length);
const usedBytes = computed(() => subscriptions.value.reduce((sum, item) => sum + item.traffic_used_bytes, 0));
const limitedBytes = computed(() => subscriptions.value.reduce((sum, item) => sum + item.traffic_limit_bytes, 0));

function handleError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return;
  }
  message.error(error instanceof APIError ? error.message : fallback);
}

async function load(silent = false) {
  if (refreshing.value) return;
  if (!silent) loading.value = subscriptions.value.length === 0;
  refreshing.value = true;
  try {
    const page = await api.listSubscriptionOperations({
      status: status.value,
      search: search.value.trim(),
      limit: pageSize,
      offset: offset.value,
    });
    subscriptions.value = page.subscriptions;
    total.value = page.total;
  } catch (error) {
    handleError(error, "订阅运营数据加载失败。");
  } finally {
    loading.value = false;
    refreshing.value = false;
  }
}

function setFilter(next: typeof status.value) {
  status.value = next;
  offset.value = 0;
  void load();
}

function openEdit(item: SubscriptionOperationRecord) {
  editing.value = item;
  tokenExpiresAt.value = item.token_expires_at ? new Date(item.token_expires_at).getTime() : null;
  userExpiresAt.value = item.user_expires_at ? new Date(item.user_expires_at).getTime() : null;
  quotaGiB.value = item.traffic_limit_bytes ? item.traffic_limit_bytes / (1024 ** 3) : 0;
}

function extend(days: number) {
  const baseline = Math.max(Date.now(), userExpiresAt.value ?? 0, tokenExpiresAt.value ?? 0);
  const next = baseline + days * 24 * 60 * 60 * 1000;
  userExpiresAt.value = next;
  tokenExpiresAt.value = next;
}

async function save() {
  if (!editing.value) return;
  saving.value = true;
  try {
    await api.updateSubscriptionOperation(editing.value.token_id, {
      token_expires_at: tokenExpiresAt.value ? new Date(tokenExpiresAt.value).toISOString() : null,
      user_expires_at: userExpiresAt.value ? new Date(userExpiresAt.value).toISOString() : null,
      traffic_limit_bytes: Math.round((quotaGiB.value ?? 0) * 1024 ** 3),
    });
    editing.value = null;
    await load(true);
    message.success("订阅额度与到期策略已更新");
  } catch (error) {
    handleError(error, "订阅更新失败。");
  } finally {
    saving.value = false;
  }
}

function revoke(item: SubscriptionOperationRecord) {
  dialog.warning({
    title: "吊销订阅",
    content: `确认吊销 ${item.display_name || item.username} 的“${item.name}”？现有订阅地址将立即失效。`,
    positiveText: "吊销",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.updateSubscriptionOperation(item.token_id, { revoke: true });
        await load(true);
        message.success("订阅已吊销");
      } catch (error) {
        handleError(error, "订阅吊销失败。");
        return false;
      }
      return true;
    },
  });
}

function quotaPercent(item: SubscriptionOperationRecord) {
  if (!item.traffic_limit_bytes) return 0;
  return Math.min(100, Math.round((item.traffic_used_bytes / item.traffic_limit_bytes) * 100));
}

function expiry(item: SubscriptionOperationRecord) {
  const values = [item.token_expires_at, item.user_expires_at].filter(Boolean).map((value) => new Date(value!).getTime());
  if (!values.length) return "长期有效";
  return new Date(Math.min(...values)).toLocaleDateString("zh-CN");
}

function statusLabel(value: SubscriptionOperationStatus) {
  return ({ active: "活跃", expiring: "即将到期", exhausted: "额度用尽", expired: "已到期", revoked: "已吊销", disabled: "用户停用" })[value];
}

onMounted(() => load());
</script>

<template>
  <main class="workspace operations-workspace subscription-workspace">
    <div class="page-heading page-heading--console">
      <div><span class="page-eyebrow">SUBSCRIPTION OPERATIONS</span><h2>订阅运营</h2><p>额度、有效期和最近使用集中在一张运营总账中</p></div>
      <n-tooltip trigger="hover">
        <template #trigger><n-button circle secondary :loading="refreshing" aria-label="刷新订阅" @click="load()"><template #icon><n-icon><refresh-cw /></n-icon></template></n-button></template>
        刷新
      </n-tooltip>
    </div>

    <section class="ops-register" aria-label="订阅摘要">
      <div><span><key-round :size="15" />订阅总数</span><strong>{{ total }}</strong><small>当前筛选 {{ subscriptions.length }} 条</small></div>
      <div><span><users-round :size="15" />活跃订阅</span><strong>{{ activeCount }}</strong><small>本页可正常使用</small></div>
      <div><span><calendar-clock :size="15" />需处理</span><strong :class="{ 'is-warning': attentionCount }">{{ attentionCount }}</strong><small>临期、到期或额度用尽</small></div>
      <div><span><gauge :size="15" />双向用量</span><strong>{{ formatBytes(usedBytes) }}</strong><small>{{ limitedBytes ? `共 ${formatBytes(limitedBytes)} 配额` : "未设置配额" }}</small></div>
    </section>

    <section class="ops-toolbar">
      <n-input v-model:value="search" clearable placeholder="搜索用户、订阅名或 Token 前缀" @keyup.enter="offset = 0; load()">
        <template #prefix><n-icon><search-icon :size="15" /></n-icon></template>
      </n-input>
      <div class="ops-segments" role="tablist" aria-label="订阅状态">
        <button v-for="item in filters" :key="item.value" type="button" :class="{ 'is-active': status === item.value }" @click="setFilter(item.value)">{{ item.label }}</button>
      </div>
    </section>

    <section class="ops-surface" aria-label="订阅列表">
      <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
      <div v-else-if="subscriptions.length === 0" class="surface-state surface-state--empty"><key-round :size="26" /><strong>没有符合条件的订阅</strong></div>
      <div v-else class="ops-table-wrap">
        <table class="ops-table subscription-table">
          <thead><tr><th>订阅 / 用户</th><th>状态</th><th>双向流量</th><th>有效期</th><th>最近活动</th><th>节点</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="item in subscriptions" :key="item.token_id">
              <td><strong>{{ item.name || "默认订阅" }}</strong><small>{{ item.display_name || item.username }} · {{ item.token_prefix }}...</small></td>
              <td><span class="ops-status" :class="`is-${item.status}`">{{ statusLabel(item.status) }}</span><small>{{ item.allowed_formats.join(" / ") }}</small></td>
              <td class="subscription-table__traffic"><strong>{{ formatBytes(item.traffic_used_bytes) }}</strong><small>{{ item.traffic_limit_bytes ? `/ ${formatBytes(item.traffic_limit_bytes)} · ${quotaPercent(item)}%` : "不限量" }}</small><i><span :style="{ width: `${quotaPercent(item)}%` }" /></i></td>
              <td><strong>{{ expiry(item) }}</strong><small>Token / 用户取较早值</small></td>
              <td><strong>{{ relativeTime(item.last_used_at || item.last_traffic_at) }}</strong><small>{{ item.last_used_at ? "订阅已拉取" : "尚未拉取" }}</small></td>
              <td><strong>{{ item.online_nodes }} / {{ item.assignment_count }}</strong><small>在线 / 已分配</small></td>
              <td class="ops-table__actions"><n-button size="small" secondary :disabled="item.status === 'revoked'" @click="openEdit(item)">调整</n-button><n-tooltip trigger="hover"><template #trigger><n-button quaternary circle type="error" :disabled="item.status === 'revoked'" aria-label="吊销订阅" @click="revoke(item)"><template #icon><n-icon><shield-off /></n-icon></template></n-button></template>吊销</n-tooltip></td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer v-if="total > pageSize" class="ops-pagination"><span>{{ offset + 1 }} - {{ Math.min(offset + pageSize, total) }} / {{ total }}</span><n-button size="small" :disabled="offset === 0" @click="offset = Math.max(0, offset - pageSize); load()">上一页</n-button><n-button size="small" :disabled="offset + pageSize >= total" @click="offset += pageSize; load()">下一页</n-button></footer>
    </section>

    <n-modal
      :show="editing !== null"
      preset="card"
      class="subscription-edit-modal"
      :style="{ width: 'calc(100% - 32px)', maxWidth: '620px', maxHeight: 'min(620px, calc(100vh - 32px))' }"
      content-style="overflow-y: auto"
      title="调整订阅策略"
      :mask-closable="!saving"
      @update:show="!$event && (editing = null)"
    >
      <n-form label-placement="top" @submit.prevent="save">
        <div class="subscription-edit-modal__quick"><span>快速延期</span><n-button size="tiny" secondary @click="extend(30)">+30 天</n-button><n-button size="tiny" secondary @click="extend(365)">+1 年</n-button></div>
        <div class="subscription-edit-modal__grid">
          <n-form-item label="Token 到期时间"><n-date-picker v-model:value="tokenExpiresAt" type="date" clearable /></n-form-item>
          <n-form-item label="用户到期时间"><n-date-picker v-model:value="userExpiresAt" type="date" clearable /></n-form-item>
        </div>
        <n-form-item label="用户双向流量额度（GiB，0 为不限量）"><n-input-number v-model:value="quotaGiB" :min="0" :max="8388607" /></n-form-item>
        <div class="modal-actions"><n-button :disabled="saving" @click="editing = null">取消</n-button><n-button type="primary" attr-type="submit" :loading="saving">保存</n-button></div>
      </n-form>
    </n-modal>
  </main>
</template>

<style scoped>
.subscription-workspace { display: grid; gap: 20px; }
.subscription-table th:first-child { width: 20%; }
.subscription-table td strong, .subscription-table td small { display: block; }
.subscription-table td > small { margin-top: 5px; color: var(--hf-muted); font-size: 10px; }
.subscription-table__traffic i { display: block; width: 120px; height: 2px; margin-top: 9px; background: var(--hf-line); }
.subscription-table__traffic i span { display: block; height: 100%; background: var(--hf-accent); }
.subscription-edit-modal__grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.subscription-edit-modal__quick { display: flex; align-items: center; gap: 8px; margin-bottom: 18px; color: var(--hf-muted); font-size: 11px; }
@media (max-width: 760px) { .subscription-edit-modal__grid { grid-template-columns: 1fr; gap: 0; } }
</style>
