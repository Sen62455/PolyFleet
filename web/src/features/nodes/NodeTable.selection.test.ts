import { mount } from "@vue/test-utils";
import { NCheckbox } from "naive-ui";
import { describe, expect, it } from "vitest";
import type { NodeRecord } from "../../types";
import NodeTable from "./NodeTable.vue";

function node(id: string, name: string): NodeRecord {
  return {
    id, name, provider: "DMIT", region: "Los Angeles",
    adapter_type: "sing_box_vless_reality", status: "online",
    cpu_percent: 22, memory_used_bytes: 512 * 1024 ** 2, memory_total_bytes: 1024 ** 3,
    disk_used_bytes: 5 * 1024 ** 3, disk_total_bytes: 20 * 1024 ** 3,
    network_rx_bps: 1024, network_tx_bps: 2048,
    traffic_used_bytes: 10 * 1024 ** 3, traffic_limit_bytes: 100 * 1024 ** 3,
    online_connections: 2, last_seen_at: "2026-08-24T08:00:00Z",
    applied_version: 2, desired_version: 2,
  } as NodeRecord;
}

describe("NodeTable batch selection", () => {
  it("emits a bounded node id set for select-all and individual selection", async () => {
    const nodes = [node("node-1", "Alpha"), node("node-2", "Beta")];
    const wrapper = mount(NodeTable, { props: { nodes, selectedNodeIds: [] } });
    const checkboxes = wrapper.findAllComponents(NCheckbox);

    checkboxes[0].vm.$emit("update:checked", true);
    expect(wrapper.emitted("update:selected-node-ids")?.[0]).toEqual([["node-1", "node-2"]]);

    await wrapper.setProps({ selectedNodeIds: ["node-1"] });
    wrapper.findAllComponents(NCheckbox)[2].vm.$emit("update:checked", true);
    expect(wrapper.emitted("update:selected-node-ids")?.at(-1)).toEqual([["node-1", "node-2"]]);
    wrapper.unmount();
  });
});
