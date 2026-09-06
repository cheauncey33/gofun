<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Calendar,
  DataAnalysis,
  Document,
  Plus,
  Tickets,
  Timer,
} from '@element-plus/icons-vue'
import api from '../api'
import EventCreationDrawer from '../components/organizer/EventCreationDrawer.vue'
import EventEditDialog from '../components/organizer/EventEditDialog.vue'
import TicketVerificationPanel from '../components/organizer/TicketVerificationPanel.vue'
import RushSaleCreateDialog from '../components/organizer/RushSaleCreateDialog.vue'
import FunnelBoard from '../components/organizer/FunnelBoard.vue'
import HallAssetDialog from '../components/organizer/HallAssetDialog.vue'

const router = useRouter()
const loading = ref(true)
const workspaceLoading = ref(false)
const applying = ref(false)
const applications = ref([])
const organizerId = ref('')
const overview = ref({
  period_days: 7,
  event_id: '',
  session_id: '',
  on_sale_events: 0,
  paid_orders: 0,
  paid_tickets: 0,
  gross_revenue_cents: 0,
  refunded_orders: 0,
  refunded_amount_cents: 0,
  net_revenue_cents: 0,
  previous_paid_orders: 0,
  previous_paid_tickets: 0,
  previous_net_revenue_cents: 0,
  payment_failed_orders: 0,
  timeout_cancelled_orders: 0,
  payment_success_rate: 0,
  pending_payment_orders: 0,
  refunding_orders: 0,
  upcoming_sessions: 0,
  inventory_total: 0,
  inventory_occupied: 0,
  inventory_occupancy_rate: 0,
})
const overviewEventId = ref('')
const overviewSessionId = ref('')
const overviewLoading = ref(false)
const events = ref([])
const venues = ref([])
const orders = ref([])
const orderTotal = ref(0)
const orderPage = ref(1)
const orderKeyword = ref('')
const orderStatusFilter = ref('all')
const rushSales = ref([])
const rushVisible = ref(false)
const activeSection = ref('overview')
const creationVisible = ref(false)
const selectedDraft = ref(null)
const editVisible = ref(false)
const editingEvent = ref(null)
const venueDialogVisible = ref(false)
const hallAssetVisible = ref(false)
const venueSubmitting = ref(false)
const isMobile = ref(window.matchMedia('(max-width: 600px)').matches)
const venueForm = reactive({
  name: '',
  city: '武汉',
  district: '',
  address: '',
  timezone: 'Asia/Shanghai',
})
const applyForm = reactive({
  name: '',
  slug: '',
  contact_name: '',
  contact_phone: '',
  description: '',
})

const memberships = computed(() =>
  applications.value.filter(item =>
    item.organizer?.status === 'active' && item.organizer?.audit_status === 'approved',
  ),
)
const pendingApplication = computed(() =>
  applications.value.find(item => item.organizer?.audit_status === 'pending'),
)
const rejectedApplication = computed(() =>
  applications.value.find(item => item.organizer?.audit_status === 'rejected'),
)
const currentMembership = computed(() =>
  memberships.value.find(item => String(item.organizer.id) === String(organizerId.value))
)
const currentOrganizer = computed(() => currentMembership.value?.organizer)
const overviewEventOptions = computed(() =>
  (events.value || []).map(item => ({
    id: String(item.id),
    title: item.title,
  })),
)
const overviewSessionOptions = computed(() => {
  const event = (events.value || []).find(item => String(item.id) === String(overviewEventId.value))
  return (event?.sessions || []).map(item => ({
    id: String(item.id),
    label: sessionOptionLabel(item),
  }))
})
const overviewScoped = computed(() => Boolean(overviewEventId.value || overviewSessionId.value))

function trendLabel(current, previous) {
  const now = Number(current || 0)
  const before = Number(previous || 0)
  if (!before) return now ? '上期为 0，本期新增' : '与上期持平'
  const change = ((now - before) / Math.abs(before)) * 100
  if (Math.abs(change) < 0.05) return '与上期持平'
  return `较前 7 天 ${change > 0 ? '↑' : '↓'} ${Math.abs(change).toFixed(1)}%`
}

const mobileMedia = window.matchMedia('(max-width: 600px)')
const updateMobile = event => { isMobile.value = event.matches }

