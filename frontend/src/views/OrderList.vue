<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'
import { formatClock, ticketOrderLabel, ticketOrderStatusClass, waitlistStatusText } from '../utils/display.js'
import AdmissionTicketCard from '../components/AdmissionTicketCard.vue'

const route = useRoute()
const router = useRouter()
const orders = ref([])
const tickets = ref([])
const loading = ref(true)
const ticketsLoading = ref(false)
const ticketsLoaded = ref(false)
const loadError = ref('')
const searchDraft = ref(String(route.query.q || ''))
let searchTimer = 0

const statusChips = [
  { id: 'all', label: '全部' },
  { id: 'pending', label: '待支付' },
  { id: 'paid', label: '已支付' },
  { id: 'cancelled', label: '已取消' },
  { id: 'refunded', label: '已退款' },
]

const waitlists = ref([])
const waitlistsLoading = ref(false)
const waitlistsLoaded = ref(false)

const tab = computed(() => {
  if (route.query.tab === 'tickets') return 'tickets'
  if (route.query.tab === 'waitlist') return 'waitlist'
  return 'orders'
})
const statusFilter = computed(() => {
  const value = String(route.query.status || '')
  return ['pending', 'paid', 'cancelled', 'refunded'].includes(value) ? value : 'all'
})
const keyword = computed(() => String(route.query.q || '').trim())
const hasOrderFilters = computed(() => statusFilter.value !== 'all' || Boolean(keyword.value))

onMounted(async () => {
  window.addEventListener('order:status', handleOrderStatus)
  await loadOrders()
  if (tab.value === 'tickets') await loadTickets()
  if (tab.value === 'waitlist') await loadWaitlists()
})

onBeforeUnmount(() => {
  window.removeEventListener('order:status', handleOrderStatus)
  window.clearTimeout(searchTimer)
})

watch(tab, async (value) => {
  if (value === 'tickets' && !ticketsLoaded.value) await loadTickets()
  if (value === 'waitlist' && !waitlistsLoaded.value) await loadWaitlists()
})

watch([statusFilter, keyword], () => {
  if (tab.value === 'orders') loadOrders()
})

watch(() => route.query.q, (value) => {
  const next = String(value || '')
  if (searchDraft.value !== next) searchDraft.value = next
})

watch(searchDraft, (value) => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    const next = value.trim()
    if (next === keyword.value) return
    applyQuery({ q: next || undefined })
  }, 280)
})

function handleOrderStatus() {
  loadOrders()
  if (tab.value === 'tickets' || ticketsLoaded.value) loadTickets()
  if (tab.value === 'waitlist' || waitlistsLoaded.value) loadWaitlists()
}

function applyQuery(patch) {
  const query = { ...route.query }
  for (const [key, value] of Object.entries(patch)) {
    if (value == null || value === '' || value === 'all') delete query[key]
    else query[key] = value
  }
  router.replace({ path: '/orders', query })
}

function selectTab(next) {
  applyQuery({ tab: next === 'orders' ? undefined : next })
}

function selectStatus(next) {
  applyQuery({ status: next })
}

function clearOrderFilters() {
  searchDraft.value = ''
  applyQuery({ status: undefined, q: undefined })
}

async function loadOrders() {
  loading.value = true
  loadError.value = ''
  try {
    const params = { page: 1, page_size: 30 }
    if (statusFilter.value !== 'all') params.status = statusFilter.value
    if (keyword.value) params.q = keyword.value
    const res = await api.getOrders(params)
    orders.value = Array.isArray(res.data?.list) ? res.data.list : []
  } catch (error) {
    loadError.value = error.response?.data?.msg || '订单加载失败'
    ElMessage.error(loadError.value)
  } finally {
    loading.value = false
  }
}

async function loadTickets() {
  ticketsLoading.value = true
  try {
    const res = await api.getMyTickets({ page: 1, page_size: 50 })
    tickets.value = res.data?.list || []
    ticketsLoaded.value = true
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '电子票加载失败')
  } finally {
    ticketsLoading.value = false
  }
}

async function loadWaitlists() {
  waitlistsLoading.value = true
  try {
    const res = await api.getWaitlists({ page: 1, page_size: 30 })
    waitlists.value = Array.isArray(res.data?.list) ? res.data.list : []
    waitlistsLoaded.value = true
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '候补加载失败')
  } finally {
    waitlistsLoading.value = false
  }
}

function waitlistHref(item) {
  if (item.status === 'fulfilled' && item.fulfilled_order_id) return `/orders/${item.fulfilled_order_id}`
  return `/waitlist/${item.id}`
}

const money = cents => `¥${(cents / 100).toFixed(2)}`

function orderClock(order) {
  return formatClock(order.create_time || order.CreateTime || order.update_time || order.UpdateTime)
}
</script>

