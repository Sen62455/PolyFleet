<script setup lang="ts">
import StatusPill from "../../components/StatusPill.vue";
import { KeyRound, Pencil } from "@lucide/vue";
import { NButton, NDrawer, NDrawerContent, NIcon, NTag } from "naive-ui";
import MetricBar from "../../components/MetricBar.vue";
import StatusIndicator from "../../components/StatusIndicator.vue";
import {
  adapterLabels,
  formatBytes,
  formatPercent,
  formatRate,
  formatUptime,
  percent,
  relativeTime,
} from "../../lib/format";
import type { NodeRecord } from "../../types";
import SUIAdapterPanel from "./SUIAdapterPanel.vue";
import NodeOperationsPanel from "./NodeOperationsPanel.vue";

defineProps<{ show: boolean; node: NodeRecord | null }>();
const emit = defineEmits<{
  "update:show": [show: boolean];
  edit: [node: NodeRecord];
  enroll: [node: NodeRecord];
  changed: [];
  "session-expired": [];
}>();

function endpointLabel(node: NodeRecord) {
  if (!node.public_host) return "未配置";
  const host = node.public_host.includes(":") ? `[${node.public_host}]` : node.public_host;
  return `${host}:${node.public_port}`;
}

function realityHandshakeLabel(node: NodeRecord) {
  if (!node.reality?.handshake_server) return "未配置";
  return `${node.reality.handshake_server}:${node.reality.handshake_port || 443}`;
}
</script>

