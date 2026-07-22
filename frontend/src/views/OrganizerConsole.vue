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
} from '@element-plus/icons-vue'
import api from '../api'
import EventCreationDrawer from '../components/organizer/EventCreationDrawer.vue'
import TicketVerificationPanel from '../components/organizer/TicketVerificationPanel.vue'

const router = useRouter()
const loading = ref(true)
const workspaceLoading = ref(false)
const memberships = ref([])
const organizerId = ref('')
const overview = ref({
  on_sale_events: 0,
  paid_tickets: 0,
  paid_revenue_cents: 0,
  pending_payment_orders: 0,
})
const events = ref([])
const venues = ref([])
const orders = ref([])
const orderTotal = ref(0)
const activeSection = ref('overview')
const creationVisible = ref(false)
const selectedDraft = ref(null)
const venueDialogVisible = ref(false)
const venueSubmitting = ref(false)
const isMobile = ref(window.matchMedia('(max-width: 600px)').matches)
const venueForm = reactive({
  name: '',
  city: '武汉',
  district: '',
  address: '',
  timezone: 'Asia/Shanghai',
})

const currentMembership = computed(() =>
  memberships.value.find(item => String(item.organizer.id) === String(organizerId.value))
)
const currentOrganizer = computed(() => currentMembership.value?.organizer)

const mobileMedia = window.matchMedia('(max-width: 600px)')
const updateMobile = event => { isMobile.value = event.matches }

onMounted(() => {
  mobileMedia.addEventListener('change', updateMobile)
  loadMemberships()
})
onBeforeUnmount(() => mobileMedia.removeEventListener('change', updateMobile))
watch(organizerId, (value, oldValue) => {
  if (value && value !== oldValue) loadWorkspace()
})

async function loadMemberships() {
  loading.value = true
  try {
    const res = await api.organizerGetMine()
    memberships.value = res.data || []
    organizerId.value = memberships.value[0]?.organizer?.id || ''
    if (organizerId.value) await loadWorkspace()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '无法加载主办方权限')
  } finally {
    loading.value = false
  }
}

async function loadWorkspace() {
  if (!organizerId.value) return
  workspaceLoading.value = true
  try {
    const [overviewRes, venueRes, eventRes, orderRes] = await Promise.all([
      api.organizerGetOverview(organizerId.value),
      api.organizerGetVenues(organizerId.value),
      api.organizerGetEvents(organizerId.value, { page: 1, page_size: 50 }),
      api.organizerGetOrders(organizerId.value, { page: 1, page_size: 10 }),
    ])
    overview.value = overviewRes.data
    venues.value = venueRes.data || []
    events.value = eventRes.data?.list || []
    orders.value = orderRes.data?.list || []
    orderTotal.value = orderRes.data?.total || 0
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
    published: { label: '售票中', type: 'success' },
    cancelled: { label: '已取消', type: 'info' },
    finished: { label: '已结束', type: 'info' },
  }[status] || { label: status, type: 'info' }
}

function orderStatus(status) {
  return {
    queued: { label: '排队中', type: 'info' },
    pending_payment: { label: '待支付', type: 'warning' },
    paid: { label: '已支付', type: 'success' },
    cancelled: { label: '已取消', type: 'info' },
    failed: { label: '失败', type: 'danger' },
  }[status] || { label: status, type: 'info' }
}

