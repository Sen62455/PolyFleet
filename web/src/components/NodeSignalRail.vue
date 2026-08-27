<script setup lang="ts">
import { computed } from "vue";
import { CloudCog, Cpu, Globe2, RadioTower } from "@lucide/vue";
import { statusLabels } from "../lib/format";
import type { NodeRecord } from "../types";

const props = withDefaults(defineProps<{ node: NodeRecord; compact?: boolean }>(), { compact: false });

type SignalState = "ok" | "warn" | "down" | "idle";

const stages = computed(() => {
  const agentState: SignalState = props.node.status === "disabled"
    ? "idle"
    : props.node.status === "online"
      ? "ok"
      : props.node.status === "pending" || props.node.status === "stale"
        ? "warn"
        : "down";
  const configurationApplied = props.node.desired_version > 0
    && props.node.applied_version >= props.node.desired_version;
	const endpointReady = props.node.enabled && props.node.public_host !== "" && props.node.public_port > 0;

  return [
		{
			label: "Control",
			value: props.node.desired_version <= 0
				? "尚未下发"
				: configurationApplied
					? `配置 v${props.node.applied_version}`
					: `等待 v${props.node.desired_version}`,
			state: (props.node.desired_version <= 0 ? "idle" : configurationApplied ? "ok" : "warn") as SignalState,
			icon: CloudCog,
		},
    {
      label: "Agent",
      value: props.node.agent_version || statusLabels[props.node.status] || "未注册",
      state: agentState,
			icon: RadioTower,
    },
    {
			label: "Core",
      value: props.node.core_running ? (props.node.core_name || "运行中") : "未运行",
      state: (props.node.core_running ? "ok" : "down") as SignalState,
			icon: Cpu,
    },
    {
			label: "Endpoint",
			value: endpointReady ? `${props.node.public_host}:${props.node.public_port}` : "端点未设置",
			state: (endpointReady && props.node.core_running ? "ok" : endpointReady ? "warn" : "idle") as SignalState,
			icon: Globe2,
    },
  ];
});
</script>

<template>
  <div
    class="node-signal-rail"
    :class="{ 'node-signal-rail--compact': compact }"
    role="list"
    aria-label="节点运行链路"
  >
    <div
      v-for="stage in stages"
      :key="stage.label"
      class="node-signal-rail__stage"
      :class="`node-signal-rail__stage--${stage.state}`"
      role="listitem"
    >
			<i aria-hidden="true"><component :is="stage.icon" :size="15" :stroke-width="1.8" /></i>
      <span>
        <strong>{{ stage.label }}</strong>
        <small :title="stage.value">{{ stage.value }}</small>
      </span>
    </div>
  </div>
</template>
