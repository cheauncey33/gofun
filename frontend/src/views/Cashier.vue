<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  CircleCheck, CircleCheckFilled, CircleCloseFilled, Lock, Ticket, Timer, Wallet, WarningFilled,
} from '@element-plus/icons-vue'
import api from '../api'
import { remainingSecondsUntil } from '../utils/display.js'

const route = useRoute()
const router = useRouter()
const order = ref(null)
const phase = ref('loading')
const failureReason = ref('')
const confirmOpen = ref(false)
const amountConfirmed = ref(false)
const now = ref(Date.now())
let clockTimer
let queueTimer
let paymentTimer

const money = cents => `¥${((cents || 0) / 100).toFixed(2)}`
const maskedPhone = computed(() => {
  const value = order.value?.contact_phone || ''
  return /^1\d{10}$/.test(value) ? `${value.slice(0, 3)}****${value.slice(-4)}` : value || '—'
})
const remainingSeconds = computed(() => remainingSecondsUntil(
  order.value?.expires_at,
  order.value?.expires_at_unix,
  now.value,
  order.value?.id,
) ?? 0)

function remainingOf(data) {
  return remainingSecondsUntil(data?.expires_at, data?.expires_at_unix, Date.now(), data?.id)
}
const countdown = computed(() => {
  const seconds = remainingSeconds.value
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
})

function resolvePhase(data) {
  if (data.status === 'queued') return 'queueing'
  if (data.status === 'paid') return 'success'
  if (data.payment_status === 'failed') {
    failureReason.value = '支付未完成，请重新支付'
    return 'failure'
  }
  if (data.status === 'pending_payment') {
    const left = remainingOf(data)
    if (left == null) return 'cashier'
    return left > 0 ? 'cashier' : 'expired'
  }
  failureReason.value = data.cancel_reason || (data.status === 'cancelled' ? '订单已取消' : '订单创建失败')
  return 'failure'
}

async function loadOrder({ preserveUnknown = false, keepPaying = false } = {}) {
  try {
    const result = await api.getOrderDetail(route.params.id)
    order.value = result.data
    const next = resolvePhase(result.data)
    phase.value = keepPaying && next === 'cashier' ? 'paying' : next
    if (phase.value === 'queueing') scheduleQueuePoll()
  } catch (error) {
    if (preserveUnknown || keepPaying) {
      phase.value = 'unknown'
      return
    }
    phase.value = 'failure'
    failureReason.value = error.response?.data?.msg || '订单信息加载失败'
  }
}

function scheduleQueuePoll() {
  clearTimeout(queueTimer)
  queueTimer = setTimeout(() => loadOrder(), 1400)
}

function schedulePaymentPoll() {
  clearTimeout(paymentTimer)
  paymentTimer = setTimeout(async () => {
    await loadOrder({ preserveUnknown: true, keepPaying: true })
    if (phase.value === 'paying') schedulePaymentPoll()
  }, 800)
}

async function confirmPayment() {
  confirmOpen.value = false
  phase.value = 'paying'
  try {
    await api.payOrder(order.value.id)
    await loadOrder({ preserveUnknown: true, keepPaying: true })
    if (phase.value === 'paying') schedulePaymentPoll()
  } catch (error) {
    const status = error.response?.status
    if (!error.response || status === 408 || status >= 500) {
      phase.value = 'unknown'
      return
    }
    phase.value = 'failure'
    failureReason.value = error.response?.data?.msg || '支付未完成'
  }
}

async function queryPaymentResult() {
  phase.value = 'paying'
  await loadOrder({ preserveUnknown: true })
}

function handleOrderStatus(event) {
  if (String(event.detail?.order_id) !== String(route.params.id)) return
  loadOrder({ preserveUnknown: true, keepPaying: phase.value === 'paying' })
}

onMounted(async () => {
  window.addEventListener('order:status', handleOrderStatus)
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  await loadOrder()
})

onBeforeUnmount(() => {
  window.removeEventListener('order:status', handleOrderStatus)
  clearInterval(clockTimer)
  clearTimeout(queueTimer)
  clearTimeout(paymentTimer)
})
</script>

