<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import {
  NButton,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch,
  type FormInst,
  type FormRules,
} from "naive-ui";
import type { AdapterType, ExitProxyRecord, NodeInput, NodeRealityInput, NodeRecord } from "../../types";

const props = withDefaults(defineProps<{
  show: boolean;
  node: NodeRecord | null;
  saving: boolean;
  exitProxies?: ExitProxyRecord[];
}>(), { exitProxies: () => [] });

const emit = defineEmits<{
  "update:show": [show: boolean];
  submit: [input: Required<NodeInput>];
  "manage-exits": [];
}>();

const formRef = ref<FormInst | null>(null);
type NodeFormModel = Omit<Required<NodeInput>, "reality"> & { reality: NodeRealityInput };
type TrafficUnit = "GiB" | "TiB";

const form = reactive<NodeFormModel>({
  name: "",
  provider: "",
  region: "",
  adapter_type: "native_hysteria2",
  public_host: "",
  public_port: 443,
  sni: "",
  tls_insecure: false,
  tls_cert_fingerprint: "",
  tls_public_key_sha256: "",
  reality: {
    handshake_server: "",
    handshake_port: 443,
  },
  traffic_limit_bytes: 0,
  traffic_reset_day: 1,
  enabled: true,
  exit_proxy_id: "",
});
const trafficLimitValue = ref(0);
const trafficLimitUnit = ref<TrafficUnit>("TiB");

const title = computed(() => (props.node ? "编辑节点" : "添加节点"));
const adapterLocked = computed(() => Boolean(props.node?.agent_installation_id));
const isReality = computed(() => form.adapter_type === "sing_box_vless_reality");
const adapterOptions = [
  { label: "原生 Hysteria2（推荐）", value: "native_hysteria2" },
  { label: "VLESS + Reality（sing-box · 实验）", value: "sing_box_vless_reality" },
  { label: "独立 sing-box（迁移兼容）", value: "standalone_sing_box" },
  { label: "S-UI（迁移兼容）", value: "s_ui" },
];
const trafficUnitOptions = [
  { label: "GiB", value: "GiB" },
  { label: "TiB", value: "TiB" },
];
const trafficUnitBytes: Record<TrafficUnit, number> = {
  GiB: 1024 ** 3,
  TiB: 1024 ** 4,
};
const exitProxyOptions = computed(() => [
  { label: "不使用（节点直出）", value: "" },
  ...props.exitProxies.map((proxy) => ({
    label: `${proxy.name} · ${proxy.kind === "landing_vps" ? "落地 VPS" : "租用 IP"} · ${proxy.host}:${proxy.port}`,
    value: proxy.id,
  })),
]);
const selectedExit = computed(() => props.exitProxies.find((proxy) => proxy.id === form.exit_proxy_id) ?? null);
const rules: FormRules = {
  name: [
    { required: true, message: "请输入节点名称", trigger: "blur" },
    { min: 2, max: 64, message: "名称长度为 2 到 64 个字符", trigger: "blur" },
  ],
  adapter_type: [{ required: true, message: "请选择适配器", trigger: "change" }],
  public_host: [{
    validator: (_rule, value: string) => !isReality.value || Boolean(value?.trim()),
    message: "请输入 Reality 公网域名或 IP",
    trigger: ["blur", "input"],
  }],
  sni: [{
    validator: (_rule, value: string) => !isReality.value || Boolean(value?.trim()),
    message: "请输入 Reality SNI / 伪装域名",
    trigger: ["blur", "input"],
  }],
  "reality.handshake_server": [{
    validator: (_rule, value: string) => !isReality.value || Boolean(value?.trim()),
    message: "请输入 Reality 握手服务器",
    trigger: ["blur", "input"],
  }],
};

