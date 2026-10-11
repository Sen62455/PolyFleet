import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import type { NodeRecord } from "../../types";
import NodeFormModal from "./NodeFormModal.vue";

describe("NodeFormModal", () => {
  it("submits TLS certificate and public-key pins with the endpoint", async () => {
    const wrapper = mount(NodeFormModal, {
      attachTo: document.body,
      props: { show: true, node: null, saving: false },
      global: { stubs: { teleport: true } },
    });
    await flushPromises();

    await wrapper.find('input[placeholder="例如：LisaHost"]').setValue("Pinned node");
    await wrapper.find('input[placeholder="AA:BB:CC:..."]').setValue("AB:".repeat(31) + "AB");
    await wrapper.find('input[placeholder="Base64 SHA-256"]').setValue("QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=");
    const addButton = wrapper.findAll("button").find((button) => button.text().trim() === "添加");
    expect(addButton).toBeDefined();
    await addButton!.trigger("click");
    await flushPromises();

    expect(wrapper.emitted("submit")?.[0]?.[0]).toMatchObject({
      name: "Pinned node",
      tls_cert_fingerprint: "AB:".repeat(31) + "AB",
      tls_public_key_sha256: "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=",
      exit_proxy_id: "",
    });
    wrapper.unmount();
  });

  it("shows and submits only the bounded Reality endpoint fields", async () => {
    const realityNode = {
      id: "node-reality",
      name: "Reality node",
      provider: "Lisa",
      region: "Los Angeles",
      adapter_type: "sing_box_vless_reality",
      public_host: "reality.example.com",
      public_port: 8443,
      sni: "www.cloudflare.com",
      tls_insecure: true,
      tls_cert_fingerprint: "legacy-pin",
      tls_public_key_sha256: "legacy-public-key-pin",
      reality: {
        handshake_server: "www.cloudflare.com",
        handshake_port: 443,
        key_generation: 1,
        applied_key_generation: 1,
        public_key: "public-key",
        short_id: "0123456789abcdef",
        material_applied_version: 2,
        material_reported_at: "2026-08-12T08:00:00Z",
      },
      traffic_limit_bytes: 2 * 1024 ** 4,
      traffic_reset_day: 15,
      enabled: true,
    } as NodeRecord;
    const wrapper = mount(NodeFormModal, {
      attachTo: document.body,
      props: { show: true, node: realityNode, saving: false },
      global: { stubs: { teleport: true } },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("TCP 端口");
    expect(wrapper.text()).toContain("Reality SNI / 伪装域名");
    expect(wrapper.text()).toContain("Reality 握手服务器");
    expect(wrapper.text()).not.toContain("跳过证书验证");
    expect(wrapper.find('input[placeholder="AA:BB:CC:..."]').exists()).toBe(false);
    expect(wrapper.find('input[placeholder="Base64 SHA-256"]').exists()).toBe(false);

    await wrapper.find('input[placeholder="用于 Reality 握手的公网 DNS 域名"]').setValue("  www.microsoft.com  ");
    const saveButton = wrapper.findAll("button").find((button) => button.text().trim() === "保存");
    expect(saveButton).toBeDefined();
    await saveButton!.trigger("click");
    await flushPromises();

    expect(wrapper.text()).toContain("链式出口");
    expect(wrapper.emitted("submit")?.[0]?.[0]).toMatchObject({
      adapter_type: "sing_box_vless_reality",
      public_port: 8443,
      tls_insecure: false,
      tls_cert_fingerprint: "",
      tls_public_key_sha256: "",
      reality: {
        handshake_server: "www.microsoft.com",
        handshake_port: 443,
      },
      traffic_limit_bytes: 2 * 1024 ** 4,
      traffic_reset_day: 15,
    });
    wrapper.unmount();
  });
});
