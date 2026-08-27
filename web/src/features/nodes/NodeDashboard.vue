<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref } from "vue";
import {
  Bell,
	ChevronDown,
	Menu as MenuIcon,
  Plus,
  RefreshCw,
  Server,
} from "@lucide/vue";
import {
	NAlert,
	NAvatar,
	NBadge,
	NButton,
	NDrawer,
	NDrawerContent,
	NDropdown,
	NIcon,
	NLayout,
	NLayoutContent,
	NLayoutHeader,
	NLayoutSider,
	NSelect,
	NSpin,
	NTooltip,
	useDialog,
	useMessage,
} from "naive-ui";
import { api, APIError } from "../../api";
import ColorModePicker from "../../components/ColorModePicker.vue";
import ConsoleNavigation from "../../components/ConsoleNavigation.vue";
import { issueCount } from "../../lib/format";
import type {
  AlertRecord,
  BulkNodeAction,
  NodeAssetInput,
  NodeAssetRecord,
  NodeInput,
  NodeMetricSeries,
  NodeRecord,
  Session,
  SubscriptionOperationRecord,
  TrafficReport,
} from "../../types";
import AlertDrawer from "../alerts/AlertDrawer.vue";
import EnrollmentDialog from "./EnrollmentDialog.vue";
import FleetOverview from "./FleetOverview.vue";
import NodeDetailPage from "./NodeDetailPage.vue";
import NodeFormModal from "./NodeFormModal.vue";
import NodeTable from "./NodeTable.vue";
import NodeAssetModal from "./NodeAssetModal.vue";
import UserDashboard from "../users/UserDashboard.vue";
import OperationsDashboard from "../operations/OperationsDashboard.vue";

const SubscriptionDashboard = defineAsyncComponent(
  () => import("../subscriptions/SubscriptionDashboard.vue"),
);
const TrafficReportsDashboard = defineAsyncComponent(
  () => import("../reports/TrafficReportsDashboard.vue"),
);
const NotificationDashboard = defineAsyncComponent(
  () => import("../notifications/NotificationDashboard.vue"),
);

const props = defineProps<{ session: Session }>();
const emit = defineEmits<{ logout: []; "session-expired": [] }>();

