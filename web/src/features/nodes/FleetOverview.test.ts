import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import type { SubscriptionOperationRecord, TrafficReport } from "../../types";
import FleetOverview from "./FleetOverview.vue";

const report: TrafficReport = {
  range: "30d",
  from: "2026-07-26T00:00:00Z",
  to: "2026-08-25T00:00:00Z",
  upload_bytes: 40,
  download_bytes: 60,
  total_bytes: 100,
  previous_upload_bytes: 20,
  previous_download_bytes: 30,
  previous_total_bytes: 50,
  daily: [{ bucket_at: "2026-08-24T00:00:00Z", upload_bytes: 40, download_bytes: 60 }],
  previous_daily: [{ bucket_at: "2026-07-25T00:00:00Z", upload_bytes: 20, download_bytes: 30 }],
  top_users: [],
  top_nodes: [],
};

const subscription: SubscriptionOperationRecord = {
  token_id: "token-1",
  user_id: "user-1",
  username: "alice",
  display_name: "Alice",
  name: "Reality subscription",
  token_prefix: "abc123",
  allowed_formats: ["clash"],
  status: "active",
  token_expires_at: "2026-09-25T00:00:00Z",
  user_expires_at: null,
  last_used_at: null,
  last_traffic_at: null,
  revoked_at: null,
  traffic_limit_bytes: 1000,
  traffic_upload_bytes: 100,
  traffic_download_bytes: 200,
  traffic_used_bytes: 300,
  assignment_count: 2,
  online_nodes: 1,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

describe("FleetOverview", () => {
  it("renders the report comparison and subscription health row", async () => {
    const wrapper = mount(FleetOverview, {
      props: {
        nodes: [], alerts: [], loading: false, error: "",
        trends: {}, assets: {}, trafficReport: report, subscriptions: [subscription],
      },
    });

    expect(wrapper.text()).toContain("30 日双向流量趋势");
    expect(wrapper.text()).toContain("对比上月 +100.0%");
    expect(wrapper.text()).toContain("Reality subscription");
    expect(wrapper.text()).toContain("1 / 2");
    expect(wrapper.findAll("polyline")).toHaveLength(4);

    await wrapper.find(".subscription-health__row").trigger("click");
    expect(wrapper.emitted("subscriptions")).toHaveLength(1);
  });
});