onMounted(() => {
  mobileMedia.addEventListener('change', updateMobile)
  loadMemberships()
})
onBeforeUnmount(() => mobileMedia.removeEventListener('change', updateMobile))
function organizerOrderQuery() {
  const params = { page: orderPage.value, page_size: 10 }
  if (orderStatusFilter.value !== 'all') params.status = orderStatusFilter.value
  if (orderKeyword.value.trim()) params.q = orderKeyword.value.trim()
  return params
}

async function loadOrders() {
  if (!organizerId.value) return
  try {
    const orderRes = await api.organizerGetOrders(organizerId.value, organizerOrderQuery())
    orders.value = orderRes.data?.list || []
    orderTotal.value = orderRes.data?.total || 0
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '订单加载失败')
  }
}

function searchOrders() {
  orderPage.value = 1
  loadOrders()
}

watch(organizerId, (value, oldValue) => {
  if (value && value !== oldValue) {
    orderPage.value = 1
    orderKeyword.value = ''
    orderStatusFilter.value = 'all'
    overviewEventId.value = ''
    overviewSessionId.value = ''
    loadWorkspace()
  }
})

watch([overviewEventId, overviewSessionId], () => {
  if (!organizerId.value || workspaceLoading.value) return
  loadOverview()
})

watch(events, () => {
  if (overviewEventId.value && !overviewEventOptions.value.some(item => item.id === String(overviewEventId.value))) {
    overviewEventId.value = ''
    overviewSessionId.value = ''
  }
})

async function loadMemberships() {
  loading.value = true
  try {
    const res = await api.organizerGetMine()
    applications.value = res.data || []
    organizerId.value = memberships.value[0]?.organizer?.id || ''
    if (organizerId.value) await loadWorkspace()
    const rejected = rejectedApplication.value?.organizer
    if (rejected && !applyForm.name) {
      applyForm.name = rejected.name || ''
      applyForm.slug = rejected.slug || ''
      applyForm.contact_name = rejected.contact_name || ''
      applyForm.contact_phone = rejected.contact_phone || ''
      applyForm.description = rejected.description || ''
    }
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '无法加载主办方权限')
  } finally {
    loading.value = false
  }
}

function slugFromName() {
  if (applyForm.slug.trim()) return
  applyForm.slug = applyForm.name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64)
}

async function submitOrganizerApply() {
  if (!applyForm.name.trim() || !applyForm.slug.trim()) {
    ElMessage.warning('请填写主办方名称和标识')
    return
  }
  applying.value = true
  try {
    await api.organizerApply({
      name: applyForm.name.trim(),
      slug: applyForm.slug.trim(),
      contact_name: applyForm.contact_name.trim(),
      contact_phone: applyForm.contact_phone.trim(),
      description: applyForm.description.trim(),
    })
    ElMessage.success('申请已提交，等待平台审核')
    await loadMemberships()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '申请失败')
  } finally {
    applying.value = false
  }
}

function overviewQuery() {
  const params = {}
  if (overviewEventId.value) params.event_id = overviewEventId.value
  if (overviewSessionId.value) params.session_id = overviewSessionId.value
  return params
}

async function loadOverview() {
  if (!organizerId.value) return
  overviewLoading.value = true
  try {
    const overviewRes = await api.organizerGetOverview(organizerId.value, overviewQuery())
    overview.value = overviewRes.data
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '经营概览加载失败')
  } finally {
    overviewLoading.value = false
  }
}

function onOverviewEventChange() {
  overviewSessionId.value = ''
}

async function loadWorkspace() {
  if (!organizerId.value) return
  workspaceLoading.value = true
  try {
    const [venueRes, eventRes, orderRes, rushRes] = await Promise.all([
      api.organizerGetVenues(organizerId.value),
      api.organizerGetEvents(organizerId.value, { page: 1, page_size: 50 }),
      api.organizerGetOrders(organizerId.value, organizerOrderQuery()),
      api.getRushSales().catch(() => ({ data: [] })),
    ])
    venues.value = venueRes.data || []
    events.value = eventRes.data?.list || []
    await loadOverview()
    if (editingEvent.value?.id) {
      editingEvent.value = events.value.find(item => String(item.id) === String(editingEvent.value.id)) || editingEvent.value
    }
    orders.value = orderRes.data?.list || []
    orderTotal.value = orderRes.data?.total || 0
    rushSales.value = (rushRes.data || []).filter(item =>
      String(item.organizer_id) === String(organizerId.value),
    )
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '工作台数据加载失败')
  } finally {
    workspaceLoading.value = false
  }
}

