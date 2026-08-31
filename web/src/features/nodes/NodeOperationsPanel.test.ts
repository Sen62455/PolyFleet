import { flushPromises, mount } from "@vue/test-utils";
import { NDialogProvider, NMessageProvider } from "naive-ui";
import { defineComponent } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import type { ConfigBackupRecord, NodeOperationRecord, NodeRecord } from "../../types";
import NodeOperationsPanel from "./NodeOperationsPanel.vue";

const node = {
  id: "node-1",
  name: "LisaHost",
  adapter_type: "native_hysteria2",
  core_name: "hysteria",
} as NodeRecord;

const failedOperation: NodeOperationRecord = {
  id: "operation-1",
  node_id: "node-1",
  node_name: "LisaHost",
  sequence: 4,
  type: "restart_core",
  status: "failed",
  attempt: 1,
  max_lines: 0,
  target: "",
  output: "service remained inactive",
  error_code: "core_restart_failed",
  error_message: "core restart failed",
  rolled_back: true,
  requested_by: "admin",
  expires_at: "2026-08-08T01:15:00Z",
  started_at: "2026-08-08T01:00:00Z",
  completed_at: "2026-08-08T01:00:03Z",
  created_at: "2026-08-08T01:00:00Z",
  updated_at: "2026-08-08T01:00:03Z",
};

const backup: ConfigBackupRecord = {
  id: "backup-1",
  node_id: "node-1",
  node_name: "LisaHost",
  operation_id: "operation-1",
  local_path: "/var/lib/hyfleet-backups/restart-config.yaml.bak",
  sha256: "a".repeat(64),
  size_bytes: 2048,
  created_at: "2026-08-08T01:00:00Z",
};

afterEach(() => vi.restoreAllMocks());

describe("NodeOperationsPanel", () => {
	it("binds the selected node as source and queues the default ping target", async () => {
	  vi.spyOn(api, "listNodeOperations").mockResolvedValue([]);
	  vi.spyOn(api, "listConfigBackups").mockResolvedValue([]);
	  const create = vi.spyOn(api, "createNodeOperation").mockResolvedValue({
		...failedOperation,
		id: "ping-operation",
		type: "ping",
		status: "queued",
		target: "42.49.64.154",
	  });
	  const host = defineComponent({
		components: { NDialogProvider, NMessageProvider, NodeOperationsPanel },
		setup: () => ({ node }),
		template: `
		  <n-dialog-provider>
			<n-message-provider>
			  <node-operations-panel :node="node" />
			</n-message-provider>
		  </n-dialog-provider>
		`,
	  });
	  const wrapper = mount(host, { attachTo: document.body });
	  await flushPromises();

	  expect(wrapper.text()).toContain("源：LisaHost");
	  const input = wrapper.find("input").element as HTMLInputElement;
	  expect(input.value).toBe("42.49.64.154");
	  const button = wrapper.findAll("button").find((item) => item.text().trim() === "开始 Ping");
	  expect(button).toBeDefined();
	  await button!.trigger("click");
	  await flushPromises();

	  expect(create).toHaveBeenCalledWith("node-1", "ping", 0, "42.49.64.154");
	  wrapper.unmount();
	});

  it("shows rollback evidence and queues a retry for a failed operation", async () => {
    vi.spyOn(api, "listNodeOperations").mockResolvedValue([failedOperation]);
    vi.spyOn(api, "listConfigBackups").mockResolvedValue([backup]);
    vi.spyOn(api, "retryNodeOperation").mockResolvedValue({
      ...failedOperation,
      id: "operation-2",
      sequence: 5,
      status: "queued",
      retry_of: failedOperation.id,
      attempt: 2,
      rolled_back: false,
      requested_by: "admin",
    });

    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NodeOperationsPanel },
      setup: () => ({ node }),
      template: `
        <n-dialog-provider>
          <n-message-provider>
            <node-operations-panel :node="node" />
          </n-message-provider>
        </n-dialog-provider>
      `,
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();

    expect(wrapper.text()).toContain("core_restart_failed");
    expect(wrapper.text()).toContain("已恢复最近可用配置");
    expect(wrapper.text()).toContain(backup.local_path);

    const retryButton = wrapper.findAll("button").find((button) => button.text().trim() === "重试");
    expect(retryButton).toBeDefined();
    await retryButton!.trigger("click");
    await flushPromises();

    expect(api.retryNodeOperation).toHaveBeenCalledWith("node-1", "operation-1");
    expect(api.listNodeOperations).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });

  it("confirms and submits a bounded Reality identity rotation", async () => {
    const realityNode = {
      ...node,
      id: "node-reality",
      name: "Reality LA",
      adapter_type: "sing_box_vless_reality",
      agent_installation_id: "install-1",
      desired_version: 7,
      applied_version: 7,
      reality: {
        handshake_server: "www.microsoft.com",
        handshake_port: 443,
        key_generation: 2,
        applied_key_generation: 2,
        public_key: "public-key",
        short_id: "0123456789abcdef",
        material_applied_version: 7,
        material_reported_at: "2026-08-12T08:00:00Z",
      },
    } as NodeRecord;
    vi.spyOn(api, "listNodeOperations").mockResolvedValue([]);
    vi.spyOn(api, "listConfigBackups").mockResolvedValue([]);
    const rotate = vi.spyOn(api, "rotateRealityIdentity").mockResolvedValue({
      ...realityNode,
      desired_version: 8,
      status: "pending",
      reality: { ...realityNode.reality!, key_generation: 3 },
    });

    const host = defineComponent({
      components: { NDialogProvider, NMessageProvider, NodeOperationsPanel },
      setup: () => ({ realityNode }),
      template: `
        <n-dialog-provider>
          <n-message-provider>
            <node-operations-panel :node="realityNode" />
          </n-message-provider>
        </n-dialog-provider>
      `,
    });
    const wrapper = mount(host, { attachTo: document.body });
    await flushPromises();

    const trigger = wrapper.find('button[aria-label="轮换 Reality 身份"]');
    expect(trigger.exists()).toBe(true);
    expect(trigger.attributes("disabled")).toBeUndefined();
    await trigger.trigger("click");
    await flushPromises();

    const confirm = Array.from(document.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "轮换",
    ) as HTMLButtonElement | undefined;
    expect(confirm).toBeDefined();
    confirm!.click();
    await flushPromises();

    expect(rotate).toHaveBeenCalledWith("node-reality", 2, 7);
    wrapper.unmount();
  });
});
