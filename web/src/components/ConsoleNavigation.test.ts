import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import ConsoleNavigation from "./ConsoleNavigation.vue";

describe("ConsoleNavigation", () => {
  it("renders system health and keeps collapse control interactive", async () => {
    const wrapper = mount(ConsoleNavigation, {
      props: { activeView: "overview", online: 3, total: 3, issues: 0 },
    });

    expect(wrapper.text()).toContain("系统健康");
    expect(wrapper.text()).toContain("全部在线");
    expect(wrapper.text()).toContain("运行正常");

    await wrapper.find(".console-nav__collapse").trigger("click");
    expect(wrapper.emitted("collapse")).toHaveLength(1);
  });

  it("uses a truthful empty-fleet state", () => {
    const wrapper = mount(ConsoleNavigation, {
      props: { activeView: "overview", online: 0, total: 0, issues: 0 },
    });

    expect(wrapper.text()).toContain("等待节点");
    expect(wrapper.text()).toContain("尚未接入");
  });
});