function openCreate() {
  if (!venues.value.length) {
    ElMessage.warning('请先创建至少一个场馆')
    venueDialogVisible.value = true
    return
  }
  selectedDraft.value = null
  creationVisible.value = true
}

function resumeDraft(event) {
  selectedDraft.value = event
  creationVisible.value = true
}

function openEdit(event) {
  editingEvent.value = event
  editVisible.value = true
}

async function unpublishEvent(event) {
  try {
    await ElMessageBox.confirm(
      '下架后活动回到草稿，前台不再展示。若已有已支付订单，请改用「取消并退款」。',
      '下架活动',
    )
    await api.organizerUnpublishEvent(organizerId.value, event.id)
    ElMessage.success('活动已下架为草稿')
    await loadWorkspace()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '下架失败')
  }
}

async function submitEventReview(event) {
  try {
    await api.organizerSubmitEventReview(organizerId.value, event.id)
    ElMessage.success('已提交审核，通过后才会出现在购票站')
    await loadWorkspace()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '提交审核失败')
  }
}

async function withdrawEventReview(event) {
  try {
    await api.organizerWithdrawEventReview(organizerId.value, event.id)
    ElMessage.success('已撤回，活动回到草稿')
    await loadWorkspace()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '撤回失败')
  }
}

async function cancelEvent(event) {
  try {
    await ElMessageBox.confirm(
      '将停售全部票档，并对未核销的已支付订单批量退款。已核销订单会跳过。是否继续？',
      '取消活动并退款',
      { type: 'warning' },
    )
    const res = await api.organizerCancelEvent(organizerId.value, event.id, {
      reason: '主办方取消活动',
      refund_paid: true,
    })
    const refund = res.data?.refund
    ElMessage.success(
      refund
        ? `活动已取消：退款 ${refund.refunded}，已核销跳过 ${refund.skipped_used}，失败 ${refund.failed}`
        : '活动已取消',
    )
    await loadWorkspace()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '取消失败')
  }
}

async function createVenue() {
  if (!venueForm.name.trim() || !venueForm.city.trim() || !venueForm.address.trim()) {
    ElMessage.warning('请完整填写场馆名称、城市和地址')
    return
  }
  venueSubmitting.value = true
  try {
    await api.organizerCreateVenue(organizerId.value, venueForm)
    ElMessage.success('场馆已创建')
    venueDialogVisible.value = false
    Object.assign(venueForm, {
      name: '',
      city: '武汉',
      district: '',
      address: '',
      timezone: 'Asia/Shanghai',
    })
    await loadWorkspace()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '创建场馆失败')
  } finally {
    venueSubmitting.value = false
  }
}