<template>
  <n-drawer
    :show="show"
    width="min(480px, 100vw)"
    placement="right"
    @update:show="emit('update:show', $event)"
  >
    <n-drawer-content v-if="node" :title="node.name" closable>
      <div class="detail-status-line">
        <status-indicator :status="node.status" />
        <span>{{ adapterLabels[node.adapter_type] }}</span>
      </div>
      <p v-if="node.status_reason" class="detail-reason">{{ node.status_reason }}</p>

      <section class="detail-section">
        <h2>资源</h2>
        <div class="detail-metrics">
          <metric-bar label="CPU" :value="node.cpu_percent" :display="formatPercent(node.cpu_percent)" />
          <metric-bar
            label="内存"
            :value="percent(node.memory_used_bytes, node.memory_total_bytes)"
            :display="`${formatBytes(node.memory_used_bytes)} / ${formatBytes(node.memory_total_bytes)}`"
          />
          <metric-bar
            label="磁盘"
            :value="percent(node.disk_used_bytes, node.disk_total_bytes)"
            :display="`${formatBytes(node.disk_used_bytes)} / ${formatBytes(node.disk_total_bytes)}`"
          />
        </div>
        <dl class="detail-list detail-list--two">
          <div><dt>下行速率</dt><dd>{{ formatRate(node.network_rx_bps) }}</dd></div>
          <div><dt>上行速率</dt><dd>{{ formatRate(node.network_tx_bps) }}</dd></div>
          <div><dt>系统负载</dt><dd>{{ node.load_1.toFixed(2) }} / {{ node.load_5.toFixed(2) }} / {{ node.load_15.toFixed(2) }}</dd></div>
          <div><dt>运行时间</dt><dd>{{ formatUptime(node.uptime_seconds) }}</dd></div>
        </dl>
      </section>

      <node-operations-panel
        v-if="node.agent_installation_id"
        :node="node"
        @changed="emit('changed')"
        @session-expired="emit('session-expired')"
      />

      <s-u-i-adapter-panel
        v-if="node.adapter_type === 's_ui'"
        :node="node"
        @changed="emit('changed')"
        @session-expired="emit('session-expired')"
      />

      <section class="detail-section">
        <div class="detail-section__heading">
          <h2>用户流量与在线</h2>
          <status-pill
            v-if="node.adapter_type === 'native_hysteria2' || node.adapter_type === 'sing_box_vless_reality'"
            :type="node.usage_available ? 'success' : (node.usage_enabled ? 'error' : 'warning')"
          >
            {{ node.usage_available ? "统计正常" : (node.usage_enabled ? "统计异常" : "统计未配置") }}
          </status-pill>
        </div>
        <dl class="detail-list detail-list--two">
          <div><dt>在线用户 / 活跃连接</dt><dd>{{ node.online_users }} / {{ node.online_connections }}</dd></div>
          <div><dt>未知在线用户</dt><dd>{{ node.online_unknown_users }}</dd></div>
          <div><dt>上传</dt><dd>{{ formatBytes(node.traffic_upload_bytes) }}</dd></div>
          <div><dt>下载</dt><dd>{{ formatBytes(node.traffic_download_bytes) }}</dd></div>
          <div><dt>未归属流量</dt><dd>{{ formatBytes(node.traffic_unattributed_bytes) }}</dd></div>
          <div><dt>当前周期有效用量</dt><dd>{{ formatBytes(node.traffic_used_bytes) }}</dd></div>
        </dl>
        <dl class="detail-list">
          <div><dt>统计采样</dt><dd>{{ relativeTime(node.usage_sampled_at) }}</dd></div>
          <div><dt>流量入账</dt><dd>{{ relativeTime(node.traffic_last_report_at) }}</dd></div>
          <div><dt>在线采样</dt><dd>{{ relativeTime(node.online_sampled_at) }}</dd></div>
          <div v-if="node.usage_error_code"><dt>统计错误</dt><dd>{{ node.usage_error_code }}</dd></div>
        </dl>
      </section>

      <section class="detail-section">
        <h2>运行环境</h2>
        <dl class="detail-list">
          <div><dt>服务商</dt><dd>{{ node.provider || "-" }}</dd></div>
          <div><dt>地区</dt><dd>{{ node.region || "-" }}</dd></div>
          <div><dt>系统</dt><dd>{{ [node.os_name, node.os_version, node.architecture].filter(Boolean).join(" ") || "尚未上报" }}</dd></div>
          <div><dt>代理核心</dt><dd>{{ [node.core_name, node.core_version].filter(Boolean).join(" ") || "尚未上报" }}</dd></div>
          <div><dt>核心服务</dt><dd>{{ node.agent_installation_id ? (node.core_running ? "运行中" : "未运行") : "尚未上报" }}</dd></div>
          <div><dt>Agent</dt><dd>{{ node.agent_version || "尚未注册" }}</dd></div>
          <div><dt>订阅端点</dt><dd>{{ endpointLabel(node) }}</dd></div>
          <template v-if="node.adapter_type === 'sing_box_vless_reality'">
            <div><dt>协议与传输</dt><dd>VLESS / TCP / Reality</dd></div>
            <div><dt>SNI / 伪装域名</dt><dd>{{ node.sni || "未配置" }}</dd></div>
            <div><dt>握手目标</dt><dd>{{ realityHandshakeLabel(node) }}</dd></div>
            <div><dt>Reality 身份</dt><dd>{{ node.reality?.public_key && node.reality?.short_id ? `已应用 · 第 ${node.reality.applied_key_generation} 代` : "等待节点生成" }}</dd></div>
            <div><dt>目标身份代际</dt><dd>{{ node.reality ? `第 ${node.reality.key_generation} 代${node.reality.key_generation !== node.reality.applied_key_generation ? " · 等待应用" : ""}` : "未配置" }}</dd></div>
            <div><dt>身份应用版本</dt><dd>{{ node.reality?.material_applied_version || "尚未应用" }}</dd></div>
          </template>
          <template v-else>
            <div><dt>TLS SNI</dt><dd>{{ node.sni || node.public_host || "-" }}</dd></div>
            <div><dt>证书验证</dt><dd>{{ node.tls_insecure ? "跳过" : "验证" }}</dd></div>
            <div><dt>证书固定</dt><dd>{{ node.tls_cert_fingerprint ? "已配置" : "未配置" }}</dd></div>
            <div><dt>公钥固定</dt><dd>{{ node.tls_public_key_sha256 ? "已配置" : "未配置" }}</dd></div>
          </template>
        </dl>
      </section>

      <section class="detail-section">
        <h2>同步</h2>
        <dl class="detail-list">
          <div><dt>最后上报</dt><dd>{{ relativeTime(node.last_seen_at) }}</dd></div>
          <div><dt>配置版本</dt><dd>{{ node.applied_version }} / {{ node.desired_version }}</dd></div>
          <div><dt>最后应用</dt><dd>{{ relativeTime(node.last_applied_at) }}</dd></div>
        </dl>
      </section>

      <template #footer>
        <div class="drawer-actions">
          <n-button @click="emit('edit', node)">
            <template #icon><n-icon><pencil /></n-icon></template>
            编辑
          </n-button>
          <n-button type="primary" @click="emit('enroll', node)">
            <template #icon><n-icon><key-round /></n-icon></template>
            注册 Agent
          </n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>
