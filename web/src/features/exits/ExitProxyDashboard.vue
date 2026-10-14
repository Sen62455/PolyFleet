<script setup lang="ts">
import SurfaceState from "../../components/SurfaceState.vue";
import { computed, onMounted, ref } from "vue";
import {
  Cable,
  Check,
  Copy as CopyIcon,
  Globe2,
  Link2,
  LockKeyhole,
  BookOpen,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  Trash2,
} from "@lucide/vue";
import {
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpin,
  NSwitch,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import StatCard from "../../components/StatCard.vue";
import IconAction from "../../components/IconAction.vue";
import { api, APIError } from "../../api";
import type {
  ExitProxyInput,
  ExitProxyKind,
  ExitProxyProtocol,
  ExitProxyRecord,
  LandingGuide,
  NodeRecord,
} from "../../types";

const emit = defineEmits<{ "session-expired": []; "nodes-changed": [] }>();
const message = useMessage();
const dialog = useDialog();
const proxies = ref<ExitProxyRecord[]>([]);
const nodes = ref<NodeRecord[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
const saving = ref(false);
const modalOpen = ref(false);
const editingID = ref("");
const name = ref("");
const kind = ref<ExitProxyKind>("rented_ip");
const protocol = ref<ExitProxyProtocol>("socks5");
const host = ref("");
const port = ref(1080);
const username = ref("");
const password = ref("");
const tls = ref(false);
const skipCertVerify = ref(false);
const udp = ref(true);
const serverSide = ref(false);
const notes = ref("");
const nodeIDs = ref<string[]>([]);
const guideOpen = ref(false);
const guide = ref<LandingGuide | null>(null);
const guideLoading = ref(false);
const copied = ref("");

const rentedCount = computed(() => proxies.value.filter((item) => item.kind === "rented_ip").length);
const landingCount = computed(() => proxies.value.filter((item) => item.kind === "landing_vps").length);
const attachedCount = computed(() => proxies.value.reduce((sum, item) => sum + item.assignments.length, 0));
const kindOptions = [
  { label: "租用 Proxy IP 出口", value: "rented_ip" },
  { label: "落地 VPS", value: "landing_vps" },
];
const protocolOptions = computed(() => {
  const socks = { label: "SOCKS5", value: "socks5" };
  const http = { label: tls.value ? "HTTPS" : "HTTP", value: "http" };
  return [socks, http];
});
const nodeOptions = computed(() => nodes.value.map((node) => ({
  label: `${node.name}${node.public_host ? ` · ${node.public_host}` : ""}`,
  value: node.id,
})));

function handleError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return;
  }
  message.error(error instanceof APIError ? error.message : fallback);
}

async function load(silent = false) {
  if (refreshing.value) return;
  if (!silent) loading.value = proxies.value.length === 0;
  refreshing.value = true;
  loadError.value = "";
  try {
    const [nextProxies, nextNodes] = await Promise.all([api.listExitProxies(), api.listNodes()]);
    proxies.value = nextProxies;
    nodes.value = nextNodes;
  } catch (error) {
    loadError.value = error instanceof APIError ? error.message : "出口代理加载失败。";
    if (error instanceof APIError && error.status === 401) emit("session-expired");
  } finally {
    loading.value = false;
    refreshing.value = false;
  }
}

function resetForm() {
  editingID.value = "";
  name.value = "";
  kind.value = "rented_ip";
  protocol.value = "socks5";
  host.value = "";
  port.value = 1080;
  username.value = "";
  password.value = "";
  tls.value = false;
  skipCertVerify.value = false;
  udp.value = true;
  serverSide.value = false;
  notes.value = "";
  nodeIDs.value = [];
}

function openCreate() {
  resetForm();
  modalOpen.value = true;
}

function openEdit(item: ExitProxyRecord) {
  editingID.value = item.id;
  name.value = item.name;
  kind.value = item.kind;
  protocol.value = item.protocol;
  host.value = item.host;
  port.value = item.port;
  username.value = item.username;
  password.value = "";
  tls.value = item.tls;
  skipCertVerify.value = item.skip_cert_verify;
  udp.value = item.udp;
  serverSide.value = item.server_side ?? false;
  notes.value = item.notes;
  nodeIDs.value = item.assignments.map((assignment) => assignment.node_id);
  modalOpen.value = true;
}

