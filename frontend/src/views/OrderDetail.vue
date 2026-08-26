<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'
import AdmissionTicketCard from '../components/AdmissionTicketCard.vue'
import { parseTimeMs, ticketOrderLabel } from '../utils/display.js'

const route = useRoute()
const router = useRouter()
const order = ref(null)
const loading = ref(true)
const actionLoading = ref(false)
let queueTimer

const money = cents => `¥${(cents / 100).toFixed(2)}`
const dateTime = value => {
  const ms = parseTimeMs(value)
  if (!Number.isFinite(ms)) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit',
  }).format(new Date(ms))
}
const attendeeFor = sequence => order.value?.attendees?.find(item => item.sequence_no === sequence)
const hasUsedTicket = () => (order.value?.tickets || []).some(item => item.status === 'used')

async function load() {
  try {
    const res = await api.getOrderDetail(route.params.id)
    order.value = res.data
    if (res.data?.status === 'queued') scheduleQueuePoll()
    else clearTimeout(queueTimer)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '订单不存在')
  } finally {
    loading.value = false
  }
}

function scheduleQueuePoll() {
  clearTimeout(queueTimer)
  queueTimer = setTimeout(() => load(), 1400)
}

function handleOrderStatus(event) {
  if (String(event.detail?.order_id) !== String(route.params.id)) return
  load()
}

async function cancel(reason = '用户主动取消') {
  const paid = order.value?.status === 'paid'
  try {
    await ElMessageBox.confirm(
      paid
        ? '退款后电子票将作废。已入场的订单无法退款。是否继续？'
        : '取消后门票将退回，是否继续？',
      paid ? '申请退款' : '取消订单',
    )
    actionLoading.value = true
    await api.cancelOrder(order.value.id, reason)
    ElMessage.success(paid ? '退款完成' : '订单已取消')
    await load()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '操作失败')
  } finally {
    actionLoading.value = false
  }
}

onMounted(() => {
  window.addEventListener('order:status', handleOrderStatus)
  load()
})
onBeforeUnmount(() => {
  window.removeEventListener('order:status', handleOrderStatus)
  clearTimeout(queueTimer)
})
</script>

<template>
  <div class="order-detail">
    <button class="back" type="button" @click="router.push('/orders')">← 返回订单</button>
    <div v-if="loading" class="detail-state">正在确认订单状态…</div>
    <div v-else-if="!order" class="detail-state">没有找到订单</div>
    <template v-else>
      <header>
        <div>
          <p>订单号 {{ order.order_no }}</p>
          <h1>{{ ticketOrderLabel(order) }}</h1>
          <span v-if="order.status === 'queued'">正在确认座位，完成后即可支付</span>
          <span v-else-if="order.status === 'pending_payment'">请在 {{ dateTime(order.expires_at) }} 前完成支付</span>
          <span v-else-if="order.payment_status === 'refunded'">退款已完成，电子票已作废</span>
          <span v-else-if="order.payment_status === 'refunding'">退款处理中</span>
          <span v-else-if="order.status === 'paid'">电子票已生成</span>
          <span v-else>{{ order.cancel_reason }}</span>
        </div>
        <strong>{{ money(order.total_amount_cents) }}</strong>
      </header>

      <section v-if="order.status === 'queued'" class="queue-banner">
        <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
        <div>
          <strong>正在确认座位</strong>
          <p>请稍候，确认完成后即可支付。</p>
        </div>
        <button type="button" @click="load">刷新</button>
      </section>

      <section v-if="order.items?.[0]" class="ticket">
        <div class="ticket-date"><span>场次</span><strong>{{ dateTime(order.items[0].session_starts_at_snapshot) }}</strong></div>
        <div>
          <small>{{ order.order_source === 'rush_sale' ? '限时开售' : '普通购票' }}</small>
          <h2>{{ order.items[0].event_title_snapshot }}</h2>
          <p>{{ order.items[0].venue_name_snapshot }}</p>
          <p>{{ order.items[0].venue_address_snapshot }}</p>
          <p v-if="order.session_seats?.length">
            座位 {{ order.session_seats.map(item => item.seat?.label).filter(Boolean).join('、') }}
          </p>
        </div>
        <div class="tier"><span>票档</span><strong>{{ order.items[0].tier_name_snapshot }}</strong><small>× {{ order.items[0].quantity }}</small></div>
      </section>
      <section class="purchase-info">
        <div>
          <small>联系人</small>
          <strong>{{ order.contact_name || '—' }}</strong>
          <span>{{ order.contact_phone ? `${order.contact_phone.slice(0, 3)}****${order.contact_phone.slice(-4)}` : '—' }}</span>
        </div>
        <template v-if="order.real_name_required">
          <div v-for="attendee in order.attendees" :key="attendee.id">
            <small>观演人 {{ String(attendee.sequence_no).padStart(2, '0') }}</small>
            <strong>{{ attendee.name }}</strong>
            <span>居民身份证 {{ attendee.id_number_masked }}</span>
          </div>
        </template>
      </section>
      <section v-if="order.tickets?.length" class="electronic-tickets">
        <header class="ticket-section-heading">
          <div>
            <h2>电子票</h2>
            <p>每张票对应一个独立入场凭证。入场时打开二维码即可，请勿截图转发。</p>
          </div>
          <router-link class="tickets-link" to="/orders?tab=tickets">全部电子票 →</router-link>
        </header>
        <div class="electronic-ticket-list">
          <AdmissionTicketCard
            v-for="admissionTicket in order.tickets"
            :key="admissionTicket.id"
            :ticket="admissionTicket"
            :attendee="attendeeFor(admissionTicket.sequence_no)"
          />
        </div>
      </section>
      <div v-if="order.status === 'pending_payment'" class="actions">
        <button type="button" :disabled="actionLoading" @click="cancel()">取消订单</button>
        <button class="primary" type="button" :disabled="actionLoading" @click="router.push(`/cashier/${order.id}`)">前往收银台 {{ money(order.total_amount_cents) }}</button>
      </div>
      <div v-else-if="order.status === 'paid' && !hasUsedTicket()" class="actions">
        <button type="button" :disabled="actionLoading" @click="cancel('用户申请退款')">申请退款</button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.order-detail { max-width: 960px; min-height: 70vh; margin: 0 auto; padding: 45px 28px; }
