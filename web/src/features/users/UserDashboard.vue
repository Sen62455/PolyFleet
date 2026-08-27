<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Plus, RefreshCw, Search, UsersRound } from "@lucide/vue";
import { NAlert, NButton, NIcon, NInput, NSpin, NTooltip, useDialog, useMessage } from "naive-ui";
import { api, APIError } from "../../api";
import type {
  NodeRecord,
  IssuedSubscriptionToken,
  SubscriptionTokenInput,
  SubscriptionTokenRecord,
  UserAssignment,
  UserCredential,
  UserInput,
  UserRecord,
} from "../../types";
import CredentialDialog from "./CredentialDialog.vue";
import SubscriptionTokenDialog from "./SubscriptionTokenDialog.vue";
import SubscriptionTokenFormModal from "./SubscriptionTokenFormModal.vue";
import UserDetailDrawer from "./UserDetailDrawer.vue";
import UserFormModal from "./UserFormModal.vue";
import UserTable from "./UserTable.vue";

const props = defineProps<{ nodes: NodeRecord[] }>();
const emit = defineEmits<{
  "session-expired": [];
  "nodes-changed": [];
  "open-node": [nodeId: string];
}>();
const message = useMessage();
const dialog = useDialog();

const users = ref<UserRecord[]>([]);
const totalUsers = ref(0);
const userOffset = ref(0);
const userPageSize = 50;
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
const formOpen = ref(false);
const saving = ref(false);
const editingUser = ref<UserRecord | null>(null);
const detailUserID = ref<string | null>(null);
const working = ref("");
const credentialOpen = ref(false);
const credentialTitle = ref("用户凭据");
const credentials = ref<UserCredential[]>([]);
const subscriptionTokens = ref<SubscriptionTokenRecord[]>([]);
const subscriptionLoading = ref(false);
const subscriptionFormOpen = ref(false);
const subscriptionSaving = ref(false);
const subscriptionUserID = ref<string | null>(null);
const issuedSubscription = ref<IssuedSubscriptionToken | null>(null);
const issuedSubscriptionOpen = ref(false);
type UserFilter = "all" | "active" | "online" | "attention";
const searchQuery = ref("");
const userFilter = ref<UserFilter>("all");
const compactDetail = ref(false);
const detailReturnTarget = ref<HTMLElement | null>(null);

const assignableNodes = computed(() =>
  props.nodes.filter(
    (node) =>
      node.adapter_type === "native_hysteria2" ||
      node.adapter_type === "sing_box_vless_reality" ||
      (node.adapter_type === "s_ui" && node.s_ui_target_inbound_ids.length > 0),
  ),
);
const onlineConnections = computed(() => users.value.reduce((total, user) => total + user.online_connections, 0));
const limitedCount = computed(() => users.value.filter((user) => user.quota_state === "limited").length);
const unavailableCount = computed(() => users.value.filter((user) => user.status !== "active").length);
const detailUser = computed(() => users.value.find((user) => user.id === detailUserID.value) ?? null);
const userFilters: Array<{ value: UserFilter; label: string }> = [
  { value: "all", label: "全部" },
  { value: "active", label: "可用" },
  { value: "online", label: "在线" },
  { value: "attention", label: "需关注" },
];
const filteredUsers = computed(() => {
  return users.value.filter((user) => {
    if (userFilter.value === "active") return user.status === "active" && user.quota_state !== "limited";
    if (userFilter.value === "online") return user.online_connections > 0;
    if (userFilter.value === "attention") return user.status !== "active" || user.quota_state === "limited";
    return true;
  });
});

function resetUserFilters() {
  searchQuery.value = "";
  userFilter.value = "all";
}

function openDetail(user: UserRecord, trigger: HTMLElement) {
  detailReturnTarget.value = trigger;
  detailUserID.value = user.id;
}

