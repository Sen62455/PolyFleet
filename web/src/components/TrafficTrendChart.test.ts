import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import TrafficTrendChart from "./TrafficTrendChart.vue";

describe("TrafficTrendChart", () => {
  it("renders current, previous, and quota reference series", () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_at: "2026-08-23T00:00:00Z", upload_bytes: 12, download_bytes: 18 },
          { bucket_at: "2026-08-24T00:00:00Z", upload_bytes: 20, download_bytes: 24 },
        ],
        previousPoints: [
          { bucket_at: "2026-07-24T00:00:00Z", upload_bytes: 8, download_bytes: 14 },
          { bucket_at: "2026-07-25T00:00:00Z", upload_bytes: 16, download_bytes: 19 },
        ],
        capacityBytes: 30,
      },
    });

    expect(wrapper.findAll("polyline")).toHaveLength(4);
    expect(wrapper.find(".traffic-trend__capacity").exists()).toBe(true);
    expect(wrapper.find(".traffic-trend__line--previous-download").attributes("points")).not.toBe("");
  });
});
