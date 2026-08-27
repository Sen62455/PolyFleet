import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { NodeTelemetrySnapshot } from "../../types";
import HostTelemetryPanel from "./HostTelemetryPanel.vue";

const snapshot: NodeTelemetrySnapshot = {
  supported: true,
  sampled_at: "2026-08-09T06:00:00Z",
  processes_available: true,
  processes_error_code: "",
  processes_total: 24,
  processes_truncated: true,
  processes_sampled_at: "2026-08-09T06:00:00Z",
  processes: [
    { pid: 100, name: "memory-heavy", unit: "memory.service", cpu_percent: 2.4, rss_bytes: 256 * 1024 ** 2, uptime_seconds: 600 },
    { pid: 200, name: "cpu-heavy", unit: "cpu.service", cpu_percent: 125.5, rss_bytes: 32 * 1024 ** 2, uptime_seconds: 120 },
  ],
  services_available: true,
  services_error_code: "",
  services_total: 2,
  services_truncated: false,
  services_sampled_at: "2026-08-09T06:00:00Z",
  services: [
    {
      unit: "hysteria-server.service",
      description: "Hysteria Server",
      active_state: "active",
      sub_state: "running",
      cpu_percent: 1.2,
      cpu_peak_percent: 4.5,
      memory_bytes: 24 * 1024 ** 2,
      memory_peak_bytes: 32 * 1024 ** 2,
      tasks: 9,
      restarts: 0,
      main_pid: 300,
    },
    {
      unit: "legacy.service",
      active_state: "failed",
      sub_state: "dead",
      cpu_percent: 0,
      cpu_peak_percent: 0.5,
      memory_bytes: 0,
      memory_peak_bytes: 8 * 1024 ** 2,
      tasks: 0,
      restarts: 3,
      main_pid: 0,
    },
  ],
};

afterEach(() => vi.restoreAllMocks());

describe("HostTelemetryPanel", () => {
  it("sorts bounded processes and filters systemd services locally", async () => {
    vi.spyOn(api, "getNodeTelemetry").mockResolvedValue(snapshot);
    const wrapper = mount(HostTelemetryPanel, { props: { nodeId: "node-1" }, attachTo: document.body });
    await flushPromises();

    expect(wrapper.text()).toContain("显示 2 / 24 项");
    expect(wrapper.text()).toContain("显示 1 / 2 项");
    expect(wrapper.text()).toContain("126%");
    expect(wrapper.text()).not.toContain("cpu-heavy200cpu.service100%");
    expect(wrapper.findAll(".telemetry-table--services tbody tr")).toHaveLength(1);
    expect(wrapper.text()).toContain("hysteria-server.service");
    let processRows = wrapper.findAll(".telemetry-table--processes tbody tr");
    expect(processRows[0].text()).toContain("cpu-heavy");

    const memorySort = wrapper.findAll("button").find((button) => button.text().trim() === "按内存");
    await memorySort!.trigger("click");
    processRows = wrapper.findAll(".telemetry-table--processes tbody tr");
    expect(processRows[0].text()).toContain("memory-heavy");

    const failedFilter = wrapper.findAll("button").find((button) => button.text().trim() === "失败");
    await failedFilter!.trigger("click");
    const serviceRows = wrapper.findAll(".telemetry-table--services tbody tr");
    expect(serviceRows).toHaveLength(1);
    expect(serviceRows[0].text()).toContain("legacy.service");
    wrapper.unmount();
  });

  it("keeps old agents as an explicit unsupported state", async () => {
    vi.spyOn(api, "getNodeTelemetry").mockResolvedValue({
      ...snapshot,
      supported: false,
      sampled_at: null,
      processes_available: false,
      processes: [],
      services_available: false,
      services: [],
    });
    const wrapper = mount(HostTelemetryPanel, { props: { nodeId: "node-old" } });
    await flushPromises();

    expect(wrapper.text()).toContain("当前 Agent 尚未上报进程与 systemd 服务快照");
    expect(wrapper.find(".telemetry-table").exists()).toBe(false);
    wrapper.unmount();
  });

  it("keeps the last bounded rows visible when one collector is temporarily unavailable", async () => {
    vi.spyOn(api, "getNodeTelemetry").mockResolvedValue({
      ...snapshot,
      processes_available: false,
      processes_error_code: "procfs_unavailable",
    });
    const wrapper = mount(HostTelemetryPanel, { props: { nodeId: "node-1" } });
    await flushPromises();

    expect(wrapper.text()).toContain("进程采集不可用（procfs_unavailable）");
    expect(wrapper.text()).toContain("cpu-heavy");
    expect(wrapper.text()).toContain("hysteria-server.service");
    wrapper.unmount();
  });

  it("does not let a slower previous node request replace the current node snapshot", async () => {
    let resolveOld!: (value: NodeTelemetrySnapshot) => void;
    const oldRequest = new Promise<NodeTelemetrySnapshot>((resolve) => { resolveOld = resolve; });
    vi.spyOn(api, "getNodeTelemetry").mockImplementation((nodeId) => {
      if (nodeId === "node-old") return oldRequest;
      return Promise.resolve({
        ...snapshot,
        processes: [{ ...snapshot.processes[0], name: "current-node-process" }],
      });
    });
    const wrapper = mount(HostTelemetryPanel, { props: { nodeId: "node-old" } });
    await wrapper.setProps({ nodeId: "node-current" });
    await flushPromises();
    expect(wrapper.text()).toContain("current-node-process");

    resolveOld({
      ...snapshot,
      processes: [{ ...snapshot.processes[0], name: "stale-node-process" }],
    });
    await flushPromises();
    expect(wrapper.text()).toContain("current-node-process");
    expect(wrapper.text()).not.toContain("stale-node-process");
    wrapper.unmount();
  });
});
