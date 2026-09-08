<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'
import { logoutSession } from '../stores/session'

const router = useRouter()
const loading = ref(true)
const acting = ref('')
const section = ref('overview')
const overview = ref({
  pending_organizers: 0,
  pending_events: 0,
  active_organizers: 0,
  published_events: 0,
  period_days: 7,
  paid_orders: 0,
  paid_tickets: 0,
  gross_revenue_cents: 0,
  refunded_orders: 0,
  refunded_amount_cents: 0,
  net_revenue_cents: 0,
  previous_paid_orders: 0,
  previous_paid_tickets: 0,
  previous_gross_revenue_cents: 0,
  previous_net_revenue_cents: 0,
  payment_failed_orders: 0,
  timeout_cancelled_orders: 0,
  payment_success_rate: 0,
  runtime: {},
})
const organizerFilter = ref('pending')
const organizers = ref([])
const pendingOrganizers = ref([])
const pendingEvents = ref([])

const runtime = computed(() => overview.value.runtime || {})
let overviewTimer = 0

function formatMs(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n) || n <= 0) return ''
  if (n < 1) return `${n.toFixed(1)} ms`
  return `${Math.round(n)} ms`
}

function formatAge(value) {
  const seconds = Number(value || 0)
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return `${Math.round(seconds)} 秒`
  if (seconds < 3600) return `${Math.round(seconds / 60)} 分钟`
  return `${(seconds / 3600).toFixed(1)} 小时`
}

function waitHint(count, ageSeconds) {
  const n = Math.round(Number(count || 0))
  if (n <= 0) return '没有积压'
  const age = formatAge(ageSeconds)
  return age ? `${n} 笔，已等 ${age}` : `${n} 笔处理中`
}

function formatPct(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n)) return '—'
  return `${n.toFixed(n % 1 ? 1 : 0)}%`
}

