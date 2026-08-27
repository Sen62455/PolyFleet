import { flushPromises, mount } from "@vue/test-utils";
import { NMessageProvider } from "naive-ui";
import { defineComponent } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { TrafficReport } from "../../types";
import TrafficReportsDashboard from "./TrafficReportsDashboard.vue";

const report: TrafficReport = {
  range: "30d",
  from: "2026-07-26T00:00:00Z",
  to: "2026-08-25T00:00:00Z",
  upload_bytes: 40 * 1024 ** 3,
  download_bytes: 60 * 1024 ** 3,
  total_bytes: 100 * 1024 ** 3,
  previous_upload_bytes: 20 * 1024 ** 3,
  previous_download_bytes: 30 * 1024 ** 3,
  previous_total_bytes: 50 * 1024 ** 3,
  daily: [
    { bucket_at: "2026-08-23T00:00:00Z", upload_bytes: 4 * 1024 ** 3, download_bytes: 6 * 1024 ** 3 },
    { bucket_at: "2026-08-24T00:00:00Z", upload_bytes: 8 * 1024 ** 3, download_bytes: 12 * 1024 ** 3 },
  ],
  top_users: [{ id: "user-1", name: "Alice", upload_bytes: 8, download_bytes: 12, total_bytes: 20 }],
  top_nodes: [{ id: "node-1", name: "dmit2-reality", upload_bytes: 14, download_bytes: 16, total_bytes: 30 }],
};

afterEach(() => vi.restoreAllMocks());

describe("TrafficReportsDashboard", () => {
  it("renders a bidirectional trend and both rankings", async () => {
    vi.spyOn(api, "trafficReport").mockResolvedValue(report);
    const host = defineComponent({
      components: { NMessageProvider, TrafficReportsDashboard },
      template: `<n-message-provider><traffic-reports-dashboard /></n-message-provider>`,
    });
    const wrapper = mount(host);
    await flushPromises();

    expect(wrapper.text()).toContain("100 GiB");
    expect(wrapper.text()).toContain("较上期 +100.0%");
    expect(wrapper.text()).toContain("Alice");
    expect(wrapper.text()).toContain("dmit2-reality");
    expect(wrapper.find('[role="img"][aria-label="每日上下行流量趋势"]').exists()).toBe(true);
    expect(wrapper.findAll("polyline")).toHaveLength(2);
    wrapper.unmount();
  });
});
