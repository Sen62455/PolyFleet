import { flushPromises, mount } from "@vue/test-utils";
import { NDialogProvider, NMessageProvider } from "naive-ui";
import { defineComponent } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { UserRecord } from "../../types";
import UserDashboard from "./UserDashboard.vue";

function user(id: string, username: string): UserRecord {
  return {
    id, username, display_name: username.toUpperCase(), notes: "",
    enabled: true, expires_at: null, status: "active",
    traffic_limit_bytes: 0, traffic_upload_bytes: 0,
    traffic_download_bytes: 0, traffic_used_bytes: 0,
    quota_state: "active", last_traffic_at: null,
    online_connections: 0, online_nodes: 0, assignments: [],
    created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z",
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("UserDashboard pagination", () => {
  it("requests the next bounded server page instead of filtering a full list", async () => {
    const list = vi.spyOn(api, "listUsersPage").mockImplementation(async (filters = {}) => ({
      users: filters.offset === 50 ? [user("user-51", "last-user")] : [user("user-1", "first-user")],
      total: 51,
      limit: 50,
      offset: filters.offset ?? 0,
    }));
    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, UserDashboard },
      template: `
        <n-dialog-provider>
          <n-message-provider><user-dashboard :nodes="[]" /></n-message-provider>
        </n-dialog-provider>
      `,
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();

    expect(wrapper.text()).toContain("FIRST-USER");
    expect(wrapper.text()).toContain("1 - 1 / 51");
    const next = wrapper.findAll("button").find((button) => button.text().trim() === "下一页");
    await next!.trigger("click");
    await flushPromises();

    expect(list).toHaveBeenLastCalledWith({ search: "", limit: 50, offset: 50 });
    expect(wrapper.text()).toContain("LAST-USER");
    wrapper.unmount();
  });
});