const message = useMessage();
const dialog = useDialog();
const nodes = ref<NodeRecord[]>([]);
const nodeTrends = ref<Record<string, NodeMetricSeries>>({});
const nodeAssets = ref<NodeAssetRecord[]>([]);
const overviewTrafficReport = ref<TrafficReport | null>(null);
const overviewSubscriptions = ref<SubscriptionOperationRecord[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const loadError = ref("");
const formOpen = ref(false);
const saving = ref(false);
const editingNode = ref<NodeRecord | null>(null);
const detailNodeID = ref<string | null>(null);
const enrollmentNode = ref<NodeRecord | null>(null);
const assetNode = ref<NodeRecord | null>(null);
const assetSaving = ref(false);
type DashboardView = "overview" | "nodes" | "users" | "subscriptions" | "reports" | "notifications" | "operations" | "node-detail";
const activeView = ref<DashboardView>("overview");
const detailReturnView = ref<"overview" | "nodes" | "users" | "operations">("overview");
const operationsNodeFilter = ref("");
const alerts = ref<AlertRecord[]>([]);
const alertsOpen = ref(false);
const alertsLoading = ref(false);
const alertWorking = ref("");
const sidebarCollapsed = ref(false);
const mobileNavigationOpen = ref(false);
const selectedNodeIDs = ref<string[]>([]);
const bulkAction = ref<BulkNodeAction>("restart_core");
const bulkWorking = ref(false);
const bulkActionOptions = [
  { label: "探测核心", value: "probe_core" },
  { label: "重启核心", value: "restart_core" },
  { label: "备份配置", value: "backup_config" },
  { label: "重试配置同步", value: "retry_sync" },
];
const accountOptions = [{ label: "退出登录", key: "logout" }];
const currentTime = ref("");

const onlineCount = computed(() => nodes.value.filter((node) => node.status === "online").length);
const pendingCount = computed(() => nodes.value.filter((node) => node.status === "pending").length);
const issues = computed(() => issueCount(nodes.value));
const detailNode = computed(() => nodes.value.find((node) => node.id === detailNodeID.value) ?? null);
const assetByNode = computed(() => Object.fromEntries(nodeAssets.value.map((asset) => [asset.node_id, asset])));
const pageTitle = computed(() => {
	if (activeView.value === "overview") return "总览";
	if (activeView.value === "nodes") return "节点";
	if (activeView.value === "users") return "用户";
	if (activeView.value === "subscriptions") return "订阅运营";
	if (activeView.value === "reports") return "流量报表";
	if (activeView.value === "notifications") return "告警通知";
	if (activeView.value === "operations") return "操作记录";
	return detailNode.value?.name || "节点详情";
});
const headerStatus = computed(() => {
  return issues.value ? `${issues.value} 台节点需关注` : "系统正常";
});

function updateClock() {
  currentTime.value = new Intl.DateTimeFormat("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date());
}

function handleAccountAction(key: string | number) {
  if (key === "logout") emit("logout");
}

function refreshDashboard() {
  void loadNodes();
  void loadAlerts(true);
}

function handleAPIError(error: unknown, fallback: string) {
  if (error instanceof APIError && error.status === 401) {
    emit("session-expired");
    return;
  }
  message.error(error instanceof APIError ? error.message : fallback);
}

async function loadNodes(silent = false) {
  if (refreshing.value) return;
  if (!silent) loading.value = nodes.value.length === 0;
  refreshing.value = true;
  loadError.value = "";
  try {
    const [nextNodes, assets] = await Promise.all([api.listNodes(), api.listNodeAssets()]);
    nodes.value = nextNodes;
    nodeAssets.value = assets;
    selectedNodeIDs.value = selectedNodeIDs.value.filter((id) => nextNodes.some((node) => node.id === id));
    if (!silent || Object.keys(nodeTrends.value).length === 0) {
      void loadNodeTrends(nodes.value);
    }
    if (!silent) void loadOverviewOperations();
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
      return;
    }
    loadError.value = error instanceof APIError ? error.message : "节点列表加载失败。";
  } finally {
    loading.value = false;
    refreshing.value = false;
  }
}

async function loadNodeTrends(currentNodes = nodes.value) {
  const results = await Promise.allSettled(
    currentNodes.map(async (node) => [node.id, await api.getNodeMetrics(node.id, "6h")] as const),
  );
  const next = { ...nodeTrends.value };
  for (const result of results) {
    if (result.status === "fulfilled") next[result.value[0]] = result.value[1];
  }
  nodeTrends.value = next;
}

async function loadAlerts(silent = false) {
  if (!silent) alertsLoading.value = true;
  try {
    alerts.value = await api.listAlerts("active");
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      emit("session-expired");
    } else if (!silent) {
      handleAPIError(error, "告警加载失败。");
    }
  } finally {
    alertsLoading.value = false;
  }
}

async function acknowledgeAlert(alert: AlertRecord) {
  alertWorking.value = alert.id;
  try {
    await api.acknowledgeAlert(alert.id);
    await loadAlerts(true);
  } catch (error) {
    handleAPIError(error, "告警确认失败。");
  } finally {
    alertWorking.value = "";
  }
}

function selectAlertNode(nodeId: string) {
	blurActiveElement();
	openNode(nodeId, "overview");
	alertsOpen.value = false;
}

function openNodeFromUsers(nodeId: string) {
	openNode(nodeId, "users");
}

function openSelectedNodes(nodeIDs: string[]) {
  selectedNodeIDs.value = nodeIDs.filter((id) => nodes.value.some((node) => node.id === id));
  navigate("nodes");
}

function openNode(nodeId: string, from: "overview" | "nodes" | "users" | "operations" = "nodes") {
	detailNodeID.value = nodeId;
	detailReturnView.value = from;
	activeView.value = "node-detail";
}

function openOperations(nodeId = "") {
	operationsNodeFilter.value = nodeId;
	activeView.value = "operations";
}

function blurActiveElement() {
	if (document.activeElement instanceof HTMLElement) {
		document.activeElement.blur();
	}
}

function openMobileNavigation() {
	blurActiveElement();
	mobileNavigationOpen.value = true;
}

function focusMobileNavigation() {
	document.querySelector<HTMLButtonElement>(".console-mobile-drawer .console-nav__item")?.focus();
}

function openAlerts() {
	blurActiveElement();
	alertsOpen.value = true;
	void loadAlerts();
}

function navigate(view: "overview" | "nodes" | "users" | "subscriptions" | "reports" | "notifications" | "operations") {
	if (mobileNavigationOpen.value) {
		blurActiveElement();
	}
	mobileNavigationOpen.value = false;
	if (view === "operations") {
		openOperations();
		return;
	}
	activeView.value = view;
}

