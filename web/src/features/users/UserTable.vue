<script setup lang="ts">
import { h } from "vue";
import { Archive, MoreHorizontal, Pencil, Settings2, Wifi } from "@lucide/vue";
import { NButton, NDropdown, NIcon, NTooltip, type DropdownOption } from "naive-ui";
import { formatBytes, formatDateTime, percent, severity } from "../../lib/format";
import type { UserRecord } from "../../types";

defineProps<{
  users: UserRecord[];
  selectedUserId?: string | null;
}>();
const emit = defineEmits<{
  select: [user: UserRecord, trigger: HTMLElement];
  action: [action: "edit" | "manage" | "archive", user: UserRecord];
}>();

function icon(component: typeof Pencil) {
  return () => h(NIcon, null, { default: () => h(component, { size: 16 }) });
}

const options: DropdownOption[] = [
  { label: "编辑", key: "edit", icon: icon(Pencil) },
  { label: "管理节点", key: "manage", icon: icon(Settings2) },
  { type: "divider", key: "divider" },
  { label: "归档", key: "archive", icon: icon(Archive) },
];

const statusLabels = { active: "启用", disabled: "已停用", expired: "已到期" } as const;
function choose(key: string | number, user: UserRecord) {
  emit("action", key as "edit" | "manage" | "archive", user);
}

function selectUser(user: UserRecord, event: MouseEvent | KeyboardEvent) {
  const trigger = event.currentTarget as HTMLElement;
  trigger.focus();
  emit("select", user, trigger);
}

function syncState(user: UserRecord): { label: string; state: string } {
  if (user.assignments.length === 0) return { label: "未分配", state: "idle" };
  if (user.assignments.some((assignment) => assignment.state === "failed")) {
    return { label: "同步失败", state: "error" };
  }
  if (user.assignments.some((assignment) => assignment.state !== "applied" || assignment.applied_version < assignment.desired_version)) {
    return { label: "等待同步", state: "pending" };
  }
  return { label: "已同步", state: "applied" };
}

</script>

<template>
  <div class="user-table-wrap user-table-wrap--desktop">
    <table class="user-table">
      <thead>
        <tr>
          <th>用户</th>
          <th>状态</th>
          <th>节点</th>
          <th>流量</th>
          <th>到期时间</th>
          <th>配置同步</th>
          <th><span class="sr-only">操作</span></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="user in users"
          :key="user.id"
          class="user-table__row"
          :class="{ 'user-table__row--selected': selectedUserId === user.id }"
          tabindex="0"
          :aria-selected="selectedUserId === user.id"
          :aria-controls="selectedUserId === user.id ? 'user-detail-panel' : undefined"
          @click="selectUser(user, $event)"
          @keydown.enter="selectUser(user, $event)"
          @keydown.space.prevent="selectUser(user, $event)"
        >
          <td>
            <div class="user-identity">
              <span class="user-avatar" aria-hidden="true">{{ (user.display_name || user.username).slice(0, 1).toUpperCase() }}</span>
              <span>
                <strong>{{ user.display_name || user.username }}</strong>
                <small>@{{ user.username }}</small>
              </span>
            </div>
          </td>
          <td>
            <div class="user-status-stack">
              <span class="status-marker" :class="`status-marker--${user.status}`">
                <i aria-hidden="true" />{{ statusLabels[user.status] }}
              </span>
              <span class="table-secondary online-inline" :class="{ 'online-inline--active': user.online_connections > 0 }">
                <wifi :size="12" aria-hidden="true" /> {{ user.online_connections }} 个连接
              </span>
            </div>
          </td>
          <td>
            <div v-if="user.assignments.length" class="assignment-inline">
              <span v-for="assignment in user.assignments.slice(0, 2)" :key="assignment.id">
                {{ assignment.node_name }}
              </span>
              <span v-if="user.assignments.length > 2">+{{ user.assignments.length - 2 }}</span>
            </div>
            <span v-else class="table-muted">未分配</span>
          </td>
          <td>
            <span class="traffic-value" :class="{ 'traffic-value--limited': user.quota_state === 'limited' }">
              {{ formatBytes(user.traffic_used_bytes) }}
            </span>
            <span class="table-secondary">{{ user.traffic_limit_bytes ? `共 ${formatBytes(user.traffic_limit_bytes)}` : "不限额" }}</span>