watch(
  () => [props.show, props.node] as const,
  ([show, node]) => {
    if (!show) return;
    form.name = node?.name ?? "";
    form.provider = node?.provider ?? "";
    form.region = node?.region ?? "";
    form.adapter_type = (node?.adapter_type ?? "native_hysteria2") as AdapterType;
    form.public_host = node?.public_host ?? "";
    form.public_port = node?.public_port ?? 443;
    form.sni = node?.sni ?? "";
    form.tls_insecure = node?.tls_insecure ?? false;
    form.tls_cert_fingerprint = node?.tls_cert_fingerprint ?? "";
    form.tls_public_key_sha256 = node?.tls_public_key_sha256 ?? "";
    form.reality.handshake_server = node?.reality?.handshake_server ?? "";
    form.reality.handshake_port = node?.reality?.handshake_port ?? 443;
    form.traffic_limit_bytes = node?.traffic_limit_bytes ?? 0;
    form.traffic_reset_day = node?.traffic_reset_day ?? 1;
    form.exit_proxy_id = node?.exit_proxy_id ?? node?.exit_proxy?.id ?? "";
    trafficLimitUnit.value = (node?.traffic_limit_bytes ?? 0) >= 1024 ** 4 ? "TiB" : "GiB";
    trafficLimitValue.value = (node?.traffic_limit_bytes ?? 0) / trafficUnitBytes[trafficLimitUnit.value];
    form.enabled = node?.enabled ?? true;
    formRef.value?.restoreValidation();
  },
  { immediate: true },
);