function handleAPIError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return;
  }
  const messages: Record<string, string> = {
    user_conflict: "用户名或节点分配已经存在。",
    adapter_users_unsupported: "该节点尚不支持受管用户，或 S-UI 目标入站尚未配置。",
    assignment_read_only: "只读导入不允许修改状态、额度或凭据。",
    user_resource_not_found: "用户或节点分配已不存在。",
    credential_rotation_pending: "节点仍有待同步配置，请等待同步完成后再轮换凭据。",
    subscription_token_expired: "该 Token 已到期，请创建新的 Token。",
    subscription_token_revoked: "该 Token 已撤销，请创建新的 Token。",
  };
  message.error(error instanceof APIError ? (messages[error.code] ?? error.message) : fallback);
}

async function loadSubscriptionTokens(userId: string) {
  subscriptionLoading.value = true;
  try {
    subscriptionTokens.value = await api.listSubscriptionTokens(userId);
  } catch (error) {
    handleAPIError(error, "订阅 Token 加载失败。");
    subscriptionTokens.value = [];
  } finally {
    subscriptionLoading.value = false;
  }
}

async function loadUsers(silent = false) {
  if (!silent) loading.value = users.value.length === 0;
  refreshing.value = true;
  loadError.value = "";
  const requestID = ++usersRequestID;
  try {
    const page = await api.listUsersPage({
      search: searchQuery.value.trim(),
      limit: userPageSize,
      offset: userOffset.value,
    });
    if (requestID !== usersRequestID) return;
    users.value = page.users;
    totalUsers.value = page.total;
    if (detailUserID.value && !page.users.some((user) => user.id === detailUserID.value)) {
      detailUserID.value = null;
    }
  } catch (error) {
    if (requestID !== usersRequestID) return;
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    loadError.value = error instanceof APIError ? error.message : "用户列表加载失败。";
  } finally {
    if (requestID === usersRequestID) {
      loading.value = false;
      refreshing.value = false;
    }
  }
}

function changeUserPage(offset: number) {
  userOffset.value = Math.max(0, offset);
  detailUserID.value = null;
  void loadUsers();
}

function openCreate() {
  editingUser.value = null;
  formOpen.value = true;
}

function openEdit(user: UserRecord) {
  editingUser.value = user;
  formOpen.value = true;
}

function showCredentials(title: string, items: UserCredential[], returnUserID: string | null) {
  if (items.length === 0) return false;
  credentialTitle.value = title;
  credentials.value = items;
  if (returnUserID) detailUserID.value = returnUserID;
  credentialOpen.value = true;
  return true;
}

function setCredentialOpen(show: boolean) {
  credentialOpen.value = show;
}

function openSubscriptionForm(user: UserRecord) {
  subscriptionUserID.value = user.id;
  subscriptionFormOpen.value = true;
}

async function createSubscriptionToken(input: SubscriptionTokenInput) {
  if (!subscriptionUserID.value) return;
  subscriptionSaving.value = true;
  const userID = subscriptionUserID.value;
  try {
    const issued = await api.createSubscriptionToken(userID, input);
    subscriptionFormOpen.value = false;
    await loadSubscriptionTokens(userID);
    showIssuedSubscription(issued, userID);
    message.success("订阅 Token 已创建");
  } catch (error) {
    handleAPIError(error, "订阅 Token 创建失败。");
  } finally {
    subscriptionSaving.value = false;
  }
}

function showIssuedSubscription(issued: IssuedSubscriptionToken, returnUserID: string) {
  issuedSubscription.value = issued;
  detailUserID.value = returnUserID;
  issuedSubscriptionOpen.value = true;
}

function setIssuedSubscriptionOpen(show: boolean) {
  issuedSubscriptionOpen.value = show;
  if (!show) {
    issuedSubscription.value = null;
  }
}

