<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'

const router = useRouter()
const loading = ref(true)
const acting = ref('')
const section = ref('overview')
const overview = ref({
  pending_organizers: 0,
  pending_events: 0,
  active_organizers: 0,
  published_events: 0,
  runtime: {},
})
const organizerFilter = ref('pending')
const organizers = ref([])
const pendingOrganizers = ref([])
const pendingEvents = ref([])

const runtime = computed(() => overview.value.runtime || {})
let overviewTimer = 0

function formatNum(value, digits = 1) {
  const n = Number(value || 0)
  if (!Number.isFinite(n)) return '—'
  return Number.isInteger(n) ? String(n) : n.toFixed(digits)
}

function formatMs(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n) || n <= 0) return '—'
  if (n < 1) return `${n.toFixed(1)} ms`
  return `${Math.round(n)} ms`
}

function formatAge(value) {
  const seconds = Number(value || 0)
  if (!seconds) return '—'
  if (seconds < 60) return `${Math.round(seconds)} 秒`
  if (seconds < 3600) return `${Math.round(seconds / 60)} 分钟`
  return `${(seconds / 3600).toFixed(1)} 小时`
}

function formatPct(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n)) return '—'
  return `${n.toFixed(n % 1 ? 1 : 0)}%`
}

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

const sreTiles = computed(() => {
  const r = runtime.value
  const qps = Number(r.http_qps || 0)
  const p95 = Number(r.http_p95_ms || 0)
  const err = Number(r.http_error_rate || 0)
  const backlog = Number(r.outbox_pending || 0) + Number(r.stock_reservation_pending || 0) + Number(r.mq_work_queue_ready || 0)
  return [
    {
      key: 'qps',
      label: '请求流量',
      value: formatNum(qps, 1),
      hint: `近 10 秒 QPS · 在途 ${Math.round(r.http_in_flight || 0)}`,
      tone: qps > 80 ? 'warn' : 'ok',
    },
    {
      key: 'latency',
      label: 'HTTP p95',
      value: formatMs(p95),
      hint: `近 5 分钟 · p99 ${formatMs(r.http_p99_ms)}`,
      tone: p95 > 1000 ? 'alert' : p95 > 500 ? 'warn' : 'ok',
    },
    {
      key: 'error',
      label: '服务端错误率',
      value: formatPct(err),
      hint: `近 5 分钟 · 5xx ${Math.round(r.http_5xx || 0)} / ${Math.round(r.http_requests || 0)}`,
      tone: err > 1 ? 'alert' : Number(r.http_5xx || 0) > 0 ? 'warn' : 'ok',
    },
    {
      key: 'backlog',
      label: '待处理积压',
      value: String(Math.round(backlog)),
      hint: `MQ ${Math.round(r.mq_work_queue_ready || 0)} · Outbox ${Math.round(r.outbox_pending || 0)} · 库存预留 ${Math.round(r.stock_reservation_pending || 0)}`,
      tone: backlog > 100 ? 'alert' : backlog > 0 ? 'warn' : 'ok',
    },
  ]
})

const latencyBars = computed(() => {
  const r = runtime.value
  const items = [
    { label: 'p50', value: Number(r.http_p50_ms || 0) },
    { label: 'p95', value: Number(r.http_p95_ms || 0) },
    { label: 'p99', value: Number(r.http_p99_ms || 0) },
  ]
  const cap = Math.max(...items.map(item => item.value), 50)
  return items.map(item => ({
    ...item,
    pct: Math.min(100, (item.value / cap) * 100),
    text: formatMs(item.value),
  }))
})

const correctness = computed(() => {
  const r = runtime.value
  const mqOk = Number(r.mq_consumed_ok || 0)
  const mqErr = Number(r.mq_consumed_err || 0)
  const mqRetry = Number(r.mq_consumed_retry || 0)
  const mqMalformed = Number(r.mq_consumed_malformed || 0)
  const mqPermanent = Number(r.mq_consumed_permanent || 0)
  const mqDeadLetter = Number(r.mq_consumed_dead_letter || 0)
  const mqFinal = mqOk + mqErr
  const txTotal = Number(r.consumer_tx_ok || 0) + Number(r.consumer_tx_err || 0)
  return [
    {
      label: 'MQ 最终成功率',
      value: mqFinal ? formatPct(100 - Number(r.mq_error_rate || 0)) : '—',
      hint: (mqFinal || mqRetry)
        ? `成功 ${Math.round(mqOk)} · 重试 ${Math.round(mqRetry)} · 永久失败 ${Math.round(mqPermanent)} · 死信 ${Math.round(mqDeadLetter)} · 格式错误 ${Math.round(mqMalformed)} · 本次启动累计`
        : '暂无数据',
      alert: mqErr > 0,
    },
    {
      label: '订单落库成功率',
      value: txTotal ? formatPct(100 - Number(r.consumer_error_rate || 0)) : '—',
      hint: txTotal ? `异常 ${Math.round(r.consumer_tx_err || 0)} · 本次启动累计` : '暂无数据',
      alert: Number(r.consumer_tx_err || 0) > 0,
    },
    {
      label: '下单到待支付 p95',
      value: formatMs(r.order_accept_p95_ms),
      hint: `消费事务 ${formatMs(r.consumer_tx_p95_ms)} · 本次启动累计`,
      alert: Number(r.order_accept_p95_ms || 0) > 5000,
    },
    {
      label: '库存恢复心跳',
      value: formatAge(r.stock_recovery_last_success_ago_seconds),
      hint: Number(r.stock_recovery_last_success_ago_seconds || 0) ? '距最近一次成功扫描' : '尚无成功记录',
      alert: Number(r.stock_recovery_last_success_ago_seconds || 0) > 120,
    },
  ]
})

