<script setup lang="ts">
import HintTooltip from "./HintTooltip.vue";
import {
  BarChart3,
  BellRing,
  Cable,
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  ClipboardList,
  LayoutDashboard,
  KeyRound,
  Server,
  UsersRound,
} from "@lucide/vue";
import BrandMark from "./BrandMark.vue";
import { consolePages, type DashboardView } from "../lib/navigation";

type NavigationView = DashboardView;

const props = withDefaults(defineProps<{
  activeView: NavigationView;
  collapsed?: boolean;
  mobile?: boolean;
}>(), { collapsed: false, mobile: false });

const emit = defineEmits<{
  navigate: [view: Exclude<NavigationView, "node-detail">];
  collapse: [];
}>();

const icons = { overview: LayoutDashboard, nodes: Server, users: UsersRound, assets: CalendarClock, exits: Cable, subscriptions: KeyRound, reports: BarChart3, notifications: BellRing, operations: ClipboardList };
const navigation = consolePages.map((page) => ({ ...page, icon: icons[page.key] }));

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
        <hint-tooltip
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
            </button>
          </template>
          {{ item.label }}
        </hint-tooltip>
      </template>
    </nav>

    <div class="console-nav__spacer" />

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