async function submit() {
  try {
    await formRef.value?.validate();
  } catch {
    return;
  }
  emit("submit", {
    name: form.name.trim(),
    provider: form.provider.trim(),
    region: form.region.trim(),
    adapter_type: form.adapter_type,
    public_host: form.public_host.trim(),
    public_port: form.public_port,
    sni: form.sni.trim(),
    tls_insecure: isReality.value ? false : form.tls_insecure,
    tls_cert_fingerprint: isReality.value ? "" : form.tls_cert_fingerprint.trim(),
    tls_public_key_sha256: isReality.value ? "" : form.tls_public_key_sha256.trim(),
    reality: isReality.value
      ? {
          handshake_server: form.reality.handshake_server.trim(),
          handshake_port: 443,
        }
      : null,
    traffic_limit_bytes: Math.round(trafficLimitValue.value * trafficUnitBytes[trafficLimitUnit.value]),
    traffic_reset_day: form.traffic_reset_day,
    enabled: form.enabled,
    exit_proxy_id: form.exit_proxy_id,
  });
}
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    :title="title"
    class="node-form-modal compact-dialog"
    :bordered="false"
    :mask-closable="!saving"
    :close-on-esc="!saving"
    :segmented="{ content: true, footer: true }"
    :style="{ width: 'min(880px, calc(100vw - 32px))' }"
    content-style="max-height: min(58vh, 460px); overflow-y: auto;"
    @update:show="emit('update:show', $event)"
  >
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="top" class="compact-form">
      <div class="node-form-layout">
        <section>
          <div class="form-section-label">节点身份</div>
          <div class="form-grid">
            <n-form-item label="名称" path="name">
              <n-input v-model:value="form.name" maxlength="64" placeholder="例如：LisaHost" autofocus />
            </n-form-item>
            <n-form-item label="节点适配器" path="adapter_type">
              <n-select
                v-model:value="form.adapter_type"
                :options="adapterOptions"
                :disabled="adapterLocked"
              />
              <template v-if="adapterLocked" #feedback>Agent 注册后不可更换适配器。</template>
            </n-form-item>
          </div>
          <div class="form-grid">
            <n-form-item label="服务商" path="provider">
              <n-input v-model:value="form.provider" maxlength="64" placeholder="例如：Lisa" />
            </n-form-item>
            <n-form-item label="地区" path="region">
              <n-input v-model:value="form.region" maxlength="64" placeholder="例如：Los Angeles" />
            </n-form-item>
          </div>
          <div class="form-section-label">订阅端点</div>
          <div class="form-grid form-grid--endpoint">
            <n-form-item label="公网域名或 IP" path="public_host">
              <n-input v-model:value="form.public_host" maxlength="253" placeholder="hy2.example.com" />
            </n-form-item>
            <n-form-item :label="isReality ? 'TCP 端口' : 'UDP 端口'" path="public_port">
              <n-input-number v-model:value="form.public_port" :min="1" :max="65535" :precision="0" />
            </n-form-item>
          </div>
          <n-form-item :label="isReality ? 'Reality SNI / 伪装域名' : 'TLS SNI'" path="sni">
            <n-input
              v-model:value="form.sni"
              maxlength="253"
              :placeholder="isReality ? '例如：www.cloudflare.com' : '留空时使用公网域名'"
            />
          </n-form-item>
          <template v-if="isReality">
            <div class="form-grid form-grid--endpoint">
              <n-form-item label="Reality 握手服务器" path="reality.handshake_server">
                <n-input
                  v-model:value="form.reality.handshake_server"
                  maxlength="253"
                  placeholder="用于 Reality 握手的公网 DNS 域名"
                />
              </n-form-item>
              <n-form-item label="握手端口" path="reality.handshake_port">
                <n-input-number :value="443" :min="443" :max="443" :precision="0" disabled />
              </n-form-item>
            </div>
          </template>
          <div v-if="!isReality" class="switch-row switch-row--compact">
            <div><strong>跳过证书验证</strong></div>
            <n-switch v-model:value="form.tls_insecure" aria-label="跳过证书验证" />
          </div>
          <div v-if="!isReality" class="form-grid">
            <n-form-item label="证书 SHA-256 指纹" path="tls_cert_fingerprint">
              <n-input
                v-model:value="form.tls_cert_fingerprint"
                maxlength="95"
                placeholder="AA:BB:CC:..."
              />
            </n-form-item>
            <n-form-item label="公钥 SHA-256（Base64）" path="tls_public_key_sha256">
              <n-input
                v-model:value="form.tls_public_key_sha256"
                maxlength="44"
                placeholder="Base64 SHA-256"
              />
            </n-form-item>
          </div>
        </section>
        <section>
          <div class="form-section-label">出口代理（可选）</div>
          <n-form-item label="链式出口" path="exit_proxy_id">
            <n-select
              v-model:value="form.exit_proxy_id"
              :options="exitProxyOptions"
              placeholder="不使用（节点直出）"
            />
            <template #feedback>
              {{ selectedExit
                ? `Clash / sing-box 会经本节点再从 ${selectedExit.host}:${selectedExit.port} 出网；URI / Base64 仍直连本节点。`
                : "不配置时，订阅把本节点当作出口 IP。" }}
            </template>
          </n-form-item>
          <n-button text type="primary" attr-type="button" @click="emit('manage-exits')">管理出口代理与落地机指引</n-button>
          <div class="form-section-label">月流量额度</div>
          <div class="form-grid form-grid--endpoint">
            <n-form-item label="节点总额度（双向）" path="traffic_limit_bytes">
              <n-input-number
                v-model:value="trafficLimitValue"
                :min="0"
                :max="8388607"
                :precision="2"
                placeholder="0 表示不限额"
              >
                <template #suffix>
                  <n-select
                    v-model:value="trafficLimitUnit"
                    :options="trafficUnitOptions"
                    :consistent-menu-width="false"
                    aria-label="流量额度单位"
                  />
                </template>
              </n-input-number>
              <template #feedback>上传与下载合计；0 表示不限额。</template>
            </n-form-item>
            <n-form-item label="每月重置日（UTC）" path="traffic_reset_day">
              <n-input-number
                v-model:value="form.traffic_reset_day"
                :min="1"
                :max="31"
                :precision="0"
              />
              <template #feedback>短月份自动使用当月最后一天。</template>
            </n-form-item>
          </div>
          <div class="switch-row">
            <div>
              <strong>启用节点</strong>
              <span>停用后仍保留节点与历史状态</span>
            </div>
            <n-switch v-model:value="form.enabled" aria-label="启用节点" />
          </div>
        </section>
      </div>
    </n-form>
    <template #footer>
      <div class="modal-actions">
        <n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="saving" @click="submit">{{ node ? "保存" : "添加" }}</n-button>
      </div>
    </template>
  </n-modal>
</template>