function rotateSubscriptionToken(user: UserRecord, token: SubscriptionTokenRecord) {
  dialog.warning({
    title: "轮换订阅 Token",
    content: `确认轮换“${token.name}”？当前订阅地址会立即失效。`,
    positiveText: "轮换",
    negativeText: "取消",
    async onPositiveClick() {
      working.value = `subscription-rotate:${token.id}`;
      try {
        const issued = await api.rotateSubscriptionToken(user.id, token.id);
        await loadSubscriptionTokens(user.id);
        showIssuedSubscription(issued, user.id);
        message.success("订阅 Token 已轮换");
      } catch (error) {
        handleAPIError(error, "订阅 Token 轮换失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

function revokeSubscriptionToken(user: UserRecord, token: SubscriptionTokenRecord) {
  dialog.warning({
    title: "撤销订阅 Token",
    content: `确认撤销“${token.name}”？对应订阅地址会立即失效。`,
    positiveText: "撤销",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      working.value = `subscription-revoke:${token.id}`;
      try {
        await api.revokeSubscriptionToken(user.id, token.id);
        await loadSubscriptionTokens(user.id);
        message.success("订阅 Token 已撤销");
      } catch (error) {
        handleAPIError(error, "订阅 Token 撤销失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

async function saveUser(input: UserInput) {
  saving.value = true;
  try {
    if (editingUser.value) {
      const saved = await api.updateUser(editingUser.value.id, input);
      detailUserID.value = saved.id;
      message.success("用户已更新");
    } else {
      const result = await api.createUser(input);
      if (!showCredentials("新用户凭据", result.credentials, result.user.id)) {
        detailUserID.value = result.user.id;
      }
      message.success("用户已添加");
    }
    formOpen.value = false;
    editingUser.value = null;
    await loadUsers(true);
    emit("nodes-changed");
  } catch (error) {
    handleAPIError(error, "用户保存失败。");
  } finally {
    saving.value = false;
  }
}

function archiveUser(user: UserRecord) {
  dialog.warning({
    title: "归档用户",
    content: `确认归档“${user.display_name || user.username}”？所有节点上的凭据都将被撤销。`,
    positiveText: "归档",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.archiveUser(user.id);
        if (detailUserID.value === user.id) detailUserID.value = null;
        await loadUsers(true);
        emit("nodes-changed");
        message.success("用户已归档");
      } catch (error) {
        handleAPIError(error, "用户归档失败。");
        return false;
      }
      return true;
    },
  });
}

function handleAction(action: "edit" | "manage" | "archive", user: UserRecord) {
  if (action === "edit") openEdit(user);
  if (action === "manage") detailUserID.value = user.id;
  if (action === "archive") archiveUser(user);
}

function userInput(user: UserRecord, enabled = user.enabled): UserInput {
  return {
    username: user.username,
    display_name: user.display_name,
    notes: user.notes,
    enabled,
    expires_at: user.expires_at,
    traffic_limit_bytes: user.traffic_limit_bytes,
    node_ids: [],
  };
}

async function toggleUser(user: UserRecord, enabled: boolean) {
  working.value = `user:${user.id}`;
  try {
    await api.updateUser(user.id, userInput(user, enabled));
    await loadUsers(true);
    emit("nodes-changed");
    message.success(enabled ? "用户已启用" : "用户已停用");
  } catch (error) {
    handleAPIError(error, "用户状态更新失败。");
  } finally {
    working.value = "";
  }
}

async function assignUser(user: UserRecord, nodeId: string, trafficLimitBytes: number) {
  working.value = `assign:${user.id}`;
  try {
    const result = await api.assignUser(user.id, nodeId, trafficLimitBytes);
    showCredentials("新节点凭据", [result.credential], user.id);
    await loadUsers(true);
    emit("nodes-changed");
    message.success("节点已分配");
  } catch (error) {
    handleAPIError(error, "节点分配失败。");
  } finally {
    working.value = "";
  }
}

async function toggleAssignment(user: UserRecord, assignment: UserAssignment, enabled: boolean) {
  working.value = `toggle:${assignment.id}`;
  try {
    await api.updateAssignment(user.id, assignment.node_id, { enabled });
    await loadUsers(true);
    emit("nodes-changed");
    message.success(enabled ? "节点分配已启用" : "节点分配已停用");
  } catch (error) {
    handleAPIError(error, "节点分配状态更新失败。");
  } finally {
    working.value = "";
  }
}

async function updateAssignmentLimit(user: UserRecord, assignment: UserAssignment, trafficLimitBytes: number) {
  working.value = `limit:${assignment.id}`;
  try {
    await api.updateAssignment(user.id, assignment.node_id, { traffic_limit_bytes: trafficLimitBytes });
    await loadUsers(true);
    emit("nodes-changed");
    message.success("节点额度已更新");
  } catch (error) {
    handleAPIError(error, "节点额度更新失败。");
  } finally {
    working.value = "";
  }
}

function kickUser(user: UserRecord, assignment?: UserAssignment) {
  const target = assignment ? `“${assignment.node_name}”` : "所有已分配节点";
  dialog.warning({
    title: "踢下线",
    content: `确认将“${user.display_name || user.username}”从${target}断开？用户在账户和额度允许时仍可重新连接。`,
    positiveText: "踢下线",
    negativeText: "取消",
    async onPositiveClick() {
      working.value = assignment ? `kick:${assignment.id}` : `kick:${user.id}`;
      try {
        const result = await api.kickUser(user.id, assignment?.node_id ?? "");
        await loadUsers(true);
        emit("nodes-changed");
        message.success(`已向 ${result.requested_nodes} 个节点排队踢线指令`);
      } catch (error) {
        handleAPIError(error, "踢线指令提交失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

function unassignUser(user: UserRecord, assignment: UserAssignment) {
  dialog.warning({
    title: "取消节点分配",
    content: `确认从“${assignment.node_name}”移除该用户？对应凭据会立即撤销。`,
    positiveText: "取消分配",
    negativeText: "返回",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      working.value = `unassign:${assignment.id}`;
      try {
        await api.unassignUser(user.id, assignment.node_id);
        await loadUsers(true);
        emit("nodes-changed");
        message.success("节点分配已取消");
      } catch (error) {
        handleAPIError(error, "取消节点分配失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

async function revealCredential(user: UserRecord, assignment: UserAssignment) {
  working.value = `reveal:${assignment.id}`;
  try {
    const credential = await api.revealCredential(user.id, assignment.node_id);
    showCredentials(`${assignment.node_name} 凭据`, [credential], user.id);
  } catch (error) {
    handleAPIError(error, "凭据读取失败。");
  } finally {
    working.value = "";
  }
}

function rotateAssignmentCredential(user: UserRecord, assignment: UserAssignment) {
  dialog.warning({
    title: "轮换节点凭据",
    content: `确认轮换“${assignment.node_name}”的节点凭据？节点同步完成前不会出现在新拉取的订阅中。`,
    positiveText: "轮换",
    negativeText: "取消",
    async onPositiveClick() {
      working.value = `credential-rotate:${assignment.id}`;
      try {
        const result = await api.rotateAssignmentCredential(user.id, assignment.node_id);
        showCredentials(`${assignment.node_name} 新凭据`, [result.credential], user.id);
        await loadUsers(true);
        emit("nodes-changed");
        message.success("新凭据已等待节点应用");
      } catch (error) {
        handleAPIError(error, "凭据轮换失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

function rotateUserCredentials(user: UserRecord) {
  dialog.warning({
    title: "轮换全部凭据",
    content: `确认轮换“${user.display_name || user.username}”在全部节点上的受管凭据？`,
    positiveText: "全部轮换",
    negativeText: "取消",
    async onPositiveClick() {
      working.value = `credential-rotate:${user.id}`;
      try {
        const result = await api.rotateUserCredentials(user.id);
        if (!showCredentials("新节点凭据", result.credentials, user.id)) {
          detailUserID.value = user.id;
        }
        await loadUsers(true);
        emit("nodes-changed");
        message.success("全部新凭据已等待节点应用");
      } catch (error) {
        handleAPIError(error, "全部凭据轮换失败。");
        return false;
      } finally {
        working.value = "";
      }
      return true;
    },
  });
}

let usersRequestID = 0;
let searchTimer: number | undefined;
watch(searchQuery, () => {
  window.clearTimeout(searchTimer);
  searchTimer = window.setTimeout(() => {
    userOffset.value = 0;
    void loadUsers();
  }, 300);
});

watch(detailUserID, (userID) => {
  if (userID) {
    loadSubscriptionTokens(userID);
  } else {
    subscriptionTokens.value = [];
    const target = detailReturnTarget.value;
    detailReturnTarget.value = null;
    if (target?.isConnected) nextTick(() => target.focus());
  }
});

let refreshTimer: number | undefined;
let compactDetailQuery: MediaQueryList | undefined;
function updateCompactDetail(event?: MediaQueryListEvent) {
  compactDetail.value = event?.matches ?? compactDetailQuery?.matches ?? false;
}
onMounted(() => {
  compactDetailQuery = window.matchMedia("(max-width: 1180px)");
  updateCompactDetail();
  compactDetailQuery.addEventListener("change", updateCompactDetail);
  loadUsers();
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") loadUsers(true);
  }, 15_000);
});
onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
  window.clearTimeout(searchTimer);
  compactDetailQuery?.removeEventListener("change", updateCompactDetail);
});
</script>

<template>
  <main
    id="users"
    class="workspace users-workspace"
    :class="{ 'users-workspace--detail': detailUser !== null }"
  >
    <section
      class="users-master-pane user-master-detail__master"
      aria-label="用户列表工作区"
      :aria-hidden="compactDetail && detailUser ? 'true' : undefined"
      :inert="compactDetail && detailUser ? true : undefined"
    >
      <div class="page-heading">
      <div>
        <h1>用户</h1>
        <p>账户状态、到期时间与节点授权</p>
      </div>
      <div class="page-heading__actions">
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button circle secondary aria-label="刷新用户" :loading="refreshing" @click="loadUsers()">
              <template #icon><n-icon><refresh-cw /></n-icon></template>
            </n-button>
          </template>
          刷新
        </n-tooltip>
        <n-button type="primary" @click="openCreate">
          <template #icon><n-icon><plus /></n-icon></template>
          添加用户
        </n-button>
      </div>
    </div>

    <section class="fleet-summary users-summary" aria-label="用户摘要">
      <div class="fleet-summary__item">
        <span>全部用户</span>
        <strong>{{ totalUsers }}</strong>
      </div>
      <div class="fleet-summary__item fleet-summary__item--healthy">
        <span>活跃连接</span>
        <strong>{{ onlineConnections }}</strong>
      </div>
      <div class="fleet-summary__item fleet-summary__item--warning">
        <span>额度用尽</span>
        <strong>{{ limitedCount }}</strong>
      </div>
      <div class="fleet-summary__item fleet-summary__item--danger">
        <span>不可用账户</span>
        <strong>{{ unavailableCount }}</strong>
      </div>
    </section>

    <n-alert v-if="loadError" type="error" :show-icon="false" class="workspace-alert">
      <div class="alert-row">
        <span>{{ loadError }}</span>
        <n-button text type="error" @click="loadUsers()">重新加载</n-button>
      </div>
    </n-alert>

    <section v-if="!loading && users.length" class="user-ledger-toolbar" aria-label="筛选用户">
      <n-input
        v-model:value="searchQuery"
        clearable
        class="user-ledger-search"
        aria-label="搜索用户"
        placeholder="搜索姓名、用户名或备注"
      >
        <template #prefix><n-icon><search /></n-icon></template>
      </n-input>
      <div class="user-filter-segment" role="group" aria-label="用户状态">
        <button
          v-for="filter in userFilters"
          :key="filter.value"
          type="button"
          :class="{ active: userFilter === filter.value }"
          :aria-pressed="userFilter === filter.value"
          @click="userFilter = filter.value"
        >
          {{ filter.label }}
        </button>
      </div>
      <span class="user-filter-result">本页 {{ filteredUsers.length }} / {{ users.length }} · 共 {{ totalUsers }}</span>
    </section>

      <section class="node-surface users-surface" aria-label="用户列表">
        <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
        <div v-else-if="users.length === 0" class="surface-state surface-state--empty">
          <users-round :size="28" :stroke-width="1.7" aria-hidden="true" />
          <strong>尚未添加用户</strong>
          <n-button type="primary" size="small" @click="openCreate">
            <template #icon><n-icon><plus /></n-icon></template>
            添加用户
          </n-button>
        </div>
        <div v-else-if="filteredUsers.length === 0" class="surface-state surface-state--empty surface-state--filtered">
          <strong>没有符合条件的用户</strong>
          <n-button secondary size="small" @click="resetUserFilters">清除筛选</n-button>
        </div>
        <user-table
          v-else
          :users="filteredUsers"
          :selected-user-id="detailUserID"
          @select="openDetail"
          @action="handleAction"
        />
        <footer v-if="totalUsers > userPageSize" class="ops-pagination">
          <span>{{ userOffset + 1 }} - {{ Math.min(userOffset + users.length, totalUsers) }} / {{ totalUsers }}</span>
          <n-button size="small" :disabled="userOffset === 0" @click="changeUserPage(userOffset - userPageSize)">上一页</n-button>
          <n-button size="small" :disabled="userOffset + userPageSize >= totalUsers" @click="changeUserPage(userOffset + userPageSize)">下一页</n-button>
        </footer>
      </section>
    </section>

    <button
      v-if="detailUser"
      type="button"
      class="user-detail-backdrop"
      aria-label="关闭用户详情"
      tabindex="-1"
      @click="detailUserID = null"
    />
    <div v-if="detailUser" class="user-master-detail__divider" aria-hidden="true" />
    <transition name="user-detail-panel">
      <user-detail-drawer
        v-if="detailUser"
        :show="true"
        :modal="compactDetail"
        :user="detailUser"
        :assignable-nodes="assignableNodes"
        :subscription-tokens="subscriptionTokens"
        :subscription-loading="subscriptionLoading"
        :working="working"
        @update:show="!$event && (detailUserID = null)"
        @edit="openEdit"
        @toggle-user="toggleUser"
        @assign="assignUser"
        @toggle-assignment="toggleAssignment"
        @update-assignment-limit="updateAssignmentLimit"
        @kick-user="kickUser"
        @kick-assignment="kickUser"
        @unassign="unassignUser"
        @reveal="revealCredential"
        @create-subscription="openSubscriptionForm"
        @rotate-subscription="rotateSubscriptionToken"
        @revoke-subscription="revokeSubscriptionToken"
        @rotate-assignment-credential="rotateAssignmentCredential"
        @rotate-user-credentials="rotateUserCredentials"
        @open-node="emit('open-node', $event)"
      />
    </transition>
  </main>

  <user-form-modal
    v-model:show="formOpen"
    :user="editingUser"
    :assignable-nodes="assignableNodes"
    :saving="saving"
    @submit="saveUser"
  />
  <credential-dialog
    :show="credentialOpen"
    :title="credentialTitle"
    :credentials="credentials"
    @update:show="setCredentialOpen"
  />
  <subscription-token-form-modal
    v-model:show="subscriptionFormOpen"
    :saving="subscriptionSaving"
    @submit="createSubscriptionToken"
  />
  <subscription-token-dialog
    :show="issuedSubscriptionOpen"
    :issued="issuedSubscription"
    @update:show="setIssuedSubscriptionOpen"
  />
</template>