<template>
  <div class="orders-page">
    <header>
      <h1>我的订单</h1>
      <nav class="order-tabs" aria-label="订单与电子票">
        <button type="button" :class="{ active: tab === 'orders' }" @click="selectTab('orders')">订单</button>
        <button type="button" :class="{ active: tab === 'waitlist' }" @click="selectTab('waitlist')">候补</button>
        <button type="button" :class="{ active: tab === 'tickets' }" @click="selectTab('tickets')">电子票</button>
      </nav>
    </header>

    <template v-if="tab === 'orders'">
      <div class="order-toolbar">
        <form class="order-search" @submit.prevent>
          <input
            v-model="searchDraft"
            type="search"
            placeholder="搜索活动、场馆或订单号"
            aria-label="搜索订单"
          >
        </form>
        <nav class="status-chips" aria-label="订单状态">
          <button
            v-for="chip in statusChips"
            :key="chip.id"
            type="button"
            :class="{ active: statusFilter === chip.id }"
            @click="selectStatus(chip.id)"
          >{{ chip.label }}</button>
        </nav>
      </div>
      <div v-if="loading && !orders.length" class="order-state">正在加载订单…</div>
      <div v-else-if="loadError" class="order-state">
        <strong>{{ loadError }}</strong>
        <button type="button" class="retry" @click="loadOrders">重新加载</button>
      </div>
      <div v-else-if="!orders.length" class="order-state">
        <template v-if="hasOrderFilters">
          <strong>没有符合条件的订单</strong>
          <button type="button" class="retry" @click="clearOrderFilters">清除筛选</button>
        </template>
        <template v-else>
          <strong>还没有订单</strong>
          <router-link to="/">去发现活动 →</router-link>
        </template>
      </div>
      <div v-else class="order-list">
        <article v-for="order in orders" :key="order.id" @click="router.push(`/orders/${order.id}`)">
          <time>{{ orderClock(order) }}</time>
          <div class="order-main">
            <h2>{{ order.items?.[0]?.event_title_snapshot }}</h2>
            <p>{{ order.items?.[0]?.tier_name_snapshot }} × {{ order.items?.[0]?.quantity }} · {{ order.items?.[0]?.venue_name_snapshot }}</p>
          </div>
          <div class="order-status">
            <span :class="ticketOrderStatusClass(order)">{{ ticketOrderLabel(order) }}</span>
            <strong>{{ money(order.total_amount_cents) }}</strong>
          </div>
        </article>
      </div>
    </template>

    <template v-else-if="tab === 'waitlist'">
      <div v-if="waitlistsLoading && !waitlistsLoaded" class="order-state">正在加载候补…</div>
      <div v-else-if="!waitlists.length" class="order-state">
        <strong>还没有候补</strong>
      </div>
      <div v-else class="order-list">
        <article v-for="item in waitlists" :key="item.id" @click="router.push(waitlistHref(item))">
          <time>{{ orderClock(item) }}</time>
          <div class="order-main">
            <h2>{{ item.event_title_snapshot }}</h2>
            <p>
              {{ item.tier_name_snapshot }} × {{ item.quantity }}
              <template v-if="item.queue_position"> · 第 {{ item.queue_position }} 位</template>
            </p>
          </div>
          <div class="order-status">
            <span :class="item.status">{{ waitlistStatusText[item.status] || item.status }}</span>
            <strong>{{ money(item.amount_cents) }}</strong>
          </div>
        </article>
      </div>
    </template>

    <template v-else>
      <div v-if="ticketsLoading && !ticketsLoaded" class="order-state">正在加载电子票…</div>
      <div v-else-if="!tickets.length" class="order-state">
        <strong>还没有电子票</strong>
        <router-link to="/">支付成功后会出现在这里</router-link>
      </div>
      <div v-else class="ticket-grid">
        <router-link
          v-for="ticket in tickets"
          :key="ticket.id"
          class="ticket-link"
          :to="`/orders/${ticket.order_id}`"
        >
          <AdmissionTicketCard :ticket="ticket" :attendee="ticket.attendee" />
        </router-link>
      </div>
    </template>
  </div>
</template>

<style scoped>
.orders-page { padding: 20px 3.2vw 8px; }
header {
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
header h1 { margin: 0; font-family: var(--font-display); font-size: 26px; }
.order-tabs { display: flex; gap: 6px; }
.order-tabs button, .status-chips button {
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--ink);
  font: 650 13px inherit;
  cursor: pointer;
}
.order-tabs button.active, .status-chips button.active {
  border-color: var(--red);
  background: var(--red);
  color: #fff;
}
.order-toolbar {
  margin: 4px 0 10px;
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.order-search {
  flex: 1 1 220px;
  max-width: 360px;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  display: flex;
  align-items: center;
}
.order-search input {
  width: 100%;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--ink);
  font: inherit;
}
.status-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.order-state {
  padding: 28px 16px;
  color: var(--muted);
}
.order-state a, .order-state .retry { color: var(--red); text-decoration: none; border: 0; background: transparent; cursor: pointer; font: inherit; }
.order-list { border-top: 1px solid var(--line); }
article {
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 20px;
  cursor: pointer;
}
article:hover { background: rgba(255,255,255,.28); }
article time {
  flex: 0 0 108px;
  color: var(--muted);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.order-main { min-width: 0; flex: 0 1 520px; }
.order-main h2 { margin: 0 0 3px; font-size: 16px; font-weight: 700; }
.order-main p { margin: 0; color: var(--muted); font-size: 12px; }
.order-status {
  display: flex;
  align-items: baseline;
  gap: 14px;
  white-space: nowrap;
}
.order-status span { color: var(--blue); font-size: 12px; }
.order-status span.failed, .order-status span.cancelled, .order-status span.refunded { color: var(--muted); }
.order-status span.pending_payment, .order-status span.queued, .order-status span.refunding { color: var(--red); }
.order-status strong { font-size: 16px; }
.ticket-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 420px));
  gap: 12px;
  justify-content: start;
}
.ticket-link { color: inherit; text-decoration: none; }
@media (max-width: 780px) {
  .orders-page { padding: 16px 18px 8px; }
  header { align-items: flex-start; flex-direction: column; gap: 10px; }
  .order-search { max-width: none; }
  article { flex-wrap: wrap; gap: 6px 12px; }
  article time { flex-basis: 100%; }
  .order-status { margin-left: 0; }
}
</style>