function formatMoney(cents) {
  return `¥${(Number(cents || 0) / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

function trendMeta(current, previous) {
  const now = Number(current || 0)
  const before = Number(previous || 0)
  if (!before && !now) return { tone: 'flat', label: '与上期持平' }
  if (!before) return { tone: 'up', label: '上期为 0，本期新增' }
  const change = ((now - before) / Math.abs(before)) * 100
  if (Math.abs(change) < 0.05) return { tone: 'flat', label: '与上期持平' }
  return {
    tone: change > 0 ? 'up' : 'down',
    label: `较前 7 天 ${change > 0 ? '↑' : '↓'} ${Math.abs(change).toFixed(1)}%`,
  }
}

const revenueTrend = computed(() => trendMeta(overview.value.gross_revenue_cents, overview.value.previous_gross_revenue_cents))
const refundRateValue = computed(() => {
  const gross = Number(overview.value.gross_revenue_cents || 0)
  const refunded = Number(overview.value.refunded_amount_cents || 0)
  if (gross <= 0) return 0
  return Math.round((refunded / gross) * 1000) / 10
})
const refundRateLabel = computed(() => (
  Number(overview.value.gross_revenue_cents || 0) > 0 ? formatPct(refundRateValue.value) : '—'
))

const workItems = computed(() => [
  {
    key: 'organizers',
    title: '主办方申请',
    count: overview.value.pending_organizers,
    action: '去审批',
    go: 'organizers',
  },
  {
    key: 'events',
    title: '活动上架',
    count: overview.value.pending_events,
    action: '去审核',
    go: 'events',
  },
])

const opsTiles = computed(() => {
  const r = runtime.value
  const qps = Number(r.http_qps || 0)
  const p50 = Number(r.http_p50_ms || 0)
  const p95 = Number(r.http_p95_ms || 0)
  const err = Number(r.http_error_rate || 0)
  const backlog = Number(r.outbox_pending || 0) + Number(r.stock_reservation_pending || 0) + Number(r.mq_work_queue_ready || 0)
  return [
    {
      key: 'qps',
      label: '当前流量',
      value: `${Number.isInteger(qps) ? qps : qps.toFixed(1)} /s`,
      hint: `近 10 秒 · 在途 ${Math.round(r.http_in_flight || 0)}`,
      tone: qps > 80 ? 'warn' : '',
    },
    {
      key: 'avg',
      label: '平均延迟',
      value: formatMs(p50) || '暂无请求',
      hint: p50 > 0 ? `中位 p50 · 尾部 p95 ${formatMs(p95) || '—'}` : '近 5 分钟没有请求',
      tone: p50 > 400 ? 'warn' : '',
    },
    {
      key: 'p95',
      label: '尾部延迟',
      value: formatMs(p95) || '暂无请求',
      hint: p95 > 0 ? `p95 · p99 ${formatMs(r.http_p99_ms) || '—'}` : '近 5 分钟没有请求',
      tone: p95 > 1000 ? 'alert' : p95 > 500 ? 'warn' : '',
    },
    {
      key: 'error',
      label: '错误率',
      value: formatPct(err),
      hint: `近 5 分钟 5xx ${Math.round(r.http_5xx || 0)} / 4xx ${Math.round(r.http_4xx || 0)}`,
      tone: err > 1 ? 'alert' : Number(r.http_5xx || 0) > 0 ? 'warn' : '',
    },
    {
      key: 'backlog',
      label: '积压',
      value: String(Math.round(backlog)),
      hint: backlog > 0 ? '有未完成的出票或预扣' : '没有积压',
      tone: backlog > 100 ? 'alert' : backlog > 0 ? 'warn' : '',
    },
  ]
})

const pipelineRows = computed(() => {
  const r = runtime.value
  const accept = formatMs(r.order_accept_p95_ms)
  const consume = formatMs(r.consumer_tx_p95_ms)
  const recovery = formatAge(r.stock_recovery_last_success_ago_seconds)
  return [
    {
      label: '订单确认积压',
      value: Math.round(r.outbox_pending || 0),
      hint: waitHint(r.outbox_pending, r.outbox_oldest_age_seconds),
      alert: Number(r.outbox_pending || 0) > 0,
    },
    {
      label: '库存预扣',
      value: Math.round(r.stock_reservation_pending || 0),
      hint: waitHint(r.stock_reservation_pending, r.stock_reservation_oldest_age_seconds),
      alert: Number(r.stock_reservation_pending || 0) > 0,
    },
    {
      label: '订单队列',
      value: Math.round(r.mq_work_queue_ready || 0),
      hint: Number(r.mq_work_queue_ready || 0) > 0
        ? `${Math.round(r.mq_work_queue_consumers || 0)} 个消费者在消化`
        : '队列畅通',
      alert: Number(r.mq_work_queue_ready || 0) > 100,
    },
    {
      label: '下单确认耗时',
      value: accept || '暂无样本',
      hint: consume ? `落库 p95 ${consume}` : '近窗没有下单样本',
      alert: Number(r.order_accept_p95_ms || 0) > 5000,
    },
    {
      label: '库存恢复',
      value: recovery ? `${recovery}前` : '尚未扫描',
      hint: recovery ? '上次成功对账' : '启动后还没有成功扫描',
      alert: Number(r.stock_recovery_last_success_ago_seconds || 0) > 120,
    },
    {
      label: '数据库连接',
      value: formatPct(r.db_pool_usage_rate),
      hint: `${Math.round(r.db_connections_in_use || 0)} / ${Math.round(r.db_max_open_connections || 0)} 在用`,
      alert: Number(r.db_pool_usage_rate || 0) > 80,
    },
  ]
})

onMounted(() => {
  loadAll()
  overviewTimer = window.setInterval(() => {
    if (section.value === 'overview') loadOverview()
  }, 5000)
})

onUnmounted(() => {
  window.clearInterval(overviewTimer)
})

async function logout() {
  await logoutSession()
  router.push('/')
}

async function loadAll() {
  loading.value = true
  try {
    await Promise.all([loadOverview(), loadOrganizers(), loadPendingOrganizers(), loadPendingEvents()])
  } finally {
    loading.value = false
  }
}

async function loadOverview() {
  const res = await api.adminGetOverview()
  overview.value = res.data || overview.value
}

async function loadOrganizers() {
  const res = await api.adminGetOrganizers({
    page: 1,
    page_size: 50,
    audit_status: organizerFilter.value === 'all' ? undefined : organizerFilter.value,
  })
  organizers.value = res.data?.list || []
}

async function loadPendingOrganizers() {
  const res = await api.adminGetOrganizers({ page: 1, page_size: 50, audit_status: 'pending' })
  pendingOrganizers.value = res.data?.list || []
}

async function loadPendingEvents() {
  const res = await api.adminGetPendingEvents({ page: 1, page_size: 50 })
  pendingEvents.value = res.data?.list || []
}

async function approveOrganizer(row) {
  acting.value = `org-ok-${row.organizer.id}`
  try {
    await api.adminApproveOrganizer(row.organizer.id)
    ElMessage.success('已批准，对方可以进入主办方工作台')
    await loadAll()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '批准失败')
  } finally {
    acting.value = ''
  }
}

async function rejectOrganizer(row) {
  try {
    const { value } = await ElMessageBox.prompt('驳回原因（选填）', '驳回主办方申请', {
      confirmButtonText: '驳回',
      cancelButtonText: '取消',
      inputPlaceholder: '例如资料不完整',
    })
    acting.value = `org-no-${row.organizer.id}`
    await api.adminRejectOrganizer(row.organizer.id, value)
    ElMessage.success('已驳回')
    await loadAll()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '驳回失败')
  } finally {
    acting.value = ''
  }
}

async function approveEvent(row) {
  acting.value = `evt-ok-${row.event.id}`
  try {
    await api.adminApproveEvent(row.event.id)
    ElMessage.success('活动已上架到购票站')
    await loadAll()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '上架失败')
  } finally {
    acting.value = ''
  }
}

async function rejectEvent(row) {
  try {
    const { value } = await ElMessageBox.prompt('驳回原因', '驳回上架申请', {
      confirmButtonText: '驳回',
      cancelButtonText: '取消',
      inputPlaceholder: '主办方会在草稿里看到这条说明',
    })
    acting.value = `evt-no-${row.event.id}`
    await api.adminRejectEvent(row.event.id, value)
    ElMessage.success('已驳回，活动回到草稿')
    await loadAll()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '驳回失败')
  } finally {
    acting.value = ''
  }
}

function auditLabel(status) {
  return {
    pending: '待审核',
    approved: '已通过',
    rejected: '已驳回',
  }[status] || status
}
</script>

<template>
  <div class="admin-shell">
    <header class="admin-topbar">
      <button class="brand" type="button" @click="router.push('/')">Gofun</button>
      <span>平台管理</span>
      <div class="topbar-actions">
        <button class="store-link" type="button" @click="router.push('/')">查看购票站</button>
        <button class="logout" type="button" @click="logout">退出登录</button>
      </div>
    </header>
    <aside>
      <button :class="{ active: section === 'overview' }" type="button" @click="section = 'overview'">工作台</button>
      <button :class="{ active: section === 'organizers' }" type="button" @click="section = 'organizers'">
        主办方申请
        <i v-if="overview.pending_organizers">{{ overview.pending_organizers }}</i>
      </button>
      <button :class="{ active: section === 'events' }" type="button" @click="section = 'events'">
        活动上架
        <i v-if="overview.pending_events">{{ overview.pending_events }}</i>
      </button>
    </aside>
    <main v-loading="loading">
      <section v-if="section === 'overview'">
        <header class="heading">
          <div>
            <h1>平台工作台</h1>
            <p>先看系统是否正常，再处理审核和全站销售</p>
          </div>
          <el-button @click="loadAll">刷新</el-button>
        </header>

        <header class="section-heading first">
          <h2>平台健康</h2>
        </header>
        <section class="sre-grid" aria-label="平台健康">
          <article
            v-for="tile in opsTiles"
            :key="tile.key"
            class="sre-tile"
            :class="tile.tone"
          >
            <span>{{ tile.label }}</span>
            <strong>{{ tile.value }}</strong>
            <small>{{ tile.hint }}</small>
          </article>
        </section>
        <ul class="pipeline-list">
          <li v-for="row in pipelineRows" :key="row.label" :class="{ alert: row.alert }">
            <span>{{ row.label }}</span>
            <strong>{{ row.value }}</strong>
            <small>{{ row.hint }}</small>
          </li>
        </ul>

        <header class="section-heading">
          <h2>待办审批</h2>
        </header>
        <div class="todo-grid">
          <button
            v-for="item in workItems"
            :key="item.key"
            class="todo-card"
            :class="{ hot: item.count > 0 }"
            type="button"
            @click="section = item.go"
          >
            <span>{{ item.title }}</span>
            <strong>{{ item.count }}</strong>
            <em>{{ item.action }}</em>
          </button>
        </div>

        <div v-if="pendingOrganizers.length || pendingEvents.length" class="inbox">
          <article v-if="pendingOrganizers[0]">
            <h3>待批主办方</h3>
            <p><b>{{ pendingOrganizers[0].organizer?.name }}</b> · {{ pendingOrganizers[0].owner_username || '未知账号' }}</p>
            <div class="inbox-actions">
              <button type="button" :disabled="!!acting" @click="approveOrganizer(pendingOrganizers[0])">通过</button>
              <button class="danger" type="button" :disabled="!!acting" @click="rejectOrganizer(pendingOrganizers[0])">驳回</button>
            </div>
          </article>
          <article v-if="pendingEvents[0]">
            <h3>待审上架</h3>
            <p><b>{{ pendingEvents[0].event?.title }}</b> · {{ pendingEvents[0].organizer_name }}</p>
            <div class="inbox-actions">
              <button type="button" :disabled="!!acting" @click="approveEvent(pendingEvents[0])">上架</button>
              <button class="danger" type="button" :disabled="!!acting" @click="rejectEvent(pendingEvents[0])">驳回</button>
            </div>
          </article>
        </div>

        <header class="section-heading">
          <h2>销售概览</h2>
        </header>
        <div class="sell-board">
          <article class="sell-hero">
            <span>售出金额</span>
            <strong>{{ formatMoney(overview.gross_revenue_cents) }}</strong>
            <i class="trend-chip" :class="revenueTrend.tone">{{ revenueTrend.label }}</i>
          </article>
          <div class="sell-metrics">
            <article>
              <span>已付款订单</span>
              <strong>{{ overview.paid_orders }}<em>笔</em></strong>
            </article>
            <article>
              <span>售出票数</span>
              <strong>{{ overview.paid_tickets }}<em>张</em></strong>
            </article>
            <article :class="{ warn: refundRateValue >= 10 }">
              <span>退款率</span>
              <strong>{{ refundRateLabel }}</strong>
            </article>
          </div>
        </div>

      </section>

      <section v-else-if="section === 'organizers'">
        <header class="heading">
          <div>
            <h1>主办方申请</h1>
          </div>
          <el-select v-model="organizerFilter" style="width: 140px" @change="loadOrganizers">
            <el-option label="待审核" value="pending" />
            <el-option label="已通过" value="approved" />
            <el-option label="已驳回" value="rejected" />
            <el-option label="全部" value="all" />
          </el-select>
        </header>
        <div class="list-card">
          <el-table :data="organizers" empty-text="这一栏没有记录">
            <el-table-column label="主办方" min-width="160">
              <template #default="{ row }">{{ row.organizer?.name }}</template>
            </el-table-column>
            <el-table-column label="标识" min-width="120">
              <template #default="{ row }">{{ row.organizer?.slug }}</template>
            </el-table-column>
            <el-table-column label="申请人" min-width="120">
              <template #default="{ row }">{{ row.owner_username || '—' }}</template>
            </el-table-column>
            <el-table-column label="联系人" min-width="140">
              <template #default="{ row }">{{ row.organizer?.contact_name || '—' }} {{ row.organizer?.contact_phone || '' }}</template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }">{{ auditLabel(row.organizer?.audit_status) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="160" align="right">
              <template #default="{ row }">
                <template v-if="row.organizer?.audit_status === 'pending'">
                  <button class="table-action" type="button" :disabled="!!acting" @click="approveOrganizer(row)">通过</button>
                  <button class="table-action danger" type="button" :disabled="!!acting" @click="rejectOrganizer(row)">驳回</button>
                </template>
                <span v-else-if="row.organizer?.audit_note" class="muted">{{ row.organizer.audit_note }}</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </section>

      <section v-else>
        <header class="heading">
          <div>
            <h1>活动上架</h1>
          </div>
        </header>
        <div class="list-card">
          <el-table :data="pendingEvents" empty-text="没有待审核的上架申请">
            <el-table-column label="活动" min-width="180">
              <template #default="{ row }">{{ row.event?.title }}</template>
            </el-table-column>
            <el-table-column label="主办方" min-width="140">
              <template #default="{ row }">{{ row.organizer_name }}</template>
            </el-table-column>
            <el-table-column label="分类" width="100">
              <template #default="{ row }">{{ row.event?.category }}</template>
            </el-table-column>
            <el-table-column label="卖法" width="90">
              <template #default="{ row }">{{ row.event?.sale_mode === 'seated' ? '选座制' : '门票制' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="160" align="right">
              <template #default="{ row }">
                <button class="table-action" type="button" :disabled="!!acting" @click="approveEvent(row)">上架</button>
                <button class="table-action danger" type="button" :disabled="!!acting" @click="rejectEvent(row)">驳回</button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.admin-shell { min-height: 100vh; background: #f8f4ec; display: grid; grid-template-columns: 180px 1fr; grid-template-rows: 60px 1fr; }
.admin-topbar {
  grid-column: 1 / -1;
  height: 60px;
  padding: 0 28px;
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 16px;
  background: rgba(248,244,236,.96);
}
.brand {
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: 800 26px var(--font-display);
  letter-spacing: .08em;
}
.admin-topbar span { color: var(--muted); }
.topbar-actions { margin-left: auto; display: flex; align-items: center; gap: 16px; }
.store-link, .logout { border: 0; background: transparent; cursor: pointer; font: inherit; }
.store-link { color: var(--ink); }
aside {
  padding: 24px 12px;
  border-right: 1px solid var(--line);
  display: grid;
  align-content: start;
  gap: 8px;
}
aside button {
  height: 44px;
  padding: 0 14px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  font: inherit;
}
aside button.active { background: rgba(181,52,41,.055); color: var(--red); }
aside i {
  min-width: 18px;
  padding: 0 6px;
  border-radius: 99px;
  background: var(--red);
  color: #fff;
  font-style: normal;
  font-size: 12px;
  text-align: center;
}
main { padding: 32px clamp(24px, 4vw, 56px) 70px; }
.heading { display: flex; justify-content: space-between; align-items: end; gap: 16px; }
.heading h1 { margin: 0; font: 760 36px var(--font-display); }
.heading p { margin: 8px 0 0; color: var(--muted); font-size: 13px; max-width: 560px; line-height: 1.7; }
.section-heading { margin-top: 26px; }
.section-heading.first { margin-top: 18px; }
.section-heading h2 { margin: 0; font: 720 21px var(--font-display); }
.section-heading p { margin: 5px 0 0; color: var(--muted); font-size: 12px; }
.todo-grid { margin-top: 12px; display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.todo-card {
  text-align: left;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  padding: 20px 22px;
  background: rgba(255,255,255,.35);
  cursor: pointer;
}
.todo-card.hot { border-color: var(--red); background: rgba(181,52,41,.06); }
.todo-card span, .todo-card small, .todo-card em { display: block; color: var(--muted); font-size: 12px; }
.todo-card strong { display: block; margin: 8px 0 6px; font: 680 40px var(--font-display); }
.todo-card em { color: var(--red); font-style: normal; margin-top: 10px; }
.sell-board { margin-top: 12px; display: grid; gap: 16px; }
.sell-hero {
  padding: 28px 32px 26px;
  border-radius: 24px;
  color: #f4eee4;
  background: linear-gradient(135deg, #3a241f 0%, #1b1512 72%);
}
.sell-hero span { display: block; color: #d2c4b4; font-size: 13px; }
.sell-hero strong {
  display: block;
  margin: 10px 0 12px;
  font: 760 clamp(40px, 5vw, 56px)/1.05 var(--font-display);
}
.sell-hero .trend-chip {
  display: inline-flex;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(244, 238, 228, .12);
  color: #f0d2c4;
  font: 650 12px var(--font-body);
}
.sell-hero .trend-chip.up { background: rgba(239, 109, 88, .22); color: #ef6d58; }
.sell-metrics { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.sell-metrics article {
  padding: 20px 22px;
  border: 1px solid var(--line-strong);
  border-radius: 18px;
  background: rgba(255,255,255,.55);
}
.sell-metrics span { color: var(--muted); font-size: 13px; }
.sell-metrics strong { display: block; margin-top: 8px; font: 750 32px/1.1 var(--font-display); }
.sell-metrics em { margin-left: 4px; color: var(--muted); font: 500 13px var(--font-body); }
.sell-metrics .warn { border-color: rgba(181,52,41,.4); background: #fbf4ee; }
.inbox {
  margin-top: 14px;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.inbox article {
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  padding: 16px 18px;
  background: rgba(255,255,255,.4);
}
.inbox h3 { margin: 0 0 8px; font: 650 13px var(--font-body); color: var(--muted); }
.inbox p { margin: 0 0 6px; }
.inbox-actions { margin-top: 10px; display: flex; gap: 12px; }
.inbox-actions button {
  border: 0;
  background: transparent;
  color: var(--red);
  cursor: pointer;
  font: inherit;
}
.inbox-actions .danger { color: #8b1e16; }
.panel-row { margin-top: 18px; display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.panel {
  margin-top: 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  padding: 18px 20px 20px;
  background: rgba(255,255,255,.28);
}
.panel-row .panel { margin-top: 0; }
.panel h2 { margin: 0 0 14px; font: 720 18px var(--font-display); }
.panel-lead { margin: -8px 0 14px; color: var(--muted); font-size: 12px; }
.sre-grid {
  margin-top: 12px;
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}
@media (min-width: 1100px) {
  .sre-grid { grid-template-columns: repeat(5, 1fr); }
}
.sre-tile {
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  padding: 16px 18px;
  background: rgba(255,255,255,.35);
}
.sre-tile span,
.sre-tile small { display: block; color: var(--muted); font-size: 12px; }
.sre-tile strong { display: block; margin: 8px 0 6px; font: 680 26px var(--font-body); }
.sre-tile.warn { border-color: #c47a3a; }
.sre-tile.alert { border-color: #8b1e16; background: rgba(139,30,22,.06); }
.pipeline-list {
  margin: 12px 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}
.pipeline-list li {
  padding: 14px 16px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: rgba(255,255,255,.4);
}
.pipeline-list li.alert { border-color: rgba(139,30,22,.45); background: rgba(139,30,22,.05); }
.pipeline-list span, .pipeline-list small { display: block; color: var(--muted); font-size: 12px; }
.pipeline-list strong { display: block; margin: 6px 0 4px; font: 680 20px var(--font-body); }
.pipeline-list li.alert strong { color: #8b1e16; }
.lat-row {
  display: grid;
  grid-template-columns: 36px 1fr 72px;
  gap: 10px;
  align-items: center;
  margin-bottom: 12px;
  font-size: 13px;
}
.lat-row b { text-align: right; }
.meter-track { height: 8px; border-radius: 99px; background: rgba(181,52,41,.1); overflow: hidden; }
.meter-track i { display: block; height: 100%; background: var(--red); }
.correct-list { margin: 0; padding: 0; list-style: none; display: grid; gap: 12px; }
.correct-list li { display: flex; justify-content: space-between; gap: 12px; align-items: start; }
.correct-list span { display: block; font-size: 13px; }
.correct-list small { display: block; color: var(--muted); font-size: 12px; margin-top: 2px; }
.correct-list strong { font: 680 18px var(--font-body); }
.correct-list li.alert strong { color: #8b1e16; }
.infrastructure-panel { margin-top: 14px; }
.infrastructure-list { margin: 0; padding: 0; list-style: none; display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px; }
.infrastructure-list li { min-width: 0; padding: 13px 14px; border: 1px solid var(--line); border-radius: var(--radius-md); }
.infrastructure-list li.alert { border-color: rgba(139,30,22,.5); background: rgba(139,30,22,.045); }
.infrastructure-list span, .infrastructure-list small { display: block; color: var(--muted); font-size: 11px; }
.infrastructure-list strong { display: block; margin: 7px 0 5px; font: 680 20px var(--font-body); }
.muted { color: var(--muted); font-size: 12px; }
.list-card {
  margin-top: 22px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: rgba(255,255,255,.28);
}
.table-action { margin-left: 10px; padding: 0; border: 0; background: transparent; color: var(--red); cursor: pointer; font-size: 13px; }
.table-action.danger { color: #8b1e16; }
@media (max-width: 900px) {
  .admin-shell { grid-template-columns: 1fr; }
  aside { display: flex; overflow: auto; border-right: 0; border-bottom: 1px solid var(--line); }
  .todo-grid, .inbox, .panel-row, .sell-metrics { grid-template-columns: 1fr; }
  .pipeline-list { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 560px) {
  .sre-grid, .pipeline-list { grid-template-columns: 1fr; }
}
</style>