.back { border: 0; background: transparent; color: var(--muted); cursor: pointer; }
.detail-state { min-height: 50vh; display: grid; place-content: center; color: var(--muted); }
header { margin: 35px 0 25px; padding: 30px 36px; border-radius: var(--radius-lg); background: #1d1814; color: #f7f2ea; display: flex; justify-content: space-between; align-items: end; }
header p { color: #aaa098; font-size: 11px; }
header h1 { margin: 8px 0; font-family: var(--font-display); font-size: 40px; }
header span { color: #c7beb5; font-size: 12px; }
header > strong { color: #ef715c; font-size: 30px; }
.queue-banner { margin-bottom: 22px; padding: 18px 20px; border: 1px dashed var(--line-strong); border-radius: var(--radius-md); display: grid; grid-template-columns: 64px 1fr auto; gap: 16px; align-items: center; }
.queue-banner strong { display: block; font: 700 18px var(--font-display); }
.queue-banner p { margin: 6px 0 0; color: var(--muted); font-size: 13px; }
.queue-banner button { height: 40px; padding: 0 14px; border: 1px solid var(--line-strong); border-radius: var(--radius-pill); background: transparent; cursor: pointer; }
.pulse-lines { display: flex; gap: 4px; align-items: end; height: 36px; }
.pulse-lines i { width: 5px; background: var(--red); animation: pulse 1.1s ease-in-out infinite; }
.pulse-lines i:nth-child(2) { animation-delay: .1s; height: 60%; }
.pulse-lines i:nth-child(3) { animation-delay: .2s; height: 90%; }
.pulse-lines i:nth-child(4) { animation-delay: .3s; height: 50%; }
.pulse-lines i:nth-child(5) { animation-delay: .4s; height: 75%; }
.pulse-lines i:first-child { height: 40%; }
@keyframes pulse { 0%,100% { opacity: .35; transform: scaleY(.7); } 50% { opacity: 1; transform: scaleY(1); } }
.ticket { min-height: 210px; padding: 30px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); box-shadow: var(--shadow-soft); display: grid; grid-template-columns: 170px 1fr 130px; gap: 30px; align-items: center; position: relative; overflow: hidden; }
.ticket::after { content: 'Gofun'; position: absolute; right: -10px; bottom: -35px; color: rgba(181,52,41,.06); font: 800 110px var(--font-display); }
.ticket-date { display: grid; gap: 10px; padding-right: 25px; border-right: 1px dashed var(--line-strong); }
.ticket span, .ticket small { color: var(--muted); font-size: 10px; letter-spacing: .12em; }
.ticket-date strong { font-family: var(--font-display); line-height: 1.6; }
.ticket h2 { margin: 8px 0 18px; font: 700 27px var(--font-display); }
.ticket p { margin: 6px 0; color: var(--muted); font-size: 12px; }
.tier { display: grid; gap: 9px; position: relative; z-index: 1; }
.tier strong { color: var(--red); font-size: 22px; }
.actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 28px; }
.actions button { min-width: 130px; height: 44px; padding: 0 18px; border: 1px solid var(--line-strong); border-radius: var(--radius-pill); background: transparent; cursor: pointer; }
.actions .primary { border-color: var(--red); background: var(--red); color: white; }
.purchase-info { margin-top: 28px; padding: 18px 22px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); display: flex; flex-wrap: wrap; gap: 14px 36px; }
.purchase-info > div { min-width: 210px; display: grid; grid-template-columns: auto auto; gap: 5px 16px; }
.purchase-info small { grid-column: 1 / -1; color: var(--red); font-size: 10px; letter-spacing: .1em; }
.purchase-info strong { font-size: 13px; }.purchase-info span { color: var(--muted); font-size: 11px; }
.electronic-tickets { margin-top: 44px; }
.ticket-section-heading { margin: 0 0 16px; padding: 0; background: transparent; color: var(--ink); align-items: end; }
.ticket-section-heading h2 { margin: 0; font: 720 24px var(--font-display); }
.ticket-section-heading p { margin: 6px 0 0; color: var(--muted); font-size: 12px; }
.ticket-section-heading > span, .tickets-link { color: var(--muted); font-size: 12px; }
.tickets-link { text-decoration: none; }
.electronic-ticket-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
@media (max-width: 780px) {
  .ticket, .electronic-ticket-list, .queue-banner { grid-template-columns: 1fr; }
}
</style>