<template>
  <main class="cashier-page">
    <nav class="breadcrumb">我的订单 <i>/</i> {{ ['cashier', 'queueing'].includes(phase) ? '收银台' : '支付结果' }}</nav>

    <ol v-if="['loading', 'queueing', 'cashier'].includes(phase)" class="steps" aria-label="购票进度">
      <li class="done"><b>1</b><span>填写信息</span></li>
      <li class="done"><b>2</b><span>核对订单</span></li>
      <li class="active"><b>3</b><span>支付</span></li>
    </ol>

    <section v-if="phase === 'loading'" class="result-state neutral">
      <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
      <h1>正在加载订单</h1><p>请稍候，不要关闭当前页面。</p>
    </section>

    <section v-else-if="phase === 'queueing'" class="result-state neutral">
      <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
      <h1>正在确认座位</h1>
      <p>请稍候，确认完成后即可支付。不要关闭页面。</p>
    </section>

    <template v-else-if="phase === 'cashier' && order">
      <div class="cashier-grid">
        <section class="payment-panel">
          <header class="countdown">
            <el-icon class="countdown-icon"><Timer /></el-icon>
            <span>请在</span>
            <strong>{{ countdown }}</strong>
            <span>内完成支付</span>
          </header>
          <article class="order-strip">
            <div class="ticket-art" aria-hidden="true">
              <el-icon><Ticket /></el-icon>
              <span>{{ order.items?.[0]?.quantity || 1 }} 张</span>
            </div>
            <div class="order-copy">
              <small>订单</small>
              <h1>{{ order.items?.[0]?.event_title_snapshot }}</h1>
              <p>{{ order.items?.[0]?.tier_name_snapshot }} · {{ order.items?.[0]?.quantity }} 张</p>
              <span>订单号 {{ order.order_no }} · 联系人 {{ maskedPhone }}</span>
            </div>
            <div class="order-amount">
              <span>应付金额</span>
              <strong>{{ money(order.total_amount_cents) }}</strong>
            </div>
          </article>

          <h2>选择支付方式</h2>
          <label class="payment-method selected">
            <input checked type="radio" name="payment" />
            <span class="brand-badge wallet" aria-hidden="true"><el-icon><Wallet /></el-icon></span>
            <span class="method-copy"><b>在线支付</b><small>确认后完成付款</small></span>
            <em class="ready">可用</em>
          </label>
          <div class="payment-method disabled" aria-disabled="true">
            <span class="method-spacer" aria-hidden="true"></span>
            <span class="brand-badge wechat" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="currentColor"><path d="M9.6 3.8c-3.8 0-6.9 2.7-6.9 6.1 0 1.9 1.1 3.6 2.8 4.7l-.6 2.4 2.7-1.4c.6.2 1.3.3 2 .3 3.8 0 6.9-2.7 6.9-6.1S13.4 3.8 9.6 3.8zm-2.3 5.2a.9.9 0 1 1 0-1.8.9.9 0 0 1 0 1.8zm4.6 0a.9.9 0 1 1 0-1.8.9.9 0 0 1 0 1.8z"/><path d="M17.4 10.6c-.4 0-.8 0-1.2.1-.5 3.2-3.5 5.7-7.1 5.7-.3 0-.7 0-1-.1C8.6 18.4 11 20 14 20c.6 0 1.2-.1 1.7-.2l2.3 1.2-.5-2c1.4-.9 2.3-2.2 2.3-3.8 0-2.5-2-4.6-4.4-4.6z" opacity=".88"/></svg>
            </span>
            <span class="method-copy"><b>微信支付</b><small>即将开通</small></span>
            <em>暂未开通</em>
          </div>
          <div class="payment-method disabled" aria-disabled="true">
            <span class="method-spacer" aria-hidden="true"></span>
            <span class="brand-badge alipay" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="currentColor"><path d="M4 6.5A3.5 3.5 0 0 1 7.5 3h9A3.5 3.5 0 0 1 20 6.5v11a3.5 3.5 0 0 1-3.5 3.5h-9A3.5 3.5 0 0 1 4 17.5v-11zm7.1 2.2h1.8v2.1h2.4v1.5h-2.4V16h-1.8v-4.2H8.8V10.3h2.3V8.7z"/></svg>
            </span>
            <span class="method-copy"><b>支付宝</b><small>即将开通</small></span>
            <em>暂未开通</em>
          </div>

          <label class="payment-agreement"><input v-model="amountConfirmed" type="checkbox" />我已确认订单金额与观演人信息</label>
          <footer class="payment-actions">
            <button class="primary-button" type="button" :disabled="!amountConfirmed" @click="confirmOpen = true">确认支付 {{ money(order.total_amount_cents) }}</button>
            <button class="outline-button" type="button" @click="router.push(`/orders/${order.id}`)">返回订单</button>
          </footer>
        </section>
        <aside class="safety-note">
          <header>
            <el-icon><Lock /></el-icon>
            <b>支付提示</b>
          </header>
          <ul>
            <li>
              <el-icon><CircleCheck /></el-icon>
              <span>支付中请勿重复点击或关闭页面</span>
            </li>
            <li>
              <el-icon><WarningFilled /></el-icon>
              <span>长时间没有结果，先回订单查看状态</span>
            </li>
          </ul>
        </aside>
      </div>
    </template>

    <section v-else-if="phase === 'paying'" class="result-state neutral">
      <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
      <h1>支付处理中</h1>
      <p>正在确认支付结果，请勿重复付款或关闭页面。</p>
    </section>

    <section v-else-if="phase === 'success'" class="result-state success">
      <el-icon class="state-icon ok"><CircleCheckFilled /></el-icon>
      <h1>支付成功</h1>
      <p>电子票已生成，可在订单中查看。</p>
      <strong>{{ money(order?.total_amount_cents) }}</strong>
      <button class="primary-button" type="button" @click="router.push(`/orders/${order.id}`)">查看电子票</button>
    </section>

    <section v-else-if="phase === 'unknown'" class="result-state warning">
      <el-icon class="state-icon warn"><WarningFilled /></el-icon>
      <h1>暂时无法确认结果</h1>
      <p>网络中断，尚未确认是否付款成功。请先查询订单，不要重复支付。</p>
      <button class="primary-button" type="button" @click="queryPaymentResult">重新查询</button>
      <button class="text-button" type="button" @click="router.push('/orders')">返回订单列表</button>
    </section>

    <section v-else class="result-state failure">
      <el-icon class="state-icon bad"><CircleCloseFilled /></el-icon>
      <h1>{{ phase === 'expired' ? '订单已超时' : '支付失败' }}</h1>
      <p>{{ phase === 'expired' ? '支付时间已结束，请返回订单查看状态。' : failureReason }}</p>
      <button v-if="order?.status === 'pending_payment' && phase !== 'expired'" class="primary-button" type="button" @click="phase = 'cashier'">返回收银台</button>
      <button class="outline-button" type="button" @click="router.push(order ? `/orders/${order.id}` : '/orders')">返回订单</button>
    </section>

    <div v-if="confirmOpen && order" class="modal-backdrop" @click.self="confirmOpen = false">
      <section class="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
        <button class="close-button" type="button" aria-label="关闭" @click="confirmOpen = false">×</button>
        <h2 id="confirm-title">确认支付</h2>
        <span>应付金额</span><strong>{{ money(order.total_amount_cents) }}</strong>
        <div class="confirm-method">
          <small>支付方式</small>
          <b>
            <span class="brand-badge wallet tiny" aria-hidden="true"><el-icon><Wallet /></el-icon></span>
            在线支付
          </b>
        </div>
        <p>确认后请勿关闭页面，等待支付完成。</p>
        <footer><button class="outline-button" type="button" @click="confirmOpen = false">取消</button><button class="primary-button" type="button" @click="confirmPayment">确认支付</button></footer>
      </section>
    </div>
  </main>