function firstSession(event) {
  return event.sessions?.[0]
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
</script>

<template>
  <div class="console-shell">
    <header class="console-topbar">
      <button class="console-brand" type="button" @click="router.push('/')">赴场</button>
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
    <template v-else-if="!memberships.length">
      <div class="console-state empty">
        <strong>你还不属于任何主办方</strong>
        <p>主办方需要由平台管理员创建并指定负责人。当前账号仍可返回购票站正常购票。</p>
        <el-button type="primary" @click="router.push('/')">返回购票站</el-button>
      </div>
    </template>
    <template v-else>
      <aside class="console-sidebar">
        <button :class="{ active: activeSection === 'overview' }" type="button" @click="scrollTo('overview')"><el-icon><DataAnalysis /></el-icon>运营概览</button>
        <button :class="{ active: activeSection === 'events' }" type="button" @click="scrollTo('events')"><el-icon><Calendar /></el-icon>活动管理</button>
        <button :class="{ active: activeSection === 'orders' }" type="button" @click="scrollTo('orders')"><el-icon><Document /></el-icon>订单</button>
        <button :class="{ active: activeSection === 'verification' }" type="button" @click="scrollTo('verification')"><el-icon><Tickets /></el-icon>现场核销</button>
      </aside>

      <main v-loading="workspaceLoading" class="console-main">
        <section id="overview" class="console-heading">
          <div>
            <h1>赴场主办方工作台</h1>
            <p>{{ currentOrganizer?.name }} · {{ currentMembership?.role === 'owner' ? '负责人' : '运营成员' }}</p>
          </div>
          <div class="heading-actions">
            <el-button @click="venueDialogVisible = true">新增场馆</el-button>
            <el-button type="primary" :icon="Plus" @click="openCreate">创建活动</el-button>
          </div>
        </section>

        <section class="metric-band" aria-label="售票概览">
          <div><span>售票中活动</span><strong>{{ overview.on_sale_events }}</strong><small>场</small></div>
          <div><span>已支付票数</span><strong>{{ overview.paid_tickets }}</strong><small>张</small></div>
          <div><span>已收款金额</span><strong>{{ formatMoney(overview.paid_revenue_cents) }}</strong></div>
          <div><span>待支付订单</span><strong>{{ overview.pending_payment_orders }}</strong><small>笔</small></div>
        </section>

        <section id="events" class="console-section">
          <header>
            <div><h2>活动管理</h2><p>草稿可继续补齐场次和票档，发布后进入购票站。</p></div>
            <span>共 {{ events.length }} 场</span>
          </header>
          <div class="table-frame">
            <el-table :data="events" empty-text="还没有活动，先创建第一场">
              <el-table-column label="状态" :width="isMobile ? 78 : 92">
                <template #default="{ row }"><el-tag :type="eventStatus(row.status).type" effect="plain">{{ eventStatus(row.status).label }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="title" label="活动名称" :min-width="isMobile ? 150 : 220" />
              <el-table-column v-if="!isMobile" label="场次时间" width="150">
                <template #default="{ row }">{{ formatDate(firstSession(row)?.starts_at) }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="场馆" min-width="170">
                <template #default="{ row }">{{ firstSession(row)?.venue?.name || '尚未配置' }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="占用 / 剩余" width="120">
                <template #default="{ row }">{{ quota(row, 'sold_count') }} / {{ quota(row, 'remaining_quota') }}</template>
              </el-table-column>
              <el-table-column label="操作" :width="isMobile ? 120 : 260" align="right">
                <template #default="{ row }">
                  <button v-if="row.status === 'draft'" class="table-action" type="button" @click="resumeDraft(row)">继续配置</button>
                  <button v-if="row.status === 'published'" class="table-action" type="button" @click="unpublishEvent(row)">下架</button>
                  <button v-if="row.status === 'published'" class="table-action danger" type="button" @click="cancelEvent(row)">取消并退款</button>
                  <button v-if="row.status !== 'draft'" class="table-action" type="button" @click="router.push(`/events/${row.id}`)">查看前台</button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </section>

        <section id="orders" class="console-section">
          <header>
            <div><h2>近期订单</h2><p>显示最近 10 笔订单；金额以订单快照为准。</p></div>
            <span>共 {{ orderTotal }} 笔</span>
          </header>
          <div class="table-frame">
            <el-table :data="orders" empty-text="暂时还没有购票订单">
              <el-table-column v-if="!isMobile" prop="order_no" label="订单号" min-width="190" />
              <el-table-column label="活动" :min-width="isMobile ? 145 : 220">
                <template #default="{ row }">{{ row.items?.[0]?.event_title_snapshot || '活动快照缺失' }}</template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="票数" width="80">
                <template #default="{ row }">{{ row.items?.reduce((sum, item) => sum + item.quantity, 0) || 0 }}</template>
              </el-table-column>
              <el-table-column label="金额" :width="isMobile ? 90 : 130">
                <template #default="{ row }">{{ formatMoney(row.total_amount_cents) }}</template>
              </el-table-column>
              <el-table-column label="状态" :width="isMobile ? 85 : 100">
                <template #default="{ row }"><el-tag :type="orderStatus(row.status).type" effect="plain">{{ orderStatus(row.status).label }}</el-tag></template>
              </el-table-column>
              <el-table-column v-if="!isMobile" label="创建时间" width="150">
                <template #default="{ row }">{{ formatDate(row.CreateTime) }}</template>
              </el-table-column>
            </el-table>
          </div>
        </section>

        <section id="verification" class="console-section">
          <header>
            <div><h2>电子票核销</h2><p>工作人员扫码或输入票码，系统实时校验入场资格并留下操作记录。</p></div>
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
.console-sidebar button { height: 44px; padding: 0 16px; display: flex; align-items: center; gap: 12px; text-align: left; font-size: 14px; }
.console-sidebar button.active { border-left: 3px solid var(--red); background: rgba(181,52,41,.055); color: var(--red); }
.console-main { margin-left: 180px; padding: 32px clamp(28px, 4vw, 62px) 70px; }
.console-heading { scroll-margin-top: 82px; display: flex; justify-content: space-between; align-items: center; }
.console-heading h1 { margin: 0; font: 760 clamp(30px, 3vw, 42px) var(--font-display); }
.console-heading p, .console-section header p { margin: 7px 0 0; color: var(--muted); font-size: 12px; }
.heading-actions { display: flex; gap: 10px; }
.metric-band { margin-top: 28px; border: 1px solid var(--line-strong); display: grid; grid-template-columns: repeat(4, 1fr); }
.metric-band div { min-height: 100px; padding: 22px 28px; border-right: 1px solid var(--line); }
.metric-band div:last-child { border: 0; }
.metric-band span { display: block; margin-bottom: 11px; color: var(--muted); font-size: 12px; }
.metric-band strong { font: 680 29px var(--font-body); }
.metric-band small { margin-left: 6px; color: var(--muted); }
.console-section { margin-top: 34px; scroll-margin-top: 82px; }
.console-section > header { margin-bottom: 15px; display: flex; justify-content: space-between; align-items: end; }
.console-section h2 { margin: 0; font: 720 22px var(--font-display); }
.console-section header > span { color: var(--muted); font-size: 12px; }
.table-frame { border: 1px solid var(--line-strong); background: rgba(255,255,255,.22); }
.table-frame :deep(.el-table), .table-frame :deep(.el-table tr), .table-frame :deep(.el-table th.el-table__cell) { background: transparent; }
.table-frame :deep(.el-table th.el-table__cell) { color: var(--muted); font-size: 12px; font-weight: 600; }
.table-frame :deep(.el-table td.el-table__cell) { padding: 12px 0; }
.table-action { margin-left: 10px; padding: 4px 0; border: 0; background: transparent; color: var(--red); font-size: 12px; font-weight: 650; cursor: pointer; }
.table-action.danger { color: #8b1e16; }
.console-state { min-height: calc(100vh - 60px); display: grid; place-content: center; text-align: center; color: var(--muted); }
.console-state.empty strong { color: var(--ink); font: 700 26px var(--font-display); }
.console-state.empty p { max-width: 480px; line-height: 1.8; }
.venue-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
@media (max-width: 900px) {
  .console-sidebar { display: none; }
  .console-main { margin-left: 0; padding: 24px 18px 60px; }
  .metric-band { grid-template-columns: repeat(2, 1fr); }
  .metric-band div:nth-child(2) { border-right: 0; }
  .metric-band div:nth-child(-n+2) { border-bottom: 1px solid var(--line); }
}
@media (max-width: 600px) {
  .console-topbar { padding: 0 16px; }
  .back-store, .organizer-switcher i { display: none; }
  .organizer-switcher :deep(.el-select) { width: 150px; }
  .console-heading { align-items: flex-start; gap: 18px; }
  .heading-actions { flex-direction: column; }
  .metric-band { grid-template-columns: 1fr; }
  .metric-band div { border-right: 0; border-bottom: 1px solid var(--line); }
  .venue-grid { grid-template-columns: 1fr; }
}
</style>