async function loadOverviewOperations() {
  const [reportResult, subscriptionsResult] = await Promise.allSettled([
    api.trafficReport("30d"),
    api.listSubscriptionOperations({ limit: 50 }),
  ]);
  if (reportResult.status === "fulfilled") overviewTrafficReport.value = reportResult.value;
  if (subscriptionsResult.status === "fulfilled") overviewSubscriptions.value = subscriptionsResult.value.subscriptions;
  const rejected = [reportResult, subscriptionsResult].find((result) => result.status === "rejected");
  if (rejected?.status === "rejected" && rejected.reason instanceof APIError && rejected.reason.status === 401) {
    emit("session-expired");
  }
}

function openAsset(node: NodeRecord) {
  assetNode.value = node;
}

async function saveAsset(input: NodeAssetInput) {
  if (!assetNode.value) return;
  assetSaving.value = true;
  try {
    const saved = await api.updateNodeAsset(assetNode.value.id, input);
    const index = nodeAssets.value.findIndex((asset) => asset.node_id === saved.node_id);
    if (index >= 0) nodeAssets.value[index] = saved;
    else nodeAssets.value.push(saved);
    assetNode.value = null;
    message.success("VPS 资产档案已更新");
  } catch (error) {
    handleAPIError(error, "VPS 资产档案保存失败。");
  } finally {
    assetSaving.value = false;
  }
}

function runBulk() {
  if (!selectedNodeIDs.value.length) return;
  const label = bulkActionOptions.find((option) => option.value === bulkAction.value)?.label ?? bulkAction.value;
  dialog.warning({
    title: `批量${label}`,
    content: `确认对 ${selectedNodeIDs.value.length} 台节点执行“${label}”？操作会分别进入各节点的有界队列。`,
    positiveText: "执行",
    negativeText: "取消",
    async onPositiveClick() {
      bulkWorking.value = true;
      try {
        const result = await api.bulkNodes(selectedNodeIDs.value, bulkAction.value);
        const failed = result.results.filter((item) => item.status === "failed").length;
        if (failed) message.warning(`${result.results.length - failed} 台已接受，${failed} 台失败`);
        else message.success(`${result.results.length} 台节点已接受批量操作`);
        selectedNodeIDs.value = [];
        await loadNodes(true);
      } catch (error) {
        handleAPIError(error, "批量操作失败。");
        return false;
      } finally {
        bulkWorking.value = false;
      }
      return true;
    },
  });
}

function openCreate() {
  editingNode.value = null;
  formOpen.value = true;
}

function openEdit(node: NodeRecord) {
  editingNode.value = node;
  formOpen.value = true;
}

async function saveNode(input: Required<NodeInput>) {
  saving.value = true;
  try {
    const saved = editingNode.value
      ? await api.updateNode(editingNode.value.id, input)
      : await api.createNode(input);
    formOpen.value = false;
    editingNode.value = null;
    await loadNodes(true);
		openNode(saved.id, "nodes");
    message.success(saved.desired_version === 1 ? "节点已添加" : "节点已更新");
  } catch (error) {
    handleAPIError(error, "节点保存失败。 ");
  } finally {
    saving.value = false;
  }
}

function archiveNode(node: NodeRecord) {
  dialog.warning({
    title: "归档节点",
    content: `确认归档“${node.name}”？该节点将从控制台列表中移除。`,
    positiveText: "归档",
    negativeText: "取消",
    positiveButtonProps: { type: "error" },
    async onPositiveClick() {
      try {
        await api.archiveNode(node.id);
				if (detailNodeID.value === node.id) {
					detailNodeID.value = null;
					activeView.value = "nodes";
				}
        await loadNodes(true);
        message.success("节点已归档");
      } catch (error) {
        handleAPIError(error, "节点归档失败。 ");
        return false;
      }
      return true;
    },
  });
}

function handleAction(action: "edit" | "enroll" | "archive", node: NodeRecord) {
  if (action === "edit") openEdit(node);
  if (action === "enroll") enrollmentNode.value = node;
  if (action === "archive") archiveNode(node);
}

let refreshTimer: number | undefined;
let clockTimer: number | undefined;
onMounted(() => {
  updateClock();
  loadNodes();
  loadAlerts(true);
  clockTimer = window.setInterval(updateClock, 1_000);
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") {
      loadNodes(true);
      loadAlerts(true);
    }
  }, 15_000);
});
onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
  window.clearInterval(clockTimer);
});
</script>

