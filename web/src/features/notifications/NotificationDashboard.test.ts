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
    expect(wrapper.text()).toContain("最终失败1");
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

    const editButton = wrapper.findAll("button").find((button) => button.text() === "编辑");
    expect(editButton).toBeDefined();
    await editButton!.trigger("click");
    await flushPromises();

    const modal = document.querySelector<HTMLElement>(".notifier-modal");
    expect(modal).not.toBeNull();
    expect(modal!.style.maxWidth).toBe("720px");
    expect(document.body.textContent).toContain("填写接收通知的目标，不是机器人用户名");
    expect(document.querySelector('.notifier-modal input[type="password"]')).not.toBeNull();

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
});
