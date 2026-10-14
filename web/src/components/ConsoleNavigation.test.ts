import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import ConsoleNavigation from "./ConsoleNavigation.vue";

describe("ConsoleNavigation", () => {
  it("keeps navigation and collapse interactive without a duplicate health card", async () => {
    const wrapper = mount(ConsoleNavigation, {
      props: { activeView: "overview" },
    });

    expect(wrapper.find('[aria-label="系统健康"]').exists()).toBe(false);
    await wrapper.get('[aria-label="用户"]').trigger("click");
    expect(wrapper.emitted("navigate")).toEqual([["users"]]);

    await wrapper.find(".console-nav__collapse").trigger("click");
    expect(wrapper.emitted("collapse")).toHaveLength(1);
  });

  it("keeps collapsed navigation labelled for keyboard users", () => {
    const wrapper = mount(ConsoleNavigation, {
      props: { activeView: "overview", collapsed: true },
    });

    expect(wrapper.find('[aria-label="用户"]').exists()).toBe(true);
    expect(wrapper.find('[aria-label="展开侧边栏"]').exists()).toBe(true);
  });
});
