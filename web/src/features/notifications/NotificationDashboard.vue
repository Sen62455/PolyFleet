<script setup lang="ts">
import SurfaceState from "../../components/SurfaceState.vue";
import { computed, onMounted, ref } from "vue";
import { AlarmClock, BellRing, Bot, CalendarClock, CheckCircle2, FlaskConical, Gauge, Pencil, Play, Plus, RefreshCw, Send, Trash2, Webhook } from "@lucide/vue";
import {
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpin,
  NSwitch,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import IconAction from "../../components/IconAction.vue";
import { api, APIError } from "../../api";
import StatCard from "../../components/StatCard.vue";
import { relativeTime } from "../../lib/format";
import type { NodeRecord, NotificationEvent, NotificationKind, NotificationNotifierRecord, NotificationReminderKind, NotificationReminderRuleRecord, NotificationSettings } from "../../types";

const emit = defineEmits<{ "session-expired": [] }>();
const message = useMessage();
const dialog = useDialog();
const settings = ref<NotificationSettings>({ notifiers: [], deliveries: [], reminder_rules: [], telegram_bots: [] });
const nodes = ref<NodeRecord[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
const loaded = ref(false);
const working = ref("");
const modalOpen = ref(false);
const saving = ref(false);
const editingID = ref("");
const name = ref("");
const kind = ref<NotificationKind>("telegram");
const enabled = ref(true);
const events = ref<NotificationEvent[]>(["created", "resolved"]);
const url = ref("");
const botToken = ref("");
const chatID = ref("");
const reminderModalOpen = ref(false);
const reminderEditingID = ref("");
const reminderName = ref("");
const reminderNotifierID = ref("");
const reminderKind = ref<NotificationReminderKind>("fleet_summary");
const reminderEnabled = ref(true);
const reminderInterval = ref(360);
const reminderLeadDays = ref(30);
const reminderThreshold = ref(80);
const reminderNodeIDs = ref<string[]>([]);
const reminderMessageTemplate = ref("");
const telegramUpdatesTemplate = "https://api.telegram.org/bot<BotToken>/getUpdates";
const reminderMessageError = computed(() => {
  const remaining = reminderMessageTemplate.value.replaceAll("{{details}}", "").replaceAll("{{time}}", "");
  return remaining.includes("{{") || remaining.includes("}}") ? "仅支持 {{details}} 和 {{time}} 占位符。" : "";
});
const reminderMessageHeader = computed(() => `规则名称：${reminderName.value.trim().replace(/\s+/g, " ") || "（待填写）"}\n提醒类型：${reminderKindLabel(reminderKind.value)}\n提醒频率：${intervalLabel(reminderInterval.value)}`);
const reminderMessagePreview = computed(() => {
  const details = `［发送时自动生成：${reminderKindLabel(reminderKind.value)}的实时数据］`;
  const body = reminderMessageTemplate.value.trim()
    ? reminderMessageTemplate.value.trim().replaceAll("{{details}}", details).replaceAll("{{time}}", "［发送时间，UTC］")
    : details;
  return `${reminderMessageHeader.value}\n\n${body}`;
});
const telegramChatIDPattern = /^(?:-?\d+|@[A-Za-z][A-Za-z0-9_]{4,31})$/;

const delivered = computed(() => settings.value.deliveries.filter((item) => item.status === "delivered").length);
const pending = computed(() => settings.value.deliveries.filter((item) => item.status === "queued" || item.status === "retry").length);
const failed = computed(() => settings.value.deliveries.filter((item) => item.status === "failed").length);
const activeReminderCount = computed(() => settings.value.reminder_rules.filter((item) => item.enabled).length);
const kindOptions = [
  { label: "Telegram", value: "telegram" },
  { label: "Slack Incoming Webhook", value: "slack" },
  { label: "自定义 Webhook", value: "webhook" },
];
const reminderKindOptions = [
  { label: "周期运行概览", value: "fleet_summary" },
  { label: "活动告警重复提醒", value: "active_alerts" },
  { label: "VPS 到期预警", value: "asset_expiry" },
  { label: "节点流量阈值", value: "traffic_usage" },
];
const intervalOptions = [
  { label: "每 15 分钟", value: 15 },
  { label: "每 30 分钟", value: 30 },
  { label: "每 1 小时", value: 60 },
  { label: "每 3 小时", value: 180 },
  { label: "每 6 小时", value: 360 },
  { label: "每 12 小时", value: 720 },
  { label: "每天", value: 1440 },
  { label: "每周", value: 10080 },
];
const notifierOptions = computed(() => settings.value.notifiers.map((item) => ({ label: item.name, value: item.id })));
const nodeOptions = computed(() => nodes.value.map((item) => ({ label: item.name, value: item.id })));

const telegramChatIDError = computed(() => {
  const value = chatID.value.trim();
  if (!value) {
    return editingID.value ? "" : "请输入接收通知的 Chat ID。";
  }
  if (/^@[A-Za-z][A-Za-z0-9_]{2,29}bot$/i.test(value)) {
    return "这里应填写接收通知的用户、群组或频道，不能填写机器人的用户名。";
  }
  if (!telegramChatIDPattern.test(value)) {
    return "请填写数字 Chat ID，或公开频道的 @channel_username。";
  }
  return "";
});

function handleError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return;
  }
  message.error(error instanceof APIError ? error.message : fallback);
}