function onKindChange(next: ExitProxyKind) {
  if (next === "landing_vps" && protocol.value === "http") {
    protocol.value = "socks5";
    tls.value = false;
    skipCertVerify.value = false;
    udp.value = true;
  }
  if (next === "landing_vps" && !username.value) username.value = "polyfleet";
}

function onProtocolChange(next: ExitProxyProtocol) {
  if (next === "http") {
    udp.value = false;
    if (port.value === 1080) port.value = tls.value ? 443 : 8080;
  } else {
    tls.value = false;
    skipCertVerify.value = false;
    udp.value = true;
    if (port.value === 8080 || port.value === 443) port.value = 1080;
  }
}

function payload(): ExitProxyInput {
  const input: ExitProxyInput = {
    server_side: serverSide.value,
    name: name.value.trim(),
    kind: kind.value,
    protocol: protocol.value,
    host: host.value.trim(),
    port: port.value,
    username: username.value.trim(),
    tls: protocol.value === "http" ? tls.value : false,
    skip_cert_verify: protocol.value === "http" && tls.value ? skipCertVerify.value : false,
    udp: protocol.value === "socks5" ? udp.value : false,
    notes: notes.value.trim(),
    node_ids: [...nodeIDs.value],
  };
  if (password.value.trim()) input.password = password.value.trim();
  return input;
}

async function save() {
  if (!name.value.trim() || !host.value.trim()) {
    message.warning("请填写名称和出口地址。");
    return;
  }
  saving.value = true;
  try {
    const saved = editingID.value
      ? await api.updateExitProxy(editingID.value, payload())
      : await api.createExitProxy(payload());
    modalOpen.value = false;
    await load(true);
    emit("nodes-changed");
    message.success(editingID.value ? "出口代理已更新" : "出口代理已添加");
    if (saved.kind === "landing_vps") {
      await openGuide(saved);
    }
  } catch (error) {
    handleError(error, "出口代理保存失败。");
  } finally {
    saving.value = false;
  }
}

function remove(item: ExitProxyRecord) {
  const assigned = item.assignments.length
    ? `有 ${item.assignments.length} 台入口节点正在使用它，删除后订阅会取消出口标记；入口机上的转发配置需另行移除。`
    : "删除后订阅不再包含这条链式出口。";
  dialog.warning({
    title: "删除出口代理",
    content: `确认删除“${item.name}”？${assigned}`,
    positiveText: "删除",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.deleteExitProxy(item.id);
        await load(true);
        emit("nodes-changed");
        message.success("出口代理已删除");
      } catch (error) {
        handleError(error, "出口代理删除失败。");
        return false;
      }
      return true;
    },
  });
}

async function openGuide(item: ExitProxyRecord) {
  if (item.kind !== "landing_vps") return;
  guideLoading.value = true;
  guideOpen.value = true;
  guide.value = null;
  try {
    guide.value = await api.getExitProxyLandingGuide(item.id);
  } catch (error) {
    handleError(error, "落地机指引加载失败。");
    guideOpen.value = false;
  } finally {
    guideLoading.value = false;
  }
}

async function copyText(value: string, key: string) {
  try {
    await navigator.clipboard.writeText(value);
    copied.value = key;
    window.setTimeout(() => {
      if (copied.value === key) copied.value = "";
    }, 1800);
  } catch {
    message.error("复制失败，请手动选择文本。");
  }
}

function kindLabel(value: ExitProxyKind) {
  return value === "landing_vps" ? "落地 VPS" : "租用 IP";
}

function protocolLabel(item: ExitProxyRecord) {
  if (item.protocol === "http") return item.tls ? "HTTPS" : "HTTP";
  return item.udp ? "SOCKS5 · UDP" : "SOCKS5";
}

function endpointLabel(item: ExitProxyRecord) {
  return `${item.host}:${item.port}`;
}

onMounted(() => load());
</script>

