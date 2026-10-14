import { flushPromises, mount } from "@vue/test-utils";
import { NDialogProvider, NMessageProvider } from "naive-ui";
import { defineComponent } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { NotificationSettings } from "../../types";
import NotificationDashboard from "./NotificationDashboard.vue";

const settings: NotificationSettings = {
  notifiers: [{
    id: "notifier-1",
    name: "运维 Telegram",
    kind: "telegram",
    enabled: true,
    target_hint: "...12345678",
    events: ["created", "resolved"],
    created_at: "2026-08-24T00:00:00Z",
    updated_at: "2026-08-24T00:00:00Z",
  }],
  deliveries: [{
    id: "delivery-1",
    notifier_id: "notifier-1",
    notifier_name: "运维 Telegram",
    notifier_kind: "telegram",
    alert_id: "alert-1",
    event_type: "created",
    status: "failed",
    attempt_count: 6,
    next_attempt_at: "2026-08-24T01:00:00Z",
    last_error: "notification endpoint returned HTTP 500",
    response_code: 500,
    delivered_at: null,
    created_at: "2026-08-24T00:00:00Z",
  }],
  reminder_rules: [{
    id: "rule-1",
    name: "每 6 小时运行概览",
    notifier_id: "notifier-1",
    notifier_name: "运维 Telegram",
    kind: "fleet_summary",
    enabled: true,
    interval_minutes: 360,
    lead_days: 30,
    threshold_percent: 80,
    node_ids: [],
    message_template: "请检查服务器\n{{details}}",
    last_run_at: null,
    last_success_at: null,
    last_result: "",
    last_error: "",
    next_run_at: "2026-08-24T06:00:00Z",
    created_at: "2026-08-24T00:00:00Z",
    updated_at: "2026-08-24T00:00:00Z",
  }],
  telegram_bots: [{
    notifier_id: "notifier-1",
    notifier_name: "运维 Telegram",
    enabled: true,
    last_poll_at: "2026-08-24T00:01:00Z",
    last_error: "",
    updated_at: "2026-08-24T00:01:00Z",
  }],
};

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("NotificationDashboard", () => {
  it("toggles a channel without resubmitting or clearing its saved credentials", async () => {
    vi.spyOn(api, "getNotificationSettings").mockResolvedValue(settings);
    vi.spyOn(api, "listNodes").mockResolvedValue([]);
    const save = vi.spyOn(api, "saveNotificationNotifier").mockResolvedValue(settings.notifiers[0]);
    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NotificationDashboard },
      template: '<n-dialog-provider><n-message-provider><notification-dashboard /></n-message-provider></n-dialog-provider>',
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();
    await wrapper.get('.notifier-list [role="switch"]').trigger("click");
    await flushPromises();
    expect(save).toHaveBeenCalledWith({ id: settings.notifiers[0].id, name: settings.notifiers[0].name, kind: settings.notifiers[0].kind, events: settings.notifiers[0].events, enabled: !settings.notifiers[0].enabled });
    wrapper.unmount();
  });
  it("shows delivery state and sends a channel test", async () => {
    vi.spyOn(api, "getNotificationSettings").mockResolvedValue(settings);
    vi.spyOn(api, "listNodes").mockResolvedValue([]);
    const sendTest = vi.spyOn(api, "testNotificationNotifier").mockResolvedValue({
      delivered: true, response_code: 200,
    });
    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NotificationDashboard },
      template: `
        <n-dialog-provider>
          <n-message-provider><notification-dashboard /></n-message-provider>
        </n-dialog-provider>
      `,
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();

    expect(wrapper.text()).toContain("运维 Telegram");
    expect(wrapper.text().replace(/\s+/g, "")).toContain("最终失败1");
    expect(wrapper.text()).toContain("notification endpoint returned HTTP 500");
    expect(wrapper.text()).toContain("每 6 小时运行概览");
    expect(wrapper.text()).toContain("/status");

    await wrapper.find('button[aria-label="测试通知"]').trigger("click");
    await flushPromises();
    expect(sendTest).toHaveBeenCalledWith("notifier-1");
    wrapper.unmount();
  });

  it("renders a compact Telegram editor and rejects a bot username as Chat ID", async () => {
    vi.spyOn(api, "getNotificationSettings").mockResolvedValue(settings);
    vi.spyOn(api, "listNodes").mockResolvedValue([]);
    const saveNotifier = vi.spyOn(api, "saveNotificationNotifier").mockResolvedValue(settings.notifiers[0]);
    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NotificationDashboard },
      template: `
        <n-dialog-provider>
          <n-message-provider><notification-dashboard /></n-message-provider>
        </n-dialog-provider>
      `,
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();

    const editButton = wrapper.find('button[aria-label="编辑通道"]');
    expect(editButton).toBeDefined();
    await editButton!.trigger("click");
    await flushPromises();

    const modal = document.querySelector<HTMLElement>(".notifier-modal");
    expect(modal).not.toBeNull();
    expect(modal!.style.maxWidth).toBe("720px");
    expect(document.body.textContent).toContain("填写接收通知的目标，不是机器人用户名");
    expect(document.querySelector('.notifier-modal input[type="password"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="telegram-updates-api"]')?.textContent).toContain("https://api.telegram.org/bot<BotToken>/getUpdates");
    expect(document.body.textContent).toContain("message.chat.id");

    const chatControl = document.querySelector<HTMLElement>('[data-testid="telegram-chat-id"]');
    const chatInput = chatControl?.matches("input") ? chatControl as HTMLInputElement : chatControl?.querySelector("input");
    expect(chatInput).not.toBeNull();
    chatInput!.value = "@polyfleet_bot";
    chatInput!.dispatchEvent(new Event("input", { bubbles: true }));
    await flushPromises();
    document.querySelector<HTMLFormElement>('[data-testid="notifier-form"]')!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(saveNotifier).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("不能填写机器人的用户名");
    wrapper.unmount();
  });

  it("preserves custom content when editing and keeps required message fields read-only", async () => {
    vi.spyOn(api, "getNotificationSettings").mockResolvedValue(settings);
    vi.spyOn(api, "listNodes").mockResolvedValue([]);
    const save = vi.spyOn(api, "saveNotificationReminderRule").mockResolvedValue(settings.reminder_rules[0]);
    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NotificationDashboard },
      template: '<n-dialog-provider><n-message-provider><notification-dashboard /></n-message-provider></n-dialog-provider>',
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();
    await wrapper.get('button[aria-label="编辑提醒"]').trigger('click');
    await flushPromises();
    const fixed = document.querySelector('[data-testid="reminder-fixed-fields"]')!;
    expect(fixed.tagName).toBe('PRE');
    expect(fixed.textContent).toContain('规则名称：每 6 小时运行概览');
    expect(fixed.textContent).toContain('提醒频率：每 6 小时');
    const control = document.querySelector('[data-testid="reminder-message-template"]')!;
    const input = (control.matches('textarea') ? control : control.querySelector('textarea')) as HTMLTextAreaElement;
    expect(input.value).toBe(settings.reminder_rules[0].message_template);
    input.value = '请处理\n{{details}}\n{{time}}';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flushPromises();
    expect(document.querySelector('[data-testid="reminder-message-preview"]')?.textContent).toContain('请处理');
    document.querySelector('[data-testid="reminder-form"]')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: 'rule-1', message_template: input.value }));
    wrapper.unmount();
  });
});