const infrastructure = computed(() => {
  const r = runtime.value
  return [
    {
      label: 'Outbox 待投递',
      value: Math.round(r.outbox_pending || 0),
      hint: `最老 ${formatAge(r.outbox_oldest_age_seconds)}`,
      alert: Number(r.outbox_pending || 0) > 0,
    },
    {
      label: '库存预留待确认',
      value: Math.round(r.stock_reservation_pending || 0),
      hint: `最老 ${formatAge(r.stock_reservation_oldest_age_seconds)}`,
      alert: Number(r.stock_reservation_pending || 0) > 0,
    },
    {
      label: '订单工作队列',
      value: Math.round(r.mq_work_queue_ready || 0),
      hint: `消费者 ${Math.round(r.mq_work_queue_consumers || 0)}`,
      alert: Number(r.mq_work_queue_ready || 0) > 100,
    },
    {
      label: 'HTTP 数据库连接池',
      value: formatPct(r.db_pool_usage_rate),
      hint: `${Math.round(r.db_connections_in_use || 0)} / ${Math.round(r.db_max_open_connections || 0)} 使用中`,
      alert: Number(r.db_pool_usage_rate || 0) > 80,
    },
    {
      label: '限流保护',
      value: formatPct(r.rate_limit_reject_rate),
      hint: `拦截 ${Math.round(r.rate_limit_rejected || 0)} · 本次启动累计`,
      alert: Number(r.rate_limit_reject_rate || 0) > 20,
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

function logout() {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
  router.push('/login')
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
      <strong class="brand">Gofun</strong>
      <span>平台管理</span>
      <button class="logout" type="button" @click="logout">退出登录</button>
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
            <p>先处理审核事项，再查看近 5 分钟平台健康与票务链路状态</p>
          </div>
          <el-button @click="loadAll">刷新</el-button>
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
            <p class="muted">{{ pendingOrganizers[0].organizer?.description || '未填写简介' }}</p>
            <div class="inbox-actions">
              <button type="button" :disabled="!!acting" @click="approveOrganizer(pendingOrganizers[0])">通过</button>
              <button class="danger" type="button" :disabled="!!acting" @click="rejectOrganizer(pendingOrganizers[0])">驳回</button>
            </div>
          </article>
          <article v-if="pendingEvents[0]">
            <h3>待审上架</h3>
            <p><b>{{ pendingEvents[0].event?.title }}</b> · {{ pendingEvents[0].organizer_name }}</p>
            <p class="muted">{{ pendingEvents[0].event?.category }} · {{ pendingEvents[0].event?.sale_mode === 'seated' ? '选座' : '计数' }}</p>
            <div class="inbox-actions">
              <button type="button" :disabled="!!acting" @click="approveEvent(pendingEvents[0])">上架</button>
              <button class="danger" type="button" :disabled="!!acting" @click="rejectEvent(pendingEvents[0])">驳回</button>
            </div>
          </article>
        </div>

        <header class="section-heading">
          <div><h2>平台健康</h2><p>HTTP 使用近 5 分钟窗口；QPS 使用近 10 秒窗口</p></div>
        </header>
        <section class="sre-grid" aria-label="平台健康">
          <article
            v-for="tile in sreTiles"
            :key="tile.key"
            class="sre-tile"
            :class="tile.tone"
          >
            <span>{{ tile.label }}</span>
            <strong>{{ tile.value }}</strong>
            <small>{{ tile.hint }}</small>
          </article>
        </section>

        <div class="panel-row">
          <section class="panel">
            <h2>HTTP 延迟分布</h2>
            <p class="panel-lead">近 5 分钟请求，按固定耗时桶估算</p>
            <div v-for="bar in latencyBars" :key="bar.label" class="lat-row">
              <span>{{ bar.label }}</span>
              <div class="meter-track"><i :style="{ width: `${bar.pct}%` }"></i></div>
              <b>{{ bar.text }}</b>
            </div>
          </section>
          <section class="panel">
            <h2>票务链路</h2>
            <p class="panel-lead">异步落库、消费与库存恢复</p>
            <ul class="correct-list">
              <li v-for="row in correctness" :key="row.label" :class="{ alert: row.alert }">
                <div>
                  <span>{{ row.label }}</span>
                  <small>{{ row.hint }}</small>
                </div>
                <strong>{{ row.value }}</strong>
              </li>
            </ul>
          </section>
        </div>

        <section class="panel infrastructure-panel">
          <h2>依赖与积压</h2>
          <p class="panel-lead">数量是当前值，年龄用于判断是否持续卡住</p>
          <ul class="infrastructure-list">
            <li v-for="row in infrastructure" :key="row.label" :class="{ alert: row.alert }">
              <span>{{ row.label }}</span>
              <strong>{{ row.value }}</strong>
              <small>{{ row.hint }}</small>
            </li>
          </ul>
        </section>
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
              <template #default="{ row }">{{ row.event?.sale_mode === 'seated' ? '选座' : '计数' }}</template>
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
.brand { font: 800 26px var(--font-display); letter-spacing: .08em; }
.admin-topbar span { color: var(--muted); }
.logout { margin-left: auto; border: 0; background: transparent; cursor: pointer; font: inherit; }
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
.section-heading h2 { margin: 0; font: 720 21px var(--font-display); }
.section-heading p { margin: 5px 0 0; color: var(--muted); font-size: 12px; }
.todo-grid { margin-top: 22px; display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
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
  margin-top: 18px;
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}
@media (min-width: 1100px) {
  .sre-grid { grid-template-columns: repeat(4, 1fr); }
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
  .todo-grid, .inbox, .panel-row { grid-template-columns: 1fr; }
  .infrastructure-list { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 560px) {
  .sre-grid, .infrastructure-list { grid-template-columns: 1fr; }
}
</style>