<template>
  <div class="app-shell">
    <n-layout has-sider class="console-shell">
      <n-layout-sider
        class="console-sidebar"
        :width="244"
        :collapsed-width="72"
        :collapsed="sidebarCollapsed"
        :show-trigger="false"
        :native-scrollbar="false"
      >
        <console-navigation
          :active-view="activeView"
          :collapsed="sidebarCollapsed"
          :online="onlineCount"
          :total="nodes.length"
          :issues="issues"
          @navigate="navigate"
          @collapse="sidebarCollapsed = !sidebarCollapsed"
        />
      </n-layout-sider>

      <n-layout class="console-stage">
        <n-layout-header class="console-header" bordered>
          <div class="console-header__identity">
            <n-button
              quaternary
              circle
              class="console-header__menu"
              aria-label="打开导航"
              @click="openMobileNavigation"
            >
              <template #icon><n-icon><menu-icon /></n-icon></template>
            </n-button>
            <div>
              <h1>{{ pageTitle }}</h1>
            </div>
          </div>

          <div class="console-header__actions">
            <span class="console-header__live" :class="{ 'is-warning': issues > 0 }">
              <i />
              <span>{{ headerStatus }}</span>
              <time>{{ currentTime }}</time>
            </span>
            <n-tooltip trigger="hover">
              <template #trigger>
                <n-button quaternary circle aria-label="刷新当前数据" :loading="refreshing" @click="refreshDashboard">
                  <template #icon><n-icon><refresh-cw /></n-icon></template>
                </n-button>
              </template>
              刷新
            </n-tooltip>
            <color-mode-picker />
            <n-tooltip trigger="hover">
              <template #trigger>
                <n-badge :value="alerts.length" :max="99" :show="alerts.length > 0">
                  <n-button
                    quaternary
                    circle
                    aria-label="查看告警"
                    @click="openAlerts"
                  >
                    <template #icon><n-icon><bell /></n-icon></template>
                  </n-button>
                </n-badge>
              </template>
              告警
            </n-tooltip>
            <n-button v-if="activeView === 'overview'" class="console-header__create" type="primary" size="small" aria-label="添加节点" @click="openCreate">
              <template #icon><n-icon><plus /></n-icon></template>
              添加节点
            </n-button>
            <n-dropdown trigger="click" :options="accountOptions" @select="handleAccountAction">
              <button class="console-header__account" type="button" aria-label="打开账户菜单">
                <n-avatar round :size="32">{{ props.session.admin.username.slice(0, 1).toUpperCase() }}</n-avatar>
                <span><strong>{{ props.session.admin.username }}</strong><small>Administrator</small></span>
                <chevron-down :size="14" aria-hidden="true" />
              </button>
            </n-dropdown>
          </div>
        </n-layout-header>

        <n-layout-content class="console-content" content-style="min-height: 100%;">
          <fleet-overview
            v-if="activeView === 'overview'"
            :nodes="nodes"
            :alerts="alerts"
            :loading="loading"
            :error="loadError"
            :trends="nodeTrends"
            :assets="assetByNode"
            :traffic-report="overviewTrafficReport"
            :subscriptions="overviewSubscriptions"
            @select="openNode($event.id, 'overview')"
            @refresh="loadNodes()"
            @create="openCreate"
            @alerts="openAlerts"
            @subscriptions="navigate('subscriptions')"
            @nodes="openSelectedNodes"
            @edit-asset="openAsset"
          />

          <main v-else-if="activeView === 'nodes'" id="nodes" class="workspace">
            <div class="page-heading page-heading--console">
              <div>
                <h2>节点目录</h2>
                <p>{{ onlineCount }} / {{ nodes.length }} 台主机正在响应</p>
              </div>
              <div class="page-heading__actions">
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-button circle secondary aria-label="刷新节点" :loading="refreshing" @click="loadNodes()">
                      <template #icon><n-icon><refresh-cw /></n-icon></template>
                    </n-button>
                  </template>
                  刷新
                </n-tooltip>
                <n-button type="primary" @click="openCreate">
                  <template #icon><n-icon><plus /></n-icon></template>
                  添加节点
                </n-button>
              </div>
            </div>

            <section class="fleet-summary" aria-label="节点摘要">
              <div class="fleet-summary__item"><span>全部节点</span><strong>{{ nodes.length }}</strong></div>
              <div class="fleet-summary__item fleet-summary__item--healthy"><span>在线</span><strong>{{ onlineCount }}</strong></div>
              <div class="fleet-summary__item fleet-summary__item--warning"><span>待连接</span><strong>{{ pendingCount }}</strong></div>
              <div class="fleet-summary__item fleet-summary__item--danger"><span>需关注</span><strong>{{ issues }}</strong></div>
            </section>

            <n-alert v-if="loadError" type="error" :show-icon="false" class="workspace-alert">
              <div class="alert-row"><span>{{ loadError }}</span><n-button text type="error" @click="loadNodes()">重新加载</n-button></div>
            </n-alert>

            <section v-if="selectedNodeIDs.length" class="bulk-commandbar" aria-label="批量节点操作">
              <strong>已选择 {{ selectedNodeIDs.length }} 台节点</strong>
              <n-select v-model:value="bulkAction" :options="bulkActionOptions" size="small" />
              <n-button size="small" type="primary" :loading="bulkWorking" @click="runBulk">执行</n-button>
              <n-button size="small" quaternary @click="selectedNodeIDs = []">取消选择</n-button>
            </section>

            <section class="node-surface" aria-label="节点列表">
              <div v-if="loading" class="surface-state"><n-spin :size="28" /></div>
              <div v-else-if="nodes.length === 0" class="surface-state surface-state--empty">
                <server :size="28" :stroke-width="1.7" aria-hidden="true" />
                <strong>尚未添加节点</strong>
                <n-button type="primary" size="small" @click="openCreate">
                  <template #icon><n-icon><plus /></n-icon></template>
                  添加节点
                </n-button>
              </div>
              <node-table
                v-else
                :nodes="nodes"
                :selected-node-ids="selectedNodeIDs"
                @select="openNode($event.id, 'nodes')"
                @action="handleAction"
                @update:selected-node-ids="selectedNodeIDs = $event"
              />
            </section>
          </main>

          <user-dashboard
            v-else-if="activeView === 'users'"
            :nodes="nodes"
            @nodes-changed="loadNodes(true)"
            @open-node="openNodeFromUsers"
            @session-expired="emit('session-expired')"
          />

          <subscription-dashboard
            v-else-if="activeView === 'subscriptions'"
            @session-expired="emit('session-expired')"
          />

          <traffic-reports-dashboard
            v-else-if="activeView === 'reports'"
            @session-expired="emit('session-expired')"
          />

          <notification-dashboard
            v-else-if="activeView === 'notifications'"
            @session-expired="emit('session-expired')"
          />

          <operations-dashboard
            v-else-if="activeView === 'operations'"
            :nodes="nodes"
            :initial-node-id="operationsNodeFilter"
            @select-node="openNode($event, 'operations')"
            @session-expired="emit('session-expired')"
          />

          <node-detail-page
            v-else-if="activeView === 'node-detail' && detailNode"
            :node="detailNode"
            @back="activeView = detailReturnView"
            @edit="openEdit"
            @enroll="enrollmentNode = $event"
            @operations="openOperations"
            @changed="loadNodes(true)"
            @session-expired="emit('session-expired')"
          />
        </n-layout-content>
      </n-layout>
    </n-layout>

    <n-drawer
      v-model:show="mobileNavigationOpen"
      placement="left"
      :width="280"
      :auto-focus="false"
      class="console-mobile-drawer"
      @after-enter="focusMobileNavigation"
    >
      <n-drawer-content body-content-style="padding: 0;">
        <console-navigation
          mobile
          :active-view="activeView"
          :online="onlineCount"
          :total="nodes.length"
          :issues="issues"
          @navigate="navigate"
        />
      </n-drawer-content>
    </n-drawer>

    <node-form-modal
      v-model:show="formOpen"
      :node="editingNode"
      :saving="saving"
      @submit="saveNode"
    />
    <enrollment-dialog
      :show="enrollmentNode !== null"
      :node="enrollmentNode"
      @update:show="!$event && (enrollmentNode = null)"
      @session-expired="emit('session-expired')"
    />
    <node-asset-modal
      :show="assetNode !== null"
      :node="assetNode"
      :asset="assetNode ? (assetByNode[assetNode.id] ?? null) : null"
      :saving="assetSaving"
      @update:show="!$event && (assetNode = null)"
      @submit="saveAsset"
    />
    <alert-drawer
      v-model:show="alertsOpen"
      :alerts="alerts"
      :loading="alertsLoading"
      :working="alertWorking"
      @refresh="loadAlerts()"
      @acknowledge="acknowledgeAlert"
      @select-node="selectAlertNode"
    />
  </div>
</template>