async function copyTelegramUpdatesURL() {
  const token = botToken.value.trim();
  if (token && !/^\d+:[A-Za-z0-9_-]+$/.test(token)) {
    message.warning("请先填写完整的 Bot Token，或清空后复制接口模板。");
    return;
  }
  try {
    await navigator.clipboard.writeText(token ? `https://api.telegram.org/bot${token}/getUpdates` : telegramUpdatesTemplate);
    message.success(token ? "接口地址已复制，包含 Bot Token，请勿转发" : "接口模板已复制，请替换 <BotToken>");
  } catch {
    message.error("复制失败，请手动复制下方接口模板。");
  }
}

async function load() {
  if (refreshing.value) return;
  refreshing.value = true;
  loadError.value = "";
  try {
    [settings.value, nodes.value] = await Promise.all([
      api.getNotificationSettings(),
      api.listNodes(),
    ]);
    loaded.value = true;
  } catch (error) {
    loadError.value = error instanceof APIError ? error.message : "通知设置加载失败。";
    if (error instanceof APIError && error.status === 401) emit("session-expired");
  } finally {
    loading.value = false;
    refreshing.value = false;
  }
}

function openReminderCreate() {
  reminderEditingID.value = "";
  reminderName.value = "";
  reminderNotifierID.value = settings.value.notifiers.find((item) => item.enabled)?.id ?? settings.value.notifiers[0]?.id ?? "";
  reminderKind.value = "fleet_summary";
  reminderEnabled.value = true;
  reminderInterval.value = 360;
  reminderLeadDays.value = 30;
  reminderThreshold.value = 80;
  reminderNodeIDs.value = [];
  reminderMessageTemplate.value = "";
  reminderModalOpen.value = true;
}

function openReminderEdit(rule: NotificationReminderRuleRecord) {
  reminderEditingID.value = rule.id;
  reminderName.value = rule.name;
  reminderNotifierID.value = rule.notifier_id;
  reminderKind.value = rule.kind;
  reminderEnabled.value = rule.enabled;
  reminderInterval.value = rule.interval_minutes;
  reminderLeadDays.value = rule.lead_days;
  reminderThreshold.value = rule.threshold_percent;
  reminderNodeIDs.value = [...rule.node_ids];
  reminderMessageTemplate.value = rule.message_template ?? "";
  reminderModalOpen.value = true;
}

async function saveReminder() {
  if (reminderMessageError.value) {
    message.warning(reminderMessageError.value);
    return;
  }
  if (!reminderName.value.trim() || !reminderNotifierID.value) {
    message.warning("请填写规则名称并选择通知通道");
    return;
  }
  saving.value = true;
  try {
    await api.saveNotificationReminderRule({
      ...(reminderEditingID.value ? { id: reminderEditingID.value } : {}),
      name: reminderName.value.trim(),
      notifier_id: reminderNotifierID.value,
      kind: reminderKind.value,
      enabled: reminderEnabled.value,
      interval_minutes: reminderInterval.value,
      lead_days: reminderLeadDays.value,
      threshold_percent: reminderThreshold.value,
      node_ids: reminderNodeIDs.value,
      message_template: reminderMessageTemplate.value.trim(),
    });
    reminderModalOpen.value = false;
    await load();
    message.success("提醒规则已保存");
  } catch (error) {
    handleError(error, "提醒规则保存失败。");
  } finally {
    saving.value = false;
  }
}

async function runReminder(rule: NotificationReminderRuleRecord) {
  working.value = `rule:${rule.id}`;
  try {
    const result = await api.runNotificationReminderRule(rule.id);
    await load();
    message.success(result.result);
  } catch (error) {
    handleError(error, "提醒执行失败。");
  } finally {
    working.value = "";
  }
}

async function setReminderEnabled(rule: NotificationReminderRuleRecord, enabled: boolean) {
  working.value = `toggle:${rule.id}`;
  try {
    await api.saveNotificationReminderRule({
      id: rule.id, name: rule.name, notifier_id: rule.notifier_id, kind: rule.kind,
      enabled, interval_minutes: rule.interval_minutes, lead_days: rule.lead_days,
      threshold_percent: rule.threshold_percent, node_ids: rule.node_ids,
      message_template: rule.message_template ?? "",
    });
    await load();
  } catch (error) {
    handleError(error, "提醒规则状态更新失败。");
  } finally {
    working.value = "";
  }
}

