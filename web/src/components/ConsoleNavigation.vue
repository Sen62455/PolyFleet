<script setup lang="ts">
import {
  Activity,
  BarChart3,
  BellRing,
  ChevronLeft,
  ChevronRight,
  ClipboardList,
  LayoutDashboard,
  KeyRound,
  Server,
  UsersRound,
} from "@lucide/vue";
import { NTooltip } from "naive-ui";
import BrandMark from "./BrandMark.vue";

type NavigationView = "overview" | "nodes" | "users" | "subscriptions" | "reports" | "notifications" | "operations" | "node-detail";

const props = withDefaults(defineProps<{
  activeView: NavigationView;
  collapsed?: boolean;
  mobile?: boolean;
  online: number;
  total: number;
  issues: number;
}>(), { collapsed: false, mobile: false });

const emit = defineEmits<{
  navigate: [view: Exclude<NavigationView, "node-detail">];
  collapse: [];
}>();

const navigation = [
  { key: "overview" as const, label: "总览", icon: LayoutDashboard, group: "监控" },
  { key: "nodes" as const, label: "节点", icon: Server, group: "监控" },
  { key: "users" as const, label: "用户", icon: UsersRound, group: "管理" },
  { key: "subscriptions" as const, label: "订阅运营", icon: KeyRound, group: "运营" },
  { key: "reports" as const, label: "流量报表", icon: BarChart3, group: "运营" },
  { key: "notifications" as const, label: "告警通知", icon: BellRing, group: "运营" },
  { key: "operations" as const, label: "操作记录", icon: ClipboardList, group: "审计" },
];

function active(key: string) {
  return key === "nodes"
    ? props.activeView === "nodes" || props.activeView === "node-detail"
    : props.activeView === key;
}
</script>

<template>
  <div class="console-nav" :class="{ 'console-nav--collapsed': collapsed, 'console-nav--mobile': mobile }">
    <div class="console-nav__brand">
      <brand-mark :compact="collapsed && !mobile" />
      <span v-if="!collapsed || mobile" class="console-nav__edition">CONTROL</span>
    </div>

    <nav class="console-nav__menu" aria-label="主导航">
      <template v-for="group in ['监控', '管理', '运营', '审计']" :key="group">
        <span v-if="!collapsed || mobile" class="console-nav__group-label">{{ group }}</span>
        <n-tooltip
          v-for="item in navigation.filter((entry) => entry.group === group)"
          :key="item.key"
          trigger="hover"
          placement="right"
          :disabled="!collapsed || mobile"
        >
          <template #trigger>
            <button
              type="button"
              class="console-nav__item"
              :class="{ 'is-active': active(item.key) }"
              :aria-current="active(item.key) ? 'page' : undefined"
              :aria-label="item.label"
              @click="emit('navigate', item.key)"
            >
              <component :is="item.icon" :size="18" :stroke-width="1.8" aria-hidden="true" />
              <span v-if="!collapsed || mobile">{{ item.label }}</span>
              <chevron-right v-if="(!collapsed || mobile) && active(item.key)" :size="14" aria-hidden="true" />
            </button>
          </template>
          {{ item.label }}
        </n-tooltip>
      </template>
    </nav>

    <div class="console-nav__spacer" />

    <section v-if="!collapsed || mobile" class="console-nav__health" aria-label="系统健康">
      <header><activity :size="15" aria-hidden="true" /><span>系统健康</span></header>
      <div>
        <strong>{{ online }} / {{ total }}</strong>
        <span :class="{ 'is-warning': issues > 0 }">{{ issues ? `${issues} 项需关注` : total ? "全部在线" : "等待节点" }}</span>
      </div>
      <small class="console-nav__health-state" :class="{ 'is-warning': issues > 0 }"><i />{{ issues ? "需要检查" : total ? "运行正常" : "尚未接入" }}</small>
      <i><span :style="{ width: `${total ? Math.round((online / total) * 100) : 0}%` }" /></i>
    </section>

    <n-tooltip v-else trigger="hover" placement="right">
      <template #trigger>
        <div class="console-nav__health-dot" :class="{ 'is-warning': issues > 0 }" :aria-label="`${online} / ${total} 台在线`">
          <activity :size="18" aria-hidden="true" />
        </div>
      </template>
      {{ online }} / {{ total }} 台在线
    </n-tooltip>

    <button
      v-if="!mobile"
      type="button"
      class="console-nav__collapse"
      :aria-label="collapsed ? '展开侧边栏' : '收起侧边栏'"
      @click="emit('collapse')"
    >
      <chevron-right v-if="collapsed" :size="16" aria-hidden="true" />
      <chevron-left v-else :size="16" aria-hidden="true" />
      <span v-if="!collapsed">收起侧边栏</span>
    </button>
  </div>
</template>