</template>

<style scoped>
.cashier-page { max-width: 1220px; min-height: 76vh; margin: 0 auto; padding: 34px 4vw 76px; }
.breadcrumb { color: var(--muted); font-size: 12px; }
.breadcrumb i { margin: 0 10px; color: var(--red); font-style: normal; }
.steps { margin: 34px 0 38px; padding: 0; display: grid; grid-template-columns: repeat(3, 1fr); list-style: none; }
.steps li { display: flex; align-items: center; gap: 11px; color: #8c847c; }
.steps li:not(:last-child)::after { content: ''; height: 1px; flex: 1; margin: 0 18px; background: var(--line-strong); }
.steps b { width: 33px; height: 33px; border: 1px solid currentColor; border-radius: 50%; display: grid; place-content: center; font: 500 18px var(--font-display); }
.steps .active, .steps .done { color: var(--red); }
.steps .active b, .steps .done b { background: var(--red); color: white; border-color: var(--red); }
.cashier-grid { display: grid; grid-template-columns: minmax(0, 1fr) 250px; gap: 26px; align-items: start; }
.payment-panel { padding: 32px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); background: rgba(255,255,255,.2); }
.countdown {
  margin-bottom: 24px;
  padding: 12px 18px;
  border-radius: var(--radius-pill);
  background: rgba(181, 52, 41, .08);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  font: 20px var(--font-display);
}
.countdown-icon { color: var(--red); font-size: 22px; }
.countdown strong { color: var(--red); font-size: 32px; letter-spacing: .04em; }
.order-strip {
  min-height: 170px;
  padding: 22px 26px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  display: grid;
  grid-template-columns: 88px 1fr 200px;
  gap: 18px;
  align-items: center;
  position: relative;
  overflow: hidden;
}
.order-strip::before, .order-strip::after {
  content: '';
  position: absolute;
  top: 72px;
  width: 22px;
  height: 22px;
  border: 1px solid var(--line-strong);
  border-radius: 50%;
  background: var(--paper);
}
.order-strip::before { left: -12px; }
.order-strip::after { right: -12px; }
.ticket-art {
  width: 88px;
  height: 108px;
  border-radius: 14px;
  background: linear-gradient(160deg, #b53429, #7a241c);
  color: #fff;
  display: grid;
  place-content: center;
  gap: 8px;
  text-align: center;
  box-shadow: inset 0 0 0 1px rgba(255,255,255,.18);
}
.ticket-art .el-icon { font-size: 34px; }
.ticket-art span { font-size: 12px; letter-spacing: .08em; }
.order-copy { min-width: 0; padding-right: 16px; border-right: 1px dashed var(--line-strong); }
.order-copy small { color: var(--red); letter-spacing: .15em; }
.order-copy h1 { margin: 10px 0 6px; font: 750 24px/1.3 var(--font-display); }
.order-copy p, .order-copy span { color: var(--muted); font-size: 12px; }
.order-amount { display: grid; place-content: center; gap: 8px; text-align: center; }
.order-amount strong { color: var(--red); font: 750 36px var(--font-display); }
.payment-panel > h2 { margin: 34px 0 18px; font: 700 22px var(--font-display); }
.payment-method {
  min-height: 84px;
  margin-top: 12px;
  padding: 16px 18px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  gap: 14px;
  background: rgba(255,255,255,.28);
}
.payment-method.selected { border-color: var(--red); box-shadow: 0 0 0 1px var(--red); }
.payment-method.disabled { opacity: .72; background: transparent; }
.payment-method input, .method-spacer { flex: 0 0 17px; width: 17px; accent-color: var(--red); }
.brand-badge {
  width: 44px;
  height: 44px;
  flex: 0 0 44px;
  border-radius: 12px;
  display: grid;
  place-content: center;
  color: #fff;
}
.brand-badge svg, .brand-badge .el-icon { width: 26px; height: 26px; font-size: 26px; }
.brand-badge.tiny { width: 28px; height: 28px; flex-basis: 28px; border-radius: 8px; }
.brand-badge.tiny .el-icon { width: 16px; height: 16px; font-size: 16px; }
.brand-badge.wallet { background: linear-gradient(145deg, #d34736, #8f2a22); }
.brand-badge.wechat { background: #07c160; }
.brand-badge.alipay { background: #1677ff; }
.method-copy { min-width: 0; flex: 1; display: grid; gap: 4px; }
.method-copy b { white-space: nowrap; font-size: 16px; }
.method-copy small { color: var(--muted); font-size: 12px; }
.payment-method em { flex: 0 0 auto; color: var(--muted); font-size: 12px; font-style: normal; white-space: nowrap; }
.payment-method em.ready { color: #2f9e6d; }
.payment-agreement { margin-top: 24px; display: flex; align-items: center; gap: 9px; font-size: 12px; cursor: pointer; }
.payment-agreement input { width: 17px; height: 17px; accent-color: var(--red); }
.payment-actions { margin-top: 24px; display: flex; gap: 14px; }
.primary-button, .outline-button {
  min-width: 150px;
  height: 48px;
  padding: 0 22px;
  border: 1px solid var(--red);
  border-radius: var(--radius-pill);
  background: var(--red);
  color: white;
  font-weight: 700;
  cursor: pointer;
}
.primary-button:disabled { opacity: .48; cursor: not-allowed; }
.outline-button { border-color: var(--line-strong); background: transparent; color: var(--ink); }
.payment-actions .primary-button { min-width: 270px; }
.text-button { margin-top: 18px; border: 0; border-bottom: 1px solid var(--ink); background: transparent; cursor: pointer; }
.safety-note {
  padding: 20px;
  border-top: 3px solid var(--red);
  border-radius: var(--radius-md);
  background: var(--paper-deep);
}
.safety-note header { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.safety-note header .el-icon { color: var(--red); font-size: 18px; }
.safety-note ul { margin: 0; padding: 0; list-style: none; display: grid; gap: 12px; }
.safety-note li { display: flex; align-items: flex-start; gap: 8px; color: var(--muted); font-size: 12px; line-height: 1.6; }
.safety-note li .el-icon { margin-top: 2px; color: var(--red); font-size: 15px; }
.result-state {
  min-height: 520px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
}
.result-state h1 { margin: 16px 0 7px; font: 750 34px var(--font-display); }
.result-state p { max-width: 520px; margin: 0 0 28px; color: var(--muted); line-height: 1.7; }
.result-state > strong { margin: -10px 0 24px; color: var(--red); font: 750 30px var(--font-display); }
.result-state .outline-button { margin-top: 12px; }
.state-icon { font-size: 64px; margin-bottom: 8px; }
.state-icon.ok { color: #2f9e6d; }
.state-icon.warn { color: #c66a16; }
.state-icon.bad { color: var(--red); }
.pulse-lines { height: 48px; display: flex; align-items: center; gap: 10px; }
.pulse-lines i { width: 2px; height: 18px; background: var(--ink); animation: pulse 1s ease-in-out infinite; }
.pulse-lines i:nth-child(2), .pulse-lines i:nth-child(4) { animation-delay: .15s; }
.pulse-lines i:nth-child(3) { height: 36px; background: var(--red); animation-delay: .3s; }
@keyframes pulse { 50% { transform: scaleY(1.6); } }
.modal-backdrop { position: fixed; inset: 0; z-index: 1000; background: rgba(20,17,14,.5); display: grid; place-content: center; padding: 20px; }
.confirm-dialog {
  width: min(440px, calc(100vw - 40px));
  padding: 28px;
  border: 1px solid var(--ink);
  border-radius: var(--radius-lg);
  background: var(--paper);
  box-shadow: var(--shadow-lift);
  position: relative;
}
.close-button { position: absolute; top: 16px; right: 16px; border: 0; background: transparent; font-size: 24px; cursor: pointer; }
.confirm-dialog h2 { margin: 0 0 27px; font: 750 24px var(--font-display); }
.confirm-dialog > span { color: var(--muted); font-size: 12px; }
.confirm-dialog > strong { margin: 8px 0 25px; display: block; color: var(--red); font: 750 38px var(--font-display); }
.confirm-method { padding: 16px 0; border-block: 1px solid var(--line-strong); display: flex; justify-content: space-between; align-items: center; }
.confirm-method b { display: inline-flex; align-items: center; gap: 8px; }
.confirm-dialog p { color: var(--muted); font-size: 12px; }
.confirm-dialog footer { margin-top: 24px; display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.confirm-dialog footer button { min-width: 0; }
@media (max-width: 800px) {
  .cashier-page { padding-inline: 18px; }
  .cashier-grid { grid-template-columns: 1fr; }
  .order-strip { grid-template-columns: 72px 1fr; }
  .order-copy { border: 0; padding: 0; }
  .order-amount { grid-column: 1 / -1; padding-top: 8px; border-top: 1px dashed var(--line-strong); }
  .safety-note { order: -1; }
}
@media (max-width: 520px) {
  .steps span { display: none; }
  .steps li:not(:last-child)::after { margin: 0 8px; }
  .payment-panel { padding: 20px 15px; }
  .payment-actions { flex-direction: column; }
  .payment-actions .primary-button { min-width: 0; }
  .result-state { padding: 35px 20px; }
}
@media (prefers-reduced-motion: reduce) { .pulse-lines i { animation: none; } }
</style>