<template>
  <main class="workspace operations-workspace exit-workspace">
    <div class="page-heading page-heading--console">
      <div>
        <span class="page-eyebrow">EXIT ROUTING</span>
        <h2>出口代理</h2>
        <p>通过租用 IP 或落地机出网。支持客户端链式连接，或由已配置好的入口服务器转发。</p>
      </div>
      <div class="page-heading__actions">
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button circle secondary :loading="refreshing" aria-label="刷新出口代理" @click="load()">
              <template #icon><n-icon><refresh-cw /></n-icon></template>
            </n-button>
          </template>
          刷新
        </n-tooltip>
        <n-button type="primary" @click="openCreate">
          <template #icon><n-icon><plus /></n-icon></template>
          添加出口
        </n-button>
      </div>
    </div>

    <section v-if="!loading" class="stat-grid" aria-label="出口摘要">
      <stat-card label="出口总数" :muted="!proxies.length"><template #icon><cable :size="18" /></template>{{ proxies.length }}<template #hint>未配置时节点保持直出</template></stat-card>
      <stat-card label="租用 IP" :muted="!rentedCount"><template #icon><globe-2 :size="18" /></template>{{ rentedCount }}<template #hint>服务商提供的 SOCKS5 / HTTP</template></stat-card>
      <stat-card label="落地 VPS" :muted="!landingCount"><template #icon><server :size="18" /></template>{{ landingCount }}<template #hint>可生成安装与防火墙指引</template></stat-card>
      <stat-card label="已挂入口" :muted="!attachedCount"><template #icon><link2 :size="18" /></template>{{ attachedCount }}<template #hint>客户端链式或入口服务器转发</template></stat-card>
    </section>

    <surface-state v-if="loadError" kind="error" :hint="loadError" :busy="refreshing" @action="load()" />
    <section class="exit-list" aria-label="出口列表">
      <surface-state v-if="loading" kind="loading" stats />
      <div v-else-if="!proxies.length && !loadError" class="surface-state surface-state--empty">
        <cable :size="26" />
        <strong>尚未配置出口代理</strong>
        <p>通过租用 IP 或落地机出网，未配置时节点保持直出。</p>
        <button type="button" class="rack-add exit-empty-add" @click="openCreate"><plus :size="20" /><span>添加出口</span></button>
      </div>
      <div v-else class="exit-cards">
        <article v-for="item in proxies" :key="item.id" class="egress-card">
          <header class="egress-card__head">
            <div><h3>{{ item.name }}</h3><div class="egress-card__tags"><span>{{ kindLabel(item.kind) }}</span><span>{{ item.server_side ? '入口服务器转发' : '客户端链式' }}</span><span class="mono">{{ protocolLabel(item) }}</span></div></div>
            <div class="egress-card__actions"><span class="pill pill--neutral">{{ item.assignments.length ? `${item.assignments.length} 台入口` : '未挂入口' }}</span>
            <icon-action label="编辑出口" @click="openEdit(item)"><n-icon><pencil /></n-icon></icon-action>
            <icon-action v-if="item.kind === 'landing_vps'" label="落地机指引" @click="openGuide(item)"><n-icon><book-open /></n-icon></icon-action>
            <icon-action label="删除出口代理" tone="danger" @click="remove(item)"><n-icon><trash-2 /></n-icon></icon-action>
          </div>
          </header>
          <dl class="egress-card__facts">
            <div><dt>出口地址</dt><dd class="egress-card__endpoint"><span class="mono" :title="endpointLabel(item)">{{ endpointLabel(item) }}</span><n-button size="tiny" quaternary :aria-label="`复制 ${item.name} 出口地址`" @click="copyText(endpointLabel(item), `endpoint:${item.id}`)"><template #icon><n-icon><check v-if="copied === `endpoint:${item.id}`" /><copy-icon v-else /></n-icon></template></n-button></dd></div>
            <div><dt>认证</dt><dd>{{ item.username || '无用户名' }}<small class="egress-card__auth"><lock-keyhole :size="12" />{{ item.password_set ? '已保存密码' : '无密码' }}</small></dd></div>
            <div><dt>挂接节点</dt><dd class="egress-card__nodes"><span v-for="assignment in item.assignments" :key="assignment.node_id" class="chip">{{ assignment.node_name }}</span><span v-if="!item.assignments.length">节点仍直出</span></dd></div>
          </dl>
          <p v-if="item.notes" class="egress-card__notes">{{ item.notes }}</p>

        </article>
      </div>
    </section>

    <n-modal
      :show="modalOpen"
      preset="card"
      class="exit-form-modal compact-dialog"
      :title="editingID ? '编辑出口代理' : '添加出口代理'"
      :bordered="false"
      :mask-closable="!saving"
      :segmented="{ content: true }"
      :style="{ width: 'min(680px, calc(100vw - 32px))' }"
      content-style="max-height: min(58vh, 460px); overflow-y: auto;"
      @update:show="!$event && (modalOpen = false)"
    >
      <n-form label-placement="top" class="compact-form" @submit.prevent="save">
        <n-form-item label="名称">
          <n-input v-model:value="name" maxlength="64" placeholder="例如：住宅出口 A 或 伦敦落地" />
        </n-form-item>
        <div class="form-grid">
          <n-form-item label="出口类型">
            <n-select v-model:value="kind" :options="kindOptions" @update:value="onKindChange" />
          </n-form-item>
          <n-form-item label="协议">
            <n-select v-model:value="protocol" :options="protocolOptions" @update:value="onProtocolChange" />
          </n-form-item>
        </div>
        <p class="exit-hint">
          {{ kind === "landing_vps"
            ? "落地机是另一台 VPS。保存后会给出 sing-box 安装、防火墙和验证命令；用户仍然连接 PolyFleet 入口。"
            : "把服务商提供的静态/动态 Proxy IP 填进来。订阅会经入口节点再连到这个出口，网站看到的是该 IP。" }}
        </p>
        <div class="form-grid form-grid--endpoint">
          <n-form-item label="主机 / IP">
            <n-input v-model:value="host" maxlength="253" placeholder="建议填 IP，例如 203.0.113.9" />
          </n-form-item>
          <n-form-item label="端口">
            <n-input-number v-model:value="port" :min="1" :max="65535" :precision="0" />
          </n-form-item>
        </div>
        <div class="form-grid">
          <n-form-item :label="kind === 'landing_vps' ? '落地用户名' : '用户名（可选）'">
            <n-input v-model:value="username" maxlength="128" :placeholder="kind === 'landing_vps' ? '默认 polyfleet' : '服务商账号'" />
          </n-form-item>
          <n-form-item :label="editingID ? '密码（留空则保持原值）' : (kind === 'landing_vps' ? '密码（留空则自动生成）' : '密码（可选）')">
            <n-input v-model:value="password" type="password" show-password-on="click" maxlength="256" />
          </n-form-item>
        </div>
        <div v-if="protocol === 'http'" class="switch-row switch-row--compact">
          <div><strong>HTTPS 代理</strong><span>CONNECT 使用 TLS</span></div>
          <n-switch v-model:value="tls" aria-label="HTTPS 代理" />
        </div>
        <div v-if="protocol === 'http' && tls" class="switch-row switch-row--compact">
          <div><strong>跳过证书验证</strong></div>
          <n-switch v-model:value="skipCertVerify" aria-label="跳过证书验证" />
        </div>
        <div v-if="protocol === 'socks5'" class="switch-row switch-row--compact">
          <div><strong>UDP</strong><span>HTTP 出口没有 UDP</span></div>
          <n-switch v-model:value="udp" aria-label="SOCKS5 UDP" />
        </div>
        <div class="switch-row switch-row--compact">
          <div><strong>入口服务器转发</strong><span>入口机已配置此出口时开启，减少客户端跨洋协商。此开关仅改变订阅，不会自动部署服务器；修改出口地址或密码后也需同步入口机。</span></div>
          <n-switch v-model:value="serverSide" aria-label="入口服务器转发" />
        </div>
        <n-form-item label="备注">
          <n-input v-model:value="notes" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" maxlength="512" placeholder="供应商、到期日或仅允许入口 IP 访问等" />
        </n-form-item>
        <n-form-item label="挂到这些入口节点">
          <n-checkbox-group v-if="nodeOptions.length" v-model:value="nodeIDs">
            <div class="exit-node-list">
              <n-checkbox v-for="option in nodeOptions" :key="option.value" :value="option.value" :label="option.label" />
            </div>
          </n-checkbox-group>
          <span v-else class="exit-hint">还没有节点。先在节点页添加入口，再回来挂接。</span>
        </n-form-item>
        <div class="modal-actions">
          <n-button :disabled="saving" @click="modalOpen = false">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="saving">{{ editingID ? "保存" : "添加" }}</n-button>
        </div>
      </n-form>
    </n-modal>

    <n-modal
      :show="guideOpen"
      preset="card"
      class="exit-guide-modal"
      title="落地机配置指引"
      :bordered="false"
      :style="{ width: 'calc(100% - 32px)', maxWidth: '760px', maxHeight: 'min(780px, calc(100vh - 32px))' }"
      content-style="overflow-y: auto"
      @update:show="!$event && (guideOpen = false)"
    >
      <div v-if="guideLoading" class="surface-state"><n-spin :size="28" /></div>
      <div v-else-if="guide" class="exit-guide">
        <p>适用于 Debian / Ubuntu 全新落地机。按步骤标注的机器执行，命令块请整段复制；出现错误先处理，再继续下一步。完成后，Clash / sing-box 订阅会经入口再从 <code>{{ guide.listen }}</code> 出网。</p>
        <article v-for="(step, index) in guide.steps" :key="step.title" class="exit-guide__step">
          <header>
            <span>{{ String(index + 1).padStart(2, "0") }}</span>
            <div>
              <strong>{{ step.title }}</strong>
              <small class="exit-guide__where">执行位置：{{ step.where }}</small>
              <small>{{ step.body }}</small>
              <small v-if="step.filename">配置文件：<code>{{ step.filename }}</code></small>
            </div>
            <n-button v-if="step.copy" size="tiny" secondary @click="copyText(step.copy, step.title)">
              <template #icon><n-icon><check v-if="copied === step.title" /><copy-icon v-else /></n-icon></template>
              {{ copied === step.title ? "已复制" : "复制" }}
            </n-button>
          </header>
          <pre v-if="step.copy"><code>{{ step.copy }}</code></pre>
          <p class="exit-guide__expected">成功标志：{{ step.expected }}</p>
        </article>
      </div>
    </n-modal>
  </main>