function removeReminder(rule: NotificationReminderRuleRecord) {
  dialog.warning({
    title: "删除提醒规则",
    content: `确认删除“${rule.name}”？`,
    positiveText: "删除",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.deleteNotificationReminderRule(rule.id);
        await load();
        message.success("提醒规则已删除");
      } catch (error) {
        handleError(error, "提醒规则删除失败。");
        return false;
      }
      return true;
    },
  });
}

async function toggleTelegramBot(notifierID: string, enabled: boolean) {
  working.value = `bot:${notifierID}`;
  try {
    await api.updateTelegramBotAccess(notifierID, enabled);
    await load();
    message.success(enabled ? "Bot 查询已启用" : "Bot 查询已停用");
  } catch (error) {
    handleError(error, "Bot 查询设置失败。");
  } finally {
    working.value = "";
  }
}

function openCreate() {
  editingID.value = "";
  name.value = "";
  kind.value = "telegram";
  enabled.value = true;
  events.value = ["created", "resolved"];
  url.value = "";
  botToken.value = "";
  chatID.value = "";
  modalOpen.value = true;
}

function openEdit(item: NotificationNotifierRecord) {
  editingID.value = item.id;
  name.value = item.name;
  kind.value = item.kind;
  enabled.value = item.enabled;
  events.value = [...item.events];
  url.value = "";
  botToken.value = "";
  chatID.value = "";
  modalOpen.value = true;
}

async function save() {
  if (!name.value.trim()) {
    message.warning("请输入通知通道名称");
    return;
  }
  if (kind.value === "telegram" && telegramChatIDError.value) {
    message.warning(telegramChatIDError.value);
    return;
  }
  if (kind.value === "telegram" && !editingID.value && !botToken.value.trim()) {
    message.warning("请输入 Telegram Bot Token");
    return;
  }
  if (events.value.length === 0) {
    message.warning("请至少选择一个触发事件");
    return;
  }
  saving.value = true;
  try {
    await api.saveNotificationNotifier({
      ...(editingID.value ? { id: editingID.value } : {}),
      name: name.value.trim(), kind: kind.value, enabled: enabled.value, events: events.value,
      ...(url.value ? { url: url.value.trim() } : {}),
      ...(botToken.value ? { bot_token: botToken.value.trim() } : {}),
      ...(chatID.value ? { chat_id: chatID.value.trim() } : {}),
    });
    modalOpen.value = false;
    await load();
    message.success("通知通道已保存");
  } catch (error) {
    handleError(error, "通知通道保存失败。");
  } finally {
    saving.value = false;
  }
}

async function test(item: NotificationNotifierRecord) {
  working.value = `test:${item.id}`;
  try {
    await api.testNotificationNotifier(item.id);
    message.success("测试通知已送达");
  } catch (error) {
    handleError(error, "测试通知发送失败。");
  } finally {
    working.value = "";
  }
}

async function setNotifierEnabled(item: NotificationNotifierRecord, value: boolean) {
  if (working.value) return;
  working.value = `notifier:${item.id}`;
  try {
    await api.saveNotificationNotifier({ id: item.id, name: item.name, kind: item.kind, enabled: value, events: item.events });
    await load();
    message.success(value ? "通知通道已启用" : "通知通道已停用");
  } catch (error) {
    handleError(error, "通知通道状态保存失败。");
  } finally {
    working.value = "";
  }
}

function remove(item: NotificationNotifierRecord) {
  dialog.warning({
    title: "删除通知通道",
    content: `确认删除“${item.name}”？尚未发送的关联投递也会被移除。`,
    positiveText: "删除",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.deleteNotificationNotifier(item.id);
        await load();
        message.success("通知通道已删除");
      } catch (error) {
        handleError(error, "通知通道删除失败。");
        return false;
      }
      return true;
    },
  });
}

function kindLabel(value: NotificationKind) {
  return ({ telegram: "Telegram", slack: "Slack", webhook: "Webhook" })[value];
}

function reminderKindLabel(value: NotificationReminderKind) {
  return reminderKindOptions.find((item) => item.value === value)?.label ?? value;
}

function intervalLabel(value: number) {
  return intervalOptions.find((item) => item.value === value)?.label ?? `每 ${value} 分钟`;
}

function timeUntil(value: string) {
  const milliseconds = new Date(value).getTime() - Date.now();
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "即将执行";
  const minutes = Math.ceil(milliseconds / 60_000);
  if (minutes < 60) return `${minutes} 分钟后`;
  const hours = Math.ceil(minutes / 60);
  if (hours < 24) return `${hours} 小时后`;
  return `${Math.ceil(hours / 24)} 天后`;
}