function scrollTo(id) {
  activeSection.value = id
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function eventStatus(status) {
  return {
    draft: { label: '待发布', type: 'warning' },
    pending_review: { label: '审核中', type: 'warning' },
    published: { label: '售票中', type: 'success' },
    cancelled: { label: '已取消', type: 'info' },
    finished: { label: '已结束', type: 'info' },
  }[status] || { label: status, type: 'info' }
}

function saleModeLabel(mode) {
  return mode === 'seated' ? '选座' : '计数'
}

function orderStatus(order) {
  if (order?.payment_status === 'refunded') return { label: '已退款', type: 'info' }
  if (order?.payment_status === 'refunding') return { label: '退款中', type: 'warning' }
  return {
    queued: { label: '排队中', type: 'info' },
    pending_payment: { label: '待支付', type: 'warning' },
    paid: { label: '已支付', type: 'success' },
    cancelled: { label: '已取消', type: 'info' },
    failed: { label: '失败', type: 'danger' },
  }[order?.status] || { label: order?.status, type: 'info' }
}

function firstSession(event) {
  return event.sessions?.[0]
}

function sessionSummary(event) {
  const sessions = event.sessions || []
  if (!sessions.length) return '尚未配置'
  const first = firstSession(event)
  const place = first?.venue?.name || '场馆待定'
  if (sessions.length === 1) return `${formatDate(first?.starts_at)} · ${place}`
  return `${sessions.length} 场 · ${formatDate(first?.starts_at)} 起`
}

function quota(event, field) {
  return (event.sessions || []).flatMap(item => item.ticket_tiers || [])
    .reduce((sum, tier) => sum + Number(tier[field] || 0), 0)
}

function formatMoney(cents) {
  return `¥${(Number(cents || 0) / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

function formatDate(value) {
  if (!value) return '尚未配置'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function sessionOptionLabel(session) {
  const place = session?.venue?.name || '场馆待定'
  return `${formatDate(session?.starts_at)} · ${place}`
}

function formatPct(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n)) return '—'
  return `${n.toFixed(n % 1 ? 1 : 0)}%`
}
</script>

<template>
  <div class="console-shell">
    <header class="console-topbar">
      <button class="console-brand" type="button" @click="router.push('/')">Gofun</button>
      <span></span>
      <button class="back-store" type="button" @click="router.push('/')">返回购票站</button>
      <div v-if="currentOrganizer" class="organizer-switcher">
        <i>{{ currentOrganizer.name.slice(0, 1) }}</i>
        <el-select v-model="organizerId" aria-label="选择主办方">
          <el-option
            v-for="membership in memberships"
            :key="membership.organizer.id"
            :label="membership.organizer.name"
            :value="membership.organizer.id"
          />
        </el-select>
      </div>
    </header>

    <template v-if="loading">
      <div class="console-state">正在核对主办方权限…</div>
    </template>
    <template v-else-if="pendingApplication">
      <div class="console-state empty">
        <strong>主办方申请审核中</strong>
        <p>「{{ pendingApplication.organizer.name }}」审核中。</p>
        <el-button @click="router.push('/')">返回购票站</el-button>
      </div>
    </template>
    <template v-else-if="!memberships.length">
      <div class="console-state empty apply">
        <strong>{{ rejectedApplication ? '申请未通过，可修改后重提' : '开通主办方工作台' }}</strong>
        <p v-if="rejectedApplication?.organizer?.audit_note">驳回原因：{{ rejectedApplication.organizer.audit_note }}</p>
        <el-form class="apply-form" label-position="top" @submit.prevent="submitOrganizerApply">
          <el-form-item label="主办方名称">
            <el-input v-model="applyForm.name" maxlength="80" @blur="slugFromName" />
          </el-form-item>
          <el-form-item label="标识 slug">
            <el-input v-model="applyForm.slug" maxlength="64" placeholder="例如 wuhan-livehouse" />
          </el-form-item>
          <el-form-item label="联系人">
            <el-input v-model="applyForm.contact_name" />
          </el-form-item>
          <el-form-item label="联系电话">
            <el-input v-model="applyForm.contact_phone" maxlength="20" />
          </el-form-item>
          <el-form-item label="简介">
            <el-input v-model="applyForm.description" type="textarea" :rows="3" maxlength="400" />
          </el-form-item>
          <el-button type="primary" :loading="applying" @click="submitOrganizerApply">提交审核</el-button>
        </el-form>
      </div>
    </template>
    <template v-else>
      <aside class="console-sidebar">
        <button :class="{ active: activeSection === 'overview' }" type="button" @click="scrollTo('overview')"><el-icon><DataAnalysis /></el-icon>运营概览</button>
        <button :class="{ active: activeSection === 'events' }" type="button" @click="scrollTo('events')"><el-icon><Calendar /></el-icon>活动管理</button>
        <button :class="{ active: activeSection === 'rush' }" type="button" @click="scrollTo('rush')"><el-icon><Timer /></el-icon>限时开售</button>
        <button :class="{ active: activeSection === 'orders' }" type="button" @click="scrollTo('orders')"><el-icon><Document /></el-icon>订单</button>
        <button :class="{ active: activeSection === 'verification' }" type="button" @click="scrollTo('verification')"><el-icon><Tickets /></el-icon>现场核销</button>
      </aside>

      <main v-loading="workspaceLoading" class="console-main">
        <section id="overview" class="console-heading">
          <div>
          <h1>Gofun 主办方工作台</h1>
            <p>{{ currentOrganizer?.name }} · {{ currentMembership?.role === 'owner' ? '负责人' : '运营成员' }}</p>
          </div>
          <div class="heading-actions">
            <el-button @click="venueDialogVisible = true">新增场馆</el-button>
            <el-button @click="hallAssetVisible = true">厅图资产</el-button>
            <el-button @click="rushVisible = true">配置开售</el-button>
            <el-button type="primary" :icon="Plus" @click="openCreate">创建活动</el-button>
          </div>
        </section>

        <section class="overview-block" aria-labelledby="business-overview-title">
          <header class="overview-heading">
            <div>
              <h2 id="business-overview-title">近 7 天经营结果</h2>
              <p>按支付和退款实际发生时间统计，与前 7 天对比。可按活动或场次下钻。</p>
            </div>
            <div class="overview-filters">
              <el-select
                v-model="overviewEventId"
                clearable
                placeholder="全部活动"
                aria-label="筛选活动"
                @change="onOverviewEventChange"
              >
                <el-option
                  v-for="item in overviewEventOptions"
                  :key="item.id"
                  :label="item.title"
                  :value="item.id"
                />
              </el-select>
              <el-select
                v-model="overviewSessionId"
                clearable
                placeholder="全部场次"
                aria-label="筛选场次"
                :disabled="!overviewEventId"
              >
                <el-option
                  v-for="item in overviewSessionOptions"
                  :key="item.id"
                  :label="item.label"
                  :value="item.id"
                />
              </el-select>
            </div>
          </header>
          <div v-loading="overviewLoading" class="metric-band business-metrics">
            <div>
              <span>净收款</span>
              <strong>{{ formatMoney(overview.net_revenue_cents) }}</strong>
              <small>{{ trendLabel(overview.net_revenue_cents, overview.previous_net_revenue_cents) }}</small>
            </div>
            <div>
              <span>支付订单</span>
              <strong>{{ overview.paid_orders }}</strong><em>笔</em>
              <small>{{ trendLabel(overview.paid_orders, overview.previous_paid_orders) }}</small>
            </div>
            <div>
              <span>售出票数</span>
              <strong>{{ overview.paid_tickets }}</strong><em>张</em>
              <small>{{ trendLabel(overview.paid_tickets, overview.previous_paid_tickets) }}</small>
            </div>
            <div>
              <span>退款</span>
              <strong>{{ formatMoney(overview.refunded_amount_cents) }}</strong>
              <small>{{ overview.refunded_orders }} 笔 · 毛收款 {{ formatMoney(overview.gross_revenue_cents) }}</small>
            </div>
          </div>
          <div v-loading="overviewLoading" class="metric-band leak-metrics">
            <div :class="{ attention: overview.payment_failed_orders > 0 }">
              <span>支付失败</span>
              <strong>{{ overview.payment_failed_orders }}</strong><em>笔</em>
              <small>窗口内支付回调失败的订单，同一单只计一次</small>
            </div>
            <div :class="{ attention: overview.timeout_cancelled_orders > 0 }">
              <span>超时关单</span>
              <strong>{{ overview.timeout_cancelled_orders }}</strong><em>笔</em>
              <small>支付窗口内未付款，系统自动取消</small>
            </div>
            <div>
              <span>支付成功率</span>
              <strong>{{ formatPct(overview.payment_success_rate) }}</strong>
              <small>成交 / (成交 + 超时关单)，不含仍待支付</small>
            </div>
          </div>
        </section>

        <section class="overview-block" aria-labelledby="operation-overview-title">
          <header class="overview-heading">
            <div>
              <h2 id="operation-overview-title">当前运营状态</h2>
              <p>{{ overviewScoped ? '实时快照，已按所选活动或场次收窄' : '实时快照，不受上方近 7 天统计周期影响' }}</p>
            </div>
          </header>
          <div class="operation-grid">
            <div><span>售票中活动</span><strong>{{ overview.on_sale_events }}</strong><small>场</small></div>
            <div :class="{ attention: overview.pending_payment_orders > 0 }"><span>待支付订单</span><strong>{{ overview.pending_payment_orders }}</strong><small>笔</small></div>
            <div :class="{ attention: overview.refunding_orders > 0 }"><span>退款处理中</span><strong>{{ overview.refunding_orders }}</strong><small>笔</small></div>
            <div><span>未来 7 天场次</span><strong>{{ overview.upcoming_sessions }}</strong><small>场</small></div>
            <div>
              <span>在售库存占用率</span>
              <strong>{{ Number(overview.inventory_occupancy_rate || 0).toFixed(1) }}%</strong>
              <small>{{ overview.inventory_occupied }} / {{ overview.inventory_total }} 张已占用</small>
            </div>
          </div>
        </section>

        <FunnelBoard :organizer-id="organizerId" :events="events" />

        <section id="events" class="console-section">
          <header>
            <div><h2>活动管理</h2></div>
            <span>共 {{ events.length }} 场</span>
          </header>
          <div class="table-frame">
            <el-table :data="events" empty-text="还没有活动，先创建第一场">
              <el-table-column label="状态" :width="isMobile ? 78 : 92">
                <template #default="{ row }"><el-tag :type="eventStatus(row.status).type" effect="plain">{{ eventStatus(row.status).label }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="title" label="活动名称" :min-width="isMobile ? 150 : 220" />
              <el-table-column v-if="!isMobile" label="卖法" width="72">
                <template #default="{ row }">{{ saleModeLabel(row.sale_mode) }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="场次" min-width="220">
                <template #default="{ row }">{{ sessionSummary(row) }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="占用 / 剩余" width="120">
                <template #default="{ row }">{{ quota(row, 'sold_count') }} / {{ quota(row, 'remaining_quota') }}</template>
              </el-table-column>
              <el-table-column label="操作" :width="isMobile ? 132 : 300" align="right">
                <template #default="{ row }">
                  <button
                    v-if="row.status === 'draft' || row.status === 'published'"
                    class="table-action"
                    type="button"
                    @click="openEdit(row)"
                  >编辑</button>
                  <button v-if="row.status === 'draft'" class="table-action" type="button" @click="resumeDraft(row)">继续配置</button>
                  <button v-if="row.status === 'draft'" class="table-action" type="button" @click="submitEventReview(row)">提交审核</button>
                  <button v-if="row.status === 'pending_review'" class="table-action" type="button" @click="withdrawEventReview(row)">撤回审核</button>
                  <button v-if="row.status === 'published'" class="table-action" type="button" @click="unpublishEvent(row)">下架</button>
                  <button v-if="row.status === 'published'" class="table-action danger" type="button" @click="cancelEvent(row)">取消并退款</button>
                  <button v-if="row.status === 'published'" class="table-action" type="button" @click="router.push(`/events/${row.id}`)">查看前台</button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </section>

        <section id="rush" class="console-section">
          <header>
            <div><h2>限时开售</h2></div>
            <el-button type="primary" size="small" @click="rushVisible = true">配置开售</el-button>
          </header>
          <div class="table-frame">
            <el-table :data="rushSales" empty-text="还没有进行中的限时开售">
              <el-table-column prop="name" label="开售名称" min-width="160" />
              <el-table-column v-if="!isMobile" label="活动" min-width="160">
                <template #default="{ row }">{{ row.event_title || '—' }}</template>
              </el-table-column>
              <el-table-column label="价格" width="90">
                <template #default="{ row }">{{ formatMoney(row.rush_price_cents) }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="剩余" width="110">
                <template #default="{ row }">{{ row.remaining_quota }} / {{ row.total_quota }}</template>
              </el-table-column>
              <el-table-column label="开售时间" min-width="140">
                <template #default="{ row }">{{ formatDate(row.starts_at) }}</template>
              </el-table-column>
            </el-table>
          </div>
        </section>

        <section id="orders" class="console-section">
          <header>
            <div><h2>订单</h2></div>
            <span>共 {{ orderTotal }} 笔</span>
          </header>
          <div class="order-toolbar">
            <el-input
              v-model="orderKeyword"
              placeholder="搜索活动、场馆或订单号"
              clearable
              @keyup.enter="searchOrders"
              @clear="searchOrders"
            />
            <el-select v-model="orderStatusFilter" style="width: 140px" @change="searchOrders">
              <el-option label="全部状态" value="all" />
              <el-option label="待支付" value="pending" />
              <el-option label="已支付" value="paid" />
              <el-option label="已取消" value="cancelled" />
              <el-option label="已退款" value="refunded" />
            </el-select>
            <el-button @click="searchOrders">搜索</el-button>
          </div>
          <div class="table-frame">
            <el-table :data="orders" empty-text="暂时还没有购票订单">
              <el-table-column v-if="!isMobile" prop="order_no" label="订单号" min-width="190" />
              <el-table-column label="活动" :min-width="isMobile ? 145 : 220">
                <template #default="{ row }">{{ row.items?.[0]?.event_title_snapshot || '—' }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="票数" width="80">
                <template #default="{ row }">{{ row.items?.reduce((sum, item) => sum + item.quantity, 0) || 0 }}</template>
              </el-table-column>
              <el-table-column label="金额" :width="isMobile ? 90 : 130">
                <template #default="{ row }">{{ formatMoney(row.total_amount_cents) }}</template>
              </el-table-column>
              <el-table-column label="状态" :width="isMobile ? 85 : 100">
                <template #default="{ row }"><el-tag :type="orderStatus(row).type" effect="plain">{{ orderStatus(row).label }}</el-tag></template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="创建时间" width="150">
                <template #default="{ row }">{{ formatDate(row.create_time || row.CreateTime) }}</template>
              </el-table-column>
            </el-table>
          </div>
          <div v-if="orderTotal > 10" class="order-pager">
            <el-pagination
              background
              layout="prev, pager, next"
              :page-size="10"
              :current-page="orderPage"
              :total="orderTotal"
              @current-change="(page) => { orderPage = page; loadOrders() }"
            />
          </div>
        </section>

        <section id="verification" class="console-section">
          <header>
            <div><h2>电子票核销</h2></div>
          </header>
          <TicketVerificationPanel :organizer-id="organizerId" />
        </section>
      </main>
    </template>

    <el-dialog v-model="venueDialogVisible" title="新增场馆" width="min(520px, 92vw)">
      <el-form label-position="top">
        <el-form-item label="场馆名称"><el-input v-model="venueForm.name" placeholder="例如：武汉光谷青年剧场" /></el-form-item>
        <div class="venue-grid">
          <el-form-item label="城市"><el-input v-model="venueForm.city" /></el-form-item>
          <el-form-item label="区县"><el-input v-model="venueForm.district" /></el-form-item>
        </div>
        <el-form-item label="详细地址"><el-input v-model="venueForm.address" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="venueDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="venueSubmitting" @click="createVenue">保存场馆</el-button>
      </template>
    </el-dialog>

    <EventCreationDrawer
      v-model="creationVisible"
      :organizer-id="organizerId"
      :venues="venues"
      :draft-event="selectedDraft"
      @completed="loadWorkspace"
    />
    <HallAssetDialog v-model="hallAssetVisible" :organizer-id="organizerId" :venues="venues" />
    <EventEditDialog
      v-model="editVisible"
      :organizer-id="organizerId"
      :event="editingEvent"
      :venues="venues"
      @saved="loadWorkspace"
    />
    <RushSaleCreateDialog
      v-model="rushVisible"
      :organizer-id="organizerId"
      :events="events"
      @created="loadWorkspace"
    />
  </div>
</template>

<style scoped>
.console-shell { min-height: 100vh; background: #f8f4ec; }
.console-topbar {
  height: 60px;
  padding: 0 28px;
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 18px;
  position: sticky;
  top: 0;
  z-index: 40;
  background: rgba(248,244,236,.96);
}
.console-brand, .back-store, .console-sidebar button {
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.console-brand { color: var(--ink); font: 800 26px var(--font-display); letter-spacing: .08em; }
.console-topbar > span { width: 1px; height: 24px; background: var(--line-strong); }
.back-store { font-size: 13px; }
.organizer-switcher { margin-left: auto; display: flex; align-items: center; gap: 10px; }
.organizer-switcher i { width: 31px; height: 31px; border-radius: 50%; background: var(--red); color: white; display: grid; place-content: center; font-style: normal; }
.organizer-switcher :deep(.el-select) { width: 190px; }
.console-sidebar {
  width: 180px;
  height: calc(100vh - 60px);
  padding: 28px 12px;
  border-right: 1px solid var(--line);
  position: fixed;
  top: 60px;
  left: 0;
  display: grid;
  align-content: start;
  gap: 8px;
}
.console-sidebar button { height: 44px; padding: 0 16px; border-radius: var(--radius-sm); display: flex; align-items: center; gap: 12px; text-align: left; font-size: 14px; }
.console-sidebar button.active { border-left: 3px solid var(--red); background: rgba(181,52,41,.055); color: var(--red); }
.console-main { margin-left: 180px; padding: 32px clamp(28px, 4vw, 62px) 70px; }
.console-heading { scroll-margin-top: 82px; display: flex; justify-content: space-between; align-items: center; }
.console-heading h1 { margin: 0; font: 760 clamp(30px, 3vw, 42px) var(--font-display); }
.console-heading p, .console-section header p { margin: 7px 0 0; color: var(--muted); font-size: 12px; }
.heading-actions { display: flex; gap: 10px; }
.overview-block { margin-top: 28px; }
.overview-heading { margin-bottom: 12px; display: flex; justify-content: space-between; align-items: end; gap: 16px; }
.overview-heading h2 { margin: 0; font: 720 22px var(--font-display); }
.overview-heading p { margin: 5px 0 0; color: var(--muted); font-size: 12px; }
.overview-filters { display: flex; gap: 10px; }
.overview-filters :deep(.el-select) { width: 180px; }
.metric-band { border: 1px solid var(--line-strong); border-radius: var(--radius-lg); overflow: hidden; display: grid; grid-template-columns: repeat(4, 1fr); }
.metric-band.leak-metrics { margin-top: 10px; grid-template-columns: repeat(3, 1fr); }
.metric-band div { min-height: 100px; padding: 22px 28px; border-right: 1px solid var(--line); }
.metric-band div:last-child { border: 0; }
.metric-band div.attention { background: rgba(181,52,41,.045); }
.metric-band span { display: block; margin-bottom: 11px; color: var(--muted); font-size: 12px; }
.metric-band strong { font: 680 29px var(--font-body); }
.metric-band em { margin-left: 6px; color: var(--muted); font-style: normal; font-size: 12px; }
.metric-band small { display: block; margin-top: 8px; color: var(--muted); font-size: 11px; }
.operation-grid { display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px; }
.operation-grid div { min-height: 92px; padding: 16px 18px; border: 1px solid var(--line); border-radius: var(--radius-md); background: rgba(255,255,255,.24); }
.operation-grid div.attention { border-color: rgba(181,52,41,.46); background: rgba(181,52,41,.045); }
.operation-grid span, .operation-grid small { display: block; color: var(--muted); font-size: 12px; }
.operation-grid strong { display: inline-block; margin: 8px 0 5px; font: 680 23px var(--font-body); }
.operation-grid small { line-height: 1.5; }
.console-section { margin-top: 34px; scroll-margin-top: 82px; }
.console-section > header { margin-bottom: 15px; display: flex; justify-content: space-between; align-items: end; }
.console-section h2 { margin: 0; font: 720 22px var(--font-display); }
.console-section header > span { color: var(--muted); font-size: 12px; }
.table-frame { border: 1px solid var(--line-strong); border-radius: var(--radius-md); overflow: hidden; background: rgba(255,255,255,.22); }
.table-frame :deep(.el-table), .table-frame :deep(.el-table tr), .table-frame :deep(.el-table th.el-table__cell) { background: transparent; }
.table-frame :deep(.el-table th.el-table__cell) { color: var(--muted); font-size: 12px; font-weight: 600; }
.table-frame :deep(.el-table td.el-table__cell) { padding: 12px 0; }
.table-action { margin-left: 10px; padding: 4px 0; border: 0; background: transparent; color: var(--red); font-size: 12px; font-weight: 650; cursor: pointer; }
.table-action.danger { color: #8b1e16; }
.order-toolbar { margin: 0 0 12px; display: flex; flex-wrap: wrap; gap: 10px; }
.order-toolbar :deep(.el-input) { width: min(280px, 100%); }
.order-pager { margin-top: 14px; display: flex; justify-content: flex-end; }
.console-state { min-height: calc(100vh - 60px); display: grid; place-content: center; text-align: center; color: var(--muted); }
.console-state.empty strong { color: var(--ink); font: 700 26px var(--font-display); }
.console-state.empty p { max-width: 480px; line-height: 1.8; }
.apply-form { width: min(420px, 100%); margin: 18px auto 0; text-align: left; }
.venue-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
@media (max-width: 900px) {
  .console-sidebar { display: none; }
  .console-main { margin-left: 0; padding: 24px 18px 60px; }
  .overview-heading { align-items: flex-start; flex-direction: column; }
  .metric-band { grid-template-columns: repeat(2, 1fr); }
  .metric-band.leak-metrics { grid-template-columns: 1fr; }
  .metric-band.business-metrics div:nth-child(2) { border-right: 0; }
  .metric-band.business-metrics div:nth-child(-n+2) { border-bottom: 1px solid var(--line); }
  .metric-band.leak-metrics div { border-right: 0; border-bottom: 1px solid var(--line); }
  .metric-band.leak-metrics div:last-child { border-bottom: 0; }
  .operation-grid { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 600px) {
  .console-topbar { padding: 0 16px; }
  .back-store, .organizer-switcher i { display: none; }
  .organizer-switcher :deep(.el-select) { width: 150px; }
  .console-heading { align-items: flex-start; gap: 18px; }
  .heading-actions { flex-direction: column; }
  .overview-filters { width: 100%; flex-wrap: wrap; }
  .overview-filters :deep(.el-select) { width: min(180px, 100%); }
  .metric-band { grid-template-columns: 1fr; }
  .metric-band div { border-right: 0; border-bottom: 1px solid var(--line); }
  .operation-grid { grid-template-columns: 1fr; }
  .venue-grid { grid-template-columns: 1fr; }
}
</style>