</template>

<style scoped>
.exit-workspace { display: grid; align-content: start; grid-auto-rows: max-content; gap: 24px; }
.exit-cards { display: grid; gap: 14px; }
.exit-hint { margin: 0 0 12px; color: var(--hf-muted); font-size: 12px; line-height: 1.55; }
.exit-node-list { display: grid; gap: 8px; }
.exit-guide { display: grid; gap: 16px; }
.exit-guide > p { margin: 0; color: var(--hf-muted); font-size: 13px; line-height: 1.6; }
.exit-guide__step { display: grid; gap: 10px; }
.exit-guide__step > *, .exit-guide__step header > div { min-width: 0; }
.exit-guide__step code { overflow-wrap: anywhere; }
.exit-guide__expected { margin: 0; padding: 10px 12px; border-left: 3px solid var(--hf-line); color: var(--hf-muted); font-size: 12px; line-height: 1.6; }
.exit-guide__step .exit-guide__where { color: var(--hf-ink); font-weight: 600; }
.exit-guide__step header { display: grid; grid-template-columns: auto 1fr auto; gap: 12px; align-items: start; }
.exit-guide__step header span {
  color: var(--hf-muted);
  font-family: var(--hf-data);
  font-size: 12px;
  font-weight: 620;
}
.exit-guide__step small { display: block; margin-top: 4px; color: var(--hf-muted); font-size: 12px; line-height: 1.5; }
.exit-guide__step pre {
  margin: 0;
  padding: 12px 14px;
  overflow: auto;
  border: 1px solid var(--hf-line);
  background: color-mix(in srgb, var(--hf-ink) 4%, transparent);
  color: var(--hf-ink);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
}
@media (max-width: 860px) {
  .exit-card dl { grid-template-columns: 1fr; }
  .exit-card header, .exit-card footer { align-items: flex-start; flex-wrap: wrap; }
}
</style>