function reminderScope(rule: NotificationReminderRuleRecord) {
  if (rule.node_ids.length === 0) return "全部节点";
  const names = rule.node_ids.map((id) => nodes.value.find((node) => node.id === id)?.name ?? id);
  return names.length <= 2 ? names.join("、") : `${names.slice(0, 2).join("、")} 等 ${names.length} 个节点`;
}

function telegramBotFor(notifierID: string) {
  return settings.value.telegram_bots.find((item) => item.notifier_id === notifierID);
}

onMounted(() => load());
</script>

<template>
  <main class="workspace operations-workspace notification-workspace">
    <div class="page-heading page-heading--console">
      <div><h2>告警通知</h2><p>告警创建与恢复会进入持久化队列，失败按退避策略重试</p></div>
      <div class="page-heading__actions"><icon-action :loading="refreshing" label="刷新通知" @click="load"><n-icon><refresh-cw /></n-icon></icon-action><n-button type="primary" @click="openCreate"><template #icon><n-icon><plus /></n-icon></template>添加通道</n-button></div>
    </div>

    <section v-if="!loading" class="stat-grid" aria-label="通知摘要">
      <stat-card label="通知通道">
        <template #icon><webhook :size="18" /></template>
        {{ settings.notifiers.length }}
        <template #hint>{{ settings.notifiers.filter((item) => item.enabled).length }} 个启用 · {{ activeReminderCount }} 条自动规则</template>
      </stat-card>
      <stat-card label="已送达" tone="signal" :muted="!delivered">
        <template #icon><check-circle2 :size="18" /></template>
        {{ delivered }}
        <template #hint>最近 30 条投递记录</template>
      </stat-card>
      <stat-card label="待投递 / 重试" :tone="pending ? 'pressure' : 'neutral'" :muted="!pending">
        <template #icon><send :size="18" /></template>
        {{ pending }}
        <template #hint>服务端自动处理</template>
      </stat-card>
      <stat-card label="最终失败" :tone="failed ? 'critical' : 'neutral'" :muted="!failed">
        <template #icon><bell-ring :size="18" /></template>
        {{ failed }}
        <template #hint>超过最大重试次数</template>
      </stat-card>
    </section>

    <surface-state v-if="loadError" kind="error" :hint="loadError" :busy="refreshing" @action="load()" />
    <surface-state v-if="loading" kind="loading" stats />
    <template v-else-if="loaded">
      <section class="notifier-surface">
        <header><span>CHANNELS</span><h3>出站通道</h3></header>
        <div v-if="settings.notifiers.length === 0" class="surface-state surface-state--empty"><webhook :size="27" /><strong>尚未配置通知通道</strong><n-button size="small" type="primary" @click="openCreate">添加通道</n-button></div>
        <div v-else class="notifier-list">
          <article v-for="item in settings.notifiers" :key="item.id">
            <div class="notifier-list__icon"><bell-ring :size="18" /></div>
            <div class="notifier-list__identity"><strong>{{ item.name }}</strong><small>{{ kindLabel(item.kind) }} · {{ item.target_hint }} · {{ item.events.map((event) => event === 'created' ? '创建' : '恢复').join(' / ') }}</small></div>
            <label class="toggle-label"><span>{{ item.enabled ? '启用' : '已停用' }}</span><n-switch :value="item.enabled" size="small" :loading="working === `notifier:${item.id}`" :disabled="!!working" :aria-label="`启用 ${item.name} 通道`" @update:value="setNotifierEnabled(item, $event)" /></label>
            <div class="notifier-list__actions"><icon-action label="编辑通道" @click="openEdit(item)"><n-icon><pencil /></n-icon></icon-action><icon-action :loading="working === `test:${item.id}`" label="测试通知" @click="test(item)"><n-icon><flask-conical /></n-icon></icon-action><icon-action tone="danger" label="删除通道" @click="remove(item)"><n-icon><trash2 /></n-icon></icon-action></div>
          </article>
        </div>
      </section>

      <section class="reminder-surface">
        <header class="notification-surface-heading">
          <div><span>AUTOMATION</span><h3>自定义提醒</h3><p>按规则发送概览、活动告警、VPS 到期和流量阈值</p></div>
          <n-button size="small" secondary :disabled="settings.notifiers.length === 0" @click="openReminderCreate"><template #icon><n-icon><plus /></n-icon></template>添加规则</n-button>
        </header>
        <div v-if="settings.reminder_rules.length === 0" class="surface-state surface-state--empty"><alarm-clock :size="27" /><strong>尚未配置自动提醒</strong><small>先创建通知通道，再设置提醒频率和作用节点</small><n-button size="small" secondary @click="settings.notifiers.length ? openReminderCreate() : openCreate()">{{ settings.notifiers.length ? '添加规则' : '添加通道' }}</n-button></div>
        <div v-else class="reminder-list">
          <article v-for="rule in settings.reminder_rules" :key="rule.id">
            <div class="reminder-list__icon"><calendar-clock v-if="rule.kind === 'asset_expiry'" :size="18" /><gauge v-else-if="rule.kind === 'traffic_usage'" :size="18" /><alarm-clock v-else :size="18" /></div>
            <div class="reminder-list__identity"><strong>{{ rule.name }}</strong><small>{{ reminderKindLabel(rule.kind) }} · {{ reminderScope(rule) }} · {{ rule.notifier_name }}</small><em :class="{ 'is-error': rule.last_error }">{{ rule.last_error || rule.last_result || '尚未执行' }}</em></div>
            <div class="reminder-list__schedule"><span>{{ intervalLabel(rule.interval_minutes) }}</span><small>下次 {{ timeUntil(rule.next_run_at) }}</small></div>
            <n-switch :value="rule.enabled" size="small" :loading="working === `toggle:${rule.id}`" :aria-label="`启用 ${rule.name} 提醒`" @update:value="setReminderEnabled(rule, $event)" />
            <div class="notifier-list__actions"><icon-action :loading="working === `rule:${rule.id}`" label="立即执行提醒" @click="runReminder(rule)"><n-icon><play /></n-icon></icon-action><icon-action label="编辑提醒" @click="openReminderEdit(rule)"><n-icon><pencil /></n-icon></icon-action><icon-action tone="danger" label="删除提醒" @click="removeReminder(rule)"><n-icon><trash2 /></n-icon></icon-action></div>
          </article>
        </div>
      </section>

      <section class="bot-surface">
        <header class="notification-surface-heading"><div><span>TELEGRAM COMMANDS</span><h3>Bot 查询</h3><p>只响应绑定的数字 Chat ID；同一 Bot 不应再被其他程序轮询或配置 webhook</p></div></header>
        <div v-if="settings.notifiers.every((item) => item.kind !== 'telegram')" class="surface-state surface-state--empty"><bot :size="27" /><strong>没有 Telegram 通道</strong><small>添加 Telegram 通道后可启用 Bot 查询</small><n-button size="small" secondary @click="openCreate">添加通道</n-button></div>
        <div v-else class="bot-list">
          <article v-for="item in settings.notifiers.filter((entry) => entry.kind === 'telegram')" :key="item.id">
            <div class="notifier-list__icon"><bot :size="18" /></div>
            <div><strong>{{ item.name }}</strong><small>/status · /nodes · /node 节点名 · 直接发送节点名</small><em v-if="telegramBotFor(item.id)?.last_error" class="is-error">{{ telegramBotFor(item.id)?.last_error }}</em><em v-else>{{ telegramBotFor(item.id)?.last_poll_at ? `最近轮询 ${relativeTime(telegramBotFor(item.id)?.last_poll_at ?? null)}` : '启用后每 15 秒检查新消息' }}</em></div>
            <n-switch :value="telegramBotFor(item.id)?.enabled ?? false" :loading="working === `bot:${item.id}`" :disabled="!item.enabled" :aria-label="`启用 ${item.name} Bot 查询`" @update:value="toggleTelegramBot(item.id, $event)" />
          </article>
        </div>
      </section>

      <section class="ops-surface delivery-surface">
        <header><span>DELIVERY LOG</span><h3>最近投递</h3></header>
        <div v-if="settings.deliveries.length === 0" class="surface-state surface-state--empty"><send :size="25" /><strong>暂无投递记录</strong><small>告警或提醒投递后会在此记录结果</small><n-button size="small" secondary @click="load()">刷新记录</n-button></div>
        <div v-else class="ops-table-wrap"><table class="ops-table mobile-ledger"><thead><tr><th>通道</th><th>事件</th><th>状态</th><th class="num">尝试</th><th class="num">HTTP</th><th>时间</th><th>错误</th></tr></thead><tbody><tr v-for="item in settings.deliveries" :key="item.id"><td data-label="通道"><strong>{{ item.notifier_name }}</strong><small>{{ kindLabel(item.notifier_kind) }}</small></td><td data-label="事件">{{ item.event_type === 'created' ? '告警创建' : '告警恢复' }}</td><td data-label="状态"><span class="pill" :class="`pill--${({ queued: 'pressure', retry: 'pressure', delivered: 'signal', failed: 'critical' } as const)[item.status]}`">{{ ({ queued: '排队', retry: '重试', delivered: '已送达', failed: '失败' } as const)[item.status] }}</span></td><td data-label="尝试" class="num">{{ item.attempt_count }}</td><td data-label="HTTP" class="num">{{ item.response_code || '—' }}</td><td data-label="时间">{{ relativeTime(item.delivered_at || item.created_at) }}</td><td data-label="错误" class="delivery-surface__error">{{ item.last_error || '—' }}</td></tr></tbody></table></div>
      </section>
    </template>

    <n-modal
      v-model:show="reminderModalOpen"
      preset="card"
      class="reminder-modal"
      :style="{ width: 'calc(100% - 32px)', maxWidth: '680px', maxHeight: 'min(760px, calc(100vh - 32px))' }"
      content-style="overflow-y: auto"
      :title="reminderEditingID ? '编辑提醒规则' : '添加提醒规则'"
      :mask-closable="!saving"
    >
      <n-form data-testid="reminder-form" label-placement="top" @submit.prevent="saveReminder">
        <div class="notifier-modal__grid"><n-form-item label="规则名称"><n-input v-model:value="reminderName" maxlength="80" placeholder="例如 每 6 小时运行概览" /></n-form-item><n-form-item label="通知通道"><n-select v-model:value="reminderNotifierID" :options="notifierOptions" placeholder="选择接收通道" /></n-form-item></div>
        <div class="notifier-modal__grid"><n-form-item label="提醒类型"><n-select v-model:value="reminderKind" :options="reminderKindOptions" /></n-form-item><n-form-item label="提醒频率"><n-select v-model:value="reminderInterval" :options="intervalOptions" /></n-form-item></div>
        <n-form-item v-if="reminderKind === 'asset_expiry'" label="提前提醒天数"><n-input-number v-model:value="reminderLeadDays" :min="0" :max="365" /></n-form-item>
        <n-form-item v-if="reminderKind === 'traffic_usage'" label="流量使用阈值（%）"><n-input-number v-model:value="reminderThreshold" :min="1" :max="100" /></n-form-item>
        <n-form-item label="作用节点"><n-select v-model:value="reminderNodeIDs" multiple clearable :options="nodeOptions" placeholder="留空表示全部节点" /></n-form-item>
        <section class="reminder-message-editor" aria-label="通知内容">
          <strong>通知内容</strong>
          <p>规则名称、提醒类型和提醒频率由上方规则设置自动生成，在消息正文中不可编辑或替换。</p>
          <pre class="reminder-message-header" data-testid="reminder-fixed-fields">{{ reminderMessageHeader }}</pre>
          <n-form-item label="自定义正文" :validation-status="reminderMessageError ? 'error' : undefined" :feedback="reminderMessageError || undefined">
            <n-input v-model:value="reminderMessageTemplate" data-testid="reminder-message-template" type="textarea" maxlength="2000" show-count :autosize="{ minRows: 4, maxRows: 10 }" placeholder="留空使用系统正文；也可写入自己的提醒内容，并插入实时数据。" />
          </n-form-item>
          <div class="reminder-message-tools">
            <n-button size="tiny" secondary @click="reminderMessageTemplate += '{{details}}'">插入实时数据</n-button>
            <n-button size="tiny" secondary @click="reminderMessageTemplate += '{{time}}'">插入发送时间</n-button>
            <n-button size="tiny" quaternary @click="reminderMessageTemplate = ''">恢复系统正文</n-button>
          </div>
          <p><code v-pre>{{details}}</code> 为当前统计或命中列表，<code v-pre>{{time}}</code> 为发送时间。自定义正文不会改变规则的触发条件。</p>
          <details open class="reminder-message-preview"><summary>通知预览（数据将在发送时填入）</summary><pre data-testid="reminder-message-preview">{{ reminderMessagePreview }}</pre></details>
        </section>
        <div class="reminder-modal__note"><strong>发送策略</strong><span>运行概览始终发送；告警、到期和流量规则仅在有命中项时发送。失败会在 5 分钟后重试，成功后按所选频率继续。</span></div>
        <n-form-item label="启用"><n-switch v-model:value="reminderEnabled" /></n-form-item>
        <div class="modal-actions"><n-button :disabled="saving" @click="reminderModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="saving">保存</n-button></div>
      </n-form>
    </n-modal>

    <n-modal
      v-model:show="modalOpen"
      preset="card"
      class="notifier-modal"
      :style="{ width: 'calc(100% - 32px)', maxWidth: '720px', maxHeight: 'min(760px, calc(100vh - 32px))' }"
      content-style="overflow-y: auto"
      :title="editingID ? '编辑通知通道' : '添加通知通道'"
      :mask-closable="!saving"
    >
      <n-form data-testid="notifier-form" label-placement="top" @submit.prevent="save">
        <div class="notifier-modal__grid"><n-form-item label="通道名称"><n-input v-model:value="name" maxlength="80" placeholder="例如 运维 Telegram" /></n-form-item><n-form-item label="类型"><n-select v-model:value="kind" :options="kindOptions" :disabled="Boolean(editingID)" /></n-form-item></div>
        <template v-if="kind === 'telegram'">
          <n-form-item label="Bot Token">
            <n-input v-model:value="botToken" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password', spellcheck: false }" :placeholder="editingID ? '留空保持现有密钥' : '123456789:AA...'" />
          </n-form-item>
          <n-form-item label="Chat ID" :validation-status="telegramChatIDError ? 'error' : undefined" :feedback="telegramChatIDError || undefined">
            <n-input data-testid="telegram-chat-id" v-model:value="chatID" :placeholder="editingID ? '留空保持现有目标' : '例如 123456789 或 -1001234567890'" />
          </n-form-item>
          <div class="notifier-field-help">
            <strong>填写接收通知的目标，不是机器人用户名</strong>
            <span>私聊：先向机器人发送 /start，再填写数字 chat.id</span>
            <span>群组：添加机器人后填写负数 chat.id；公开频道可填写 @channel_username，并授予机器人发消息权限</span>
            <strong>获取 chat.id：按下面 3 步操作</strong>
            <span>1. 在接收通知的私聊或群组里向机器人发送 /start。频道请先将机器人设为管理员，再发布一条消息。</span>
            <span>2. 复制接口地址到浏览器打开；将 &lt;BotToken&gt; 替换为 BotFather 提供的密钥。上方填写了 Token 时，复制按钮会自动填入。</span>
            <code class="telegram-api-template" data-testid="telegram-updates-api">{{ telegramUpdatesTemplate }}</code>
            <div><n-button size="tiny" secondary @click="copyTelegramUpdatesURL">复制 Chat ID 接口</n-button> <a href="https://core.telegram.org/bots/api#getupdates" target="_blank" rel="noopener noreferrer">Telegram 官方说明</a></div>
            <span>3. 找到 result 中的 message.chat.id，复制完整数字（含负号）到上方 Chat ID。频道对应 channel_post.chat.id；不要复制 from.id。</span>
            <span>若 result 为空，请先暂停下方的「Bot 查询」、重新发消息后再打开接口，取到 ID 后可重新开启。409 表示已有 Webhook 或其他程序在获取更新，需要先处理该冲突。</span>
            <span v-if="editingID">已保存的 Token 不会回显；未重新填写 Token 时，复制的是接口模板。</span>
          </div>
        </template>
        <n-form-item v-else :label="kind === 'slack' ? 'Slack Incoming Webhook URL' : 'Webhook URL'"><n-input v-model:value="url" type="password" show-password-on="click" :placeholder="editingID ? '留空保持现有地址' : 'https://...'" /></n-form-item>
        <n-form-item label="触发事件"><n-checkbox-group v-model:value="events"><n-checkbox value="created" label="告警创建" /><n-checkbox value="resolved" label="告警恢复" /></n-checkbox-group></n-form-item>
        <n-form-item label="启用"><n-switch v-model:value="enabled" /></n-form-item>
        <div class="modal-actions"><n-button :disabled="saving" @click="modalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="saving">保存</n-button></div>
      </n-form>
    </n-modal>
  </main>
