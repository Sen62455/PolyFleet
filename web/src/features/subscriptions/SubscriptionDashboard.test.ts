import { flushPromises, mount } from "@vue/test-utils";
import { NDialogProvider, NMessageProvider } from "naive-ui";
import { defineComponent } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { SubscriptionOperationRecord } from "../../types";
import SubscriptionDashboard from "./SubscriptionDashboard.vue";

const subscription: SubscriptionOperationRecord = {
  token_id: "token-1",
  user_id: "user-1",
  username: "alice",
  display_name: "Alice",
  name: "Primary",
  token_prefix: "poly_abcd",
  allowed_formats: ["clash", "sing-box"],
  status: "active",
  token_expires_at: "2026-10-01T00:00:00Z",
  user_expires_at: "2026-11-01T00:00:00Z",
  last_used_at: "2026-08-24T07:00:00Z",
  last_traffic_at: "2026-08-24T07:01:00Z",
  revoked_at: null,
  traffic_limit_bytes: 100 * 1024 ** 3,
  traffic_upload_bytes: 20 * 1024 ** 3,
  traffic_download_bytes: 30 * 1024 ** 3,
  traffic_used_bytes: 50 * 1024 ** 3,
  assignment_count: 3,
  online_nodes: 2,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-24T07:01:00Z",
};

function mountDashboard() {
  const host = defineComponent({
    components: { NDialogProvider, NMessageProvider, SubscriptionDashboard },
    template: `
      <n-dialog-provider>
        <n-message-provider>
          <subscription-dashboard />
        </n-message-provider>
      </n-dialog-provider>
    `,
  });
  return mount(host, { attachTo: document.body });
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("SubscriptionDashboard", () => {
  it("renders quota evidence, filters server-side, and saves an extension", async () => {
    const list = vi.spyOn(api, "listSubscriptionOperations").mockResolvedValue({
      subscriptions: [subscription], total: 1, limit: 50, offset: 0,
    });
    const update = vi.spyOn(api, "updateSubscriptionOperation").mockResolvedValue(subscription);
    const wrapper = mountDashboard();
    await flushPromises();

    expect(wrapper.text()).toContain("Alice");
    expect(wrapper.text()).toContain("50.0 GiB");
    expect(wrapper.text()).toContain("/ 100 GiB · 50%");

    const activeFilter = wrapper.findAll(".ops-segments button").find((button) => button.text() === "活跃");
    await activeFilter!.trigger("click");
    await flushPromises();
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ status: "active", offset: 0 }));

    const adjust = wrapper.findAll("button").find((button) => button.text().trim() === "调整");
    await adjust!.trigger("click");
    await flushPromises();
    expect(document.body.textContent).toContain("调整订阅策略");
    const modal = document.body.querySelector<HTMLElement>(".subscription-edit-modal");
    expect(modal?.style.width).toBe("calc(100% - 32px)");
    expect(modal?.style.maxWidth).toBe("620px");

    const extend = Array.from(document.body.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "+30 天",
    ) as HTMLButtonElement;
    extend.click();
    const save = Array.from(document.body.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "保存",
    ) as HTMLButtonElement;
    save.click();
    await flushPromises();

    expect(update).toHaveBeenCalledWith("token-1", expect.objectContaining({
      token_expires_at: expect.any(String),
      user_expires_at: expect.any(String),
      traffic_limit_bytes: 100 * 1024 ** 3,
    }));
    wrapper.unmount();
  });
});