<i v-if="user.traffic_limit_bytes" class="traffic-bar" :class="`is-${severity(percent(user.traffic_used_bytes, user.traffic_limit_bytes))}`">
  <b :style="{ width: `${percent(user.traffic_used_bytes, user.traffic_limit_bytes)}%` }" />
</i>
          </td>
          <td><span class="user-expiry">{{ formatDateTime(user.expires_at) }}</span></td>
          <td>
            <span class="sync-state" :class="`sync-state--${syncState(user).state}`">
              <i aria-hidden="true" />{{ syncState(user).label }}
            </span>
          </td>
          <td class="action-cell" @click.stop @keydown.stop>
            <n-dropdown trigger="click" :options="options" @select="choose($event, user)">
              <n-tooltip trigger="hover">
                <template #trigger>
                  <n-button quaternary circle :aria-label="`${user.username} 操作`">
                    <template #icon><n-icon><more-horizontal /></n-icon></template>
                  </n-button>
                </template>
                操作
              </n-tooltip>
            </n-dropdown>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="user-mobile-list">
    <article
      v-for="user in users"
      :key="user.id"
      class="user-mobile-item"
      :class="{ 'user-mobile-item--selected': selectedUserId === user.id }"
      @click="selectUser(user, $event)"
    >
      <button
        type="button"
        class="user-mobile-item__select"
        :aria-label="`管理 ${user.username}`"
        :aria-current="selectedUserId === user.id ? 'true' : undefined"
        :aria-controls="selectedUserId === user.id ? 'user-detail-panel' : undefined"
        @click.stop="selectUser(user, $event)"
      >
        <header>
          <div class="user-identity">
              <span class="user-avatar" aria-hidden="true">{{ (user.display_name || user.username).slice(0, 1).toUpperCase() }}</span>
            <span>
              <strong>{{ user.display_name || user.username }}</strong>
              <small>@{{ user.username }}</small>
            </span>
          </div>
        </header>
        <div class="user-mobile-item__status">
          <span class="status-marker" :class="`status-marker--${user.status}`">
            <i aria-hidden="true" />{{ statusLabels[user.status] }}
          </span>
          <span>{{ user.assignments.length }} 个节点</span>
          <span class="online-inline" :class="{ 'online-inline--active': user.online_connections > 0 }">
            <wifi :size="12" aria-hidden="true" /> {{ user.online_connections }} 个连接
          </span>
        </div>
        <footer>
          <span>{{ formatBytes(user.traffic_used_bytes) }} / {{ user.traffic_limit_bytes ? formatBytes(user.traffic_limit_bytes) : "不限额" }}</span>
          <span>{{ formatDateTime(user.expires_at) }}</span>
          <span class="sync-state" :class="`sync-state--${syncState(user).state}`">
            <i aria-hidden="true" />{{ syncState(user).label }}
          </span>
        </footer>
<i v-if="user.traffic_limit_bytes" class="traffic-bar" :class="`is-${severity(percent(user.traffic_used_bytes, user.traffic_limit_bytes))}`">
  <b :style="{ width: `${percent(user.traffic_used_bytes, user.traffic_limit_bytes)}%` }" />
</i>
      </button>
      <div class="user-mobile-item__menu" @click.stop @keydown.stop>
        <n-dropdown trigger="click" :options="options" @select="choose($event, user)">
          <n-button quaternary circle :aria-label="`${user.username} 操作`">
            <template #icon><n-icon><more-horizontal /></n-icon></template>
          </n-button>
        </n-dropdown>
      </div>
    </article>
  </div>
</template>