</template>

<style scoped>
.reminder-message-editor { display: grid; gap: 10px; margin: 0 0 20px; padding: 14px; border: 1px solid var(--hf-line); border-radius: 10px; }
.reminder-message-editor > p { margin: 0; color: var(--hf-muted); font-size: 12px; line-height: 1.65; }
.reminder-message-editor .n-form-item { margin-bottom: 0; }
.reminder-message-header, .reminder-message-preview pre { margin: 0; padding: 12px; background: var(--hf-surface-soft); color: var(--hf-ink); font: 12px/1.7 var(--hf-data); white-space: pre-wrap; overflow-wrap: anywhere; }
.reminder-message-tools { display: flex; gap: 6px; flex-wrap: wrap; }
.reminder-message-preview summary { cursor: pointer; margin-bottom: 8px; color: var(--hf-muted); font-size: 12px; }
.telegram-api-template { display: block; padding: 8px; overflow-wrap: anywhere; color: var(--hf-ink); }
.notification-workspace { display: grid; gap: 20px; }.notifier-surface,.delivery-surface { overflow: hidden; border: 1px solid var(--hf-line); border-radius: var(--hf-radius); background: var(--hf-surface); box-shadow: var(--hf-shadow); }.notifier-surface > header,.delivery-surface > header { min-height: 62px; padding: 13px 18px; border-bottom: 1px solid var(--hf-line); }.notifier-surface > header span,.delivery-surface > header span { color: var(--hf-muted); font-family: var(--hf-data); font-size: 12px; }.notifier-surface h3,.delivery-surface h3 { margin: 4px 0 0; color: var(--hf-ink); font-size: 14px; }.notifier-list article { display: grid; min-height: 76px; grid-template-columns: 38px minmax(0, 1fr) auto auto; align-items: center; gap: 12px; padding: 10px 18px; border-bottom: 1px solid var(--hf-line); }.notifier-list article:last-child { border-bottom: 0; }.notifier-list__icon { display: grid; width: 34px; height: 34px; place-items: center; border: 1px solid var(--hf-line); border-radius: 10px; color: var(--hf-accent); }.notifier-list__identity strong,.notifier-list__identity small { display: block; }.notifier-list__identity strong { color: var(--hf-ink); font-size: 13px; }.notifier-list__identity small { margin-top: 5px; color: var(--hf-muted); font-size: 12px; }.notifier-list__actions { display: flex; align-items: center; gap: 3px; }.delivery-surface__error { max-width: 280px; overflow: hidden; color: var(--hf-muted); text-overflow: ellipsis; white-space: nowrap; }.notifier-modal__grid { display: grid; grid-template-columns: 1.3fr 1fr; gap: 16px; }.notifier-field-help { display: grid; gap: 4px; margin: -8px 0 20px; padding: 11px 13px; border: 1px solid var(--hf-line); background: var(--hf-surface-soft); color: var(--hf-muted); font-size: 12px; line-height: 1.55; }.notifier-field-help strong { color: var(--hf-ink); font-size: 12px; }.notifier-field-help span { display: block; }@media (max-width: 720px) { .notifier-list article { grid-template-columns: 38px minmax(0, 1fr) auto; }.notifier-list__actions { grid-column: 2 / -1; }.notifier-modal__grid { grid-template-columns: 1fr; gap: 0; }.notifier-field-help { margin-top: -4px; } }
.reminder-surface,.bot-surface { overflow: hidden; border: 1px solid var(--hf-line); border-radius: var(--hf-radius); background: var(--hf-surface); box-shadow: var(--hf-shadow); }.notification-surface-heading { display: flex; min-height: 72px; align-items: center; justify-content: space-between; gap: 16px; padding: 13px 18px; border-bottom: 1px solid var(--hf-line); }.notification-surface-heading span { color: var(--hf-muted); font-family: var(--hf-data); font-size: 12px; }.notification-surface-heading h3 { margin: 4px 0 0; color: var(--hf-ink); font-size: 14px; }.notification-surface-heading p { margin: 4px 0 0; color: var(--hf-muted); font-size: 12px; }.reminder-list article { display: grid; min-height: 84px; grid-template-columns: 38px minmax(220px, 1fr) minmax(120px, auto) auto auto; align-items: center; gap: 12px; padding: 11px 18px; border-bottom: 1px solid var(--hf-line); }.reminder-list article:last-child,.bot-list article:last-child { border-bottom: 0; }.reminder-list__icon { display: grid; width: 34px; height: 34px; place-items: center; border: 1px solid var(--hf-line); border-radius: 10px; color: var(--hf-accent); }.reminder-list__identity strong,.reminder-list__identity small,.reminder-list__identity em,.reminder-list__schedule span,.reminder-list__schedule small,.bot-list article strong,.bot-list article small,.bot-list article em { display: block; }.reminder-list__identity strong,.bot-list article strong { color: var(--hf-ink); font-size: 13px; }.reminder-list__identity small,.bot-list article small { margin-top: 4px; color: var(--hf-muted); font-size: 12px; }.reminder-list__identity em,.bot-list article em { margin-top: 5px; color: var(--hf-muted); font-size: 12px; font-style: normal; }.reminder-list__identity em.is-error,.bot-list article em.is-error { color: var(--hf-danger); }.reminder-list__schedule { text-align: right; }.reminder-list__schedule span { color: var(--hf-ink); font-size: 12px; }.reminder-list__schedule small { margin-top: 4px; color: var(--hf-muted); font-size: 12px; }.bot-list article { display: grid; min-height: 76px; grid-template-columns: 38px minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 11px 18px; border-bottom: 1px solid var(--hf-line); }.reminder-modal__note { display: grid; gap: 4px; margin: -4px 0 18px; padding: 11px 13px; border: 1px solid var(--hf-line); background: var(--hf-surface-soft); color: var(--hf-muted); font-size: 12px; line-height: 1.55; }.reminder-modal__note strong { color: var(--hf-ink); }@media (max-width: 820px) { .reminder-list article { grid-template-columns: 38px minmax(0, 1fr) auto; }.reminder-list__schedule { grid-column: 2; text-align: left; }.reminder-list__actions { grid-column: 2 / -1; }.notification-surface-heading { align-items: flex-start; } }@media (max-width: 520px) { .notification-surface-heading { flex-direction: column; }.notification-surface-heading .n-button { width: 100%; }.reminder-list article { grid-template-columns: 38px minmax(0, 1fr) auto; }.reminder-list__identity { min-width: 0; }.reminder-list__identity small { overflow-wrap: anywhere; } }
</style>

<style>
.notifier-modal.n-card { max-width: 720px; }
.notifier-modal.n-card > .n-card__content { min-height: 0; }
.reminder-modal.n-card { max-width: 680px; }
.reminder-modal.n-card > .n-card__content { min-height: 0; }
</style>
