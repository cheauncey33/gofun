<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import api from '../api'

const route = useRoute()
const router = useRouter()
const order = ref(null)
const user = ref(null)
const phase = ref('loading')
const failureReason = ref('')
const confirmOpen = ref(false)
const amountConfirmed = ref(false)
const now = ref(Date.now())
let clockTimer
let queueTimer

const money = cents => `¥${((cents || 0) / 100).toFixed(2)}`
const maskedPhone = computed(() => {
  const value = order.value?.contact_phone || ''
  return /^1\d{10}$/.test(value) ? `${value.slice(0, 3)}****${value.slice(-4)}` : value || '—'
})
const remainingSeconds = computed(() => order.value
  ? Math.max(0, Math.floor((new Date(order.value.expires_at).getTime() - now.value) / 1000))
  : 0)
const countdown = computed(() => {
  const seconds = remainingSeconds.value
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
})

function resolvePhase(data) {
  if (data.status === 'queued') return 'queueing'
  if (data.status === 'paid') return 'success'
  if (data.status === 'pending_payment') return remainingSeconds.value > 0 ? 'cashier' : 'expired'
  failureReason.value = data.cancel_reason || (data.status === 'cancelled' ? '订单已取消' : '订单创建失败')
  return 'failure'
}

async function loadOrder({ preserveUnknown = false } = {}) {
  try {
    const result = await api.getOrderDetail(route.params.id)
    order.value = result.data
    phase.value = resolvePhase(result.data)
    if (phase.value === 'queueing') scheduleQueuePoll()
  } catch (error) {
    if (preserveUnknown) {
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

async function confirmPayment() {
  confirmOpen.value = false
  phase.value = 'paying'
  try {
    await api.payOrder(order.value.id)
    await loadOrder({ preserveUnknown: true })
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

onMounted(async () => {
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  const [, userResult] = await Promise.allSettled([loadOrder(), api.getUserInfo()])
  if (userResult.status === 'fulfilled') user.value = userResult.value.data
})

onBeforeUnmount(() => {
  clearInterval(clockTimer)
  clearTimeout(queueTimer)
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
      <h1>正在读取订单</h1><p>请稍候，不要关闭当前页面。</p>
    </section>

    <section v-else-if="phase === 'queueing'" class="result-state neutral">
      <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
      <small>ORDER CONFIRMATION</small>
      <h1>正在确认票额</h1>
      <p>订单已进入队列，消费者正在核对 MySQL 票额；此时尚未发起支付。</p>
      <button class="outline-button" type="button" @click="loadOrder">立即刷新</button>
    </section>

    <template v-else-if="phase === 'cashier' && order">
      <div class="cashier-grid">
        <section class="payment-panel">
          <header class="countdown">请在 <strong>{{ countdown }}</strong> 内完成支付</header>
          <article class="order-strip">
            <div><small>ORDER</small><h1>{{ order.items?.[0]?.event_title_snapshot }}</h1><p>{{ order.items?.[0]?.tier_name_snapshot }} · {{ order.items?.[0]?.quantity }} 张</p><span>订单号 {{ order.order_no }} · 联系人 {{ maskedPhone }}</span></div>
            <div><span>应付金额</span><strong>{{ money(order.total_amount_cents) }}</strong></div>
          </article>

          <h2>选择支付方式</h2>
          <label class="payment-method selected">
            <input checked type="radio" name="payment" />
            <i>¥</i>
            <span><b>余额支付（演示通道）</b><small>当前为项目演示支付，不代表微信或支付宝已接入。</small></span>
            <em>可用余额<br /><b>{{ money(user?.balance_cents) }}</b></em>
          </label>
          <div class="payment-method disabled" aria-disabled="true">
            <span class="future-icons">微 / 支</span>
            <span><b>微信支付 / 支付宝支付</b><small>尚未接入真实支付机构</small></span>
            <em>不可用</em>
          </div>

          <label class="payment-agreement"><input v-model="amountConfirmed" type="checkbox" />我已确认订单金额与观演人信息</label>
          <footer class="payment-actions">
            <button class="primary-button" type="button" :disabled="!amountConfirmed" @click="confirmOpen = true">确认支付 {{ money(order.total_amount_cents) }}</button>
            <button class="outline-button" type="button" @click="router.push(`/orders/${order.id}`)">返回订单</button>
          </footer>
        </section>
        <aside class="safety-note"><b>支付安全提示</b><p>支付过程中不要重复点击。若页面显示“结果未知”，先查询订单状态，再决定是否重新支付。</p></aside>
      </div>
    </template>

    <section v-else-if="phase === 'paying'" class="result-state neutral">
      <div class="pulse-lines"><i></i><i></i><i></i><i></i><i></i></div>
      <small>PAYMENT PROCESSING</small><h1>支付处理中</h1>
      <p>正在确认支付结果，请勿重复付款或关闭页面。</p>
    </section>

    <section v-else-if="phase === 'success'" class="result-state success">
      <div class="state-mark">✓</div><small>PAYMENT SUCCESS</small><h1>支付成功</h1>
      <p>订单已支付完成，电子票已按购票数量签发。</p>
      <strong>{{ money(order?.total_amount_cents) }}</strong>
      <button class="primary-button" type="button" @click="router.push(`/orders/${order.id}`)">查看电子票</button>
    </section>

    <section v-else-if="phase === 'unknown'" class="result-state warning">
      <div class="state-mark">!</div><small>PAYMENT STATUS UNKNOWN</small><h1>支付结果未知</h1>
      <p>网络中断，暂未确认是否扣款。请先查询结果，不要重复支付。</p>
      <button class="primary-button" type="button" @click="queryPaymentResult">重新查询支付结果</button>
      <button class="text-button" type="button" @click="router.push('/orders')">返回订单列表</button>
    </section>

    <section v-else class="result-state failure">
      <div class="state-mark">×</div><small>PAYMENT NOT COMPLETED</small><h1>{{ phase === 'expired' ? '订单已超时' : '支付失败' }}</h1>
      <p>{{ phase === 'expired' ? '支付时限已结束，请返回订单确认最终取消状态。' : failureReason }}</p>
      <button v-if="order?.status === 'pending_payment' && phase !== 'expired'" class="primary-button" type="button" @click="phase = 'cashier'">返回收银台</button>
      <button class="outline-button" type="button" @click="router.push(order ? `/orders/${order.id}` : '/orders')">返回订单</button>
    </section>

    <div v-if="confirmOpen && order" class="modal-backdrop" @click.self="confirmOpen = false">
      <section class="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
        <button class="close-button" type="button" aria-label="关闭" @click="confirmOpen = false">×</button>
        <h2 id="confirm-title">确认支付</h2>
        <span>应付金额</span><strong>{{ money(order.total_amount_cents) }}</strong>
        <div><small>支付方式</small><b>余额支付（演示通道）</b></div>
        <p>确认后将从账户余额中扣款，请勿重复支付。</p>
        <footer><button class="outline-button" type="button" @click="confirmOpen = false">取消</button><button class="primary-button" type="button" @click="confirmPayment">确认扣款</button></footer>
      </section>
    </div>
  </main>
</template>

<style scoped>
.cashier-page { max-width: 1220px; min-height: 76vh; margin: 0 auto; padding: 34px 4vw 76px; }
.breadcrumb { color: var(--muted); font-size: 12px; }.breadcrumb i { margin: 0 10px; color: var(--red); font-style: normal; }
.steps { margin: 34px 0 38px; padding: 0; display: grid; grid-template-columns: repeat(3, 1fr); list-style: none; }
.steps li { display: flex; align-items: center; gap: 11px; color: #8c847c; }.steps li:not(:last-child)::after { content: ''; height: 1px; flex: 1; margin: 0 18px; background: var(--line-strong); }
.steps b { width: 33px; height: 33px; border: 1px solid currentColor; border-radius: 50%; display: grid; place-content: center; font: 500 18px var(--font-display); }
.steps .active, .steps .done { color: var(--red); }.steps .active b, .steps .done b { background: var(--red); color: white; border-color: var(--red); }
.cashier-grid { display: grid; grid-template-columns: minmax(0, 1fr) 250px; gap: 26px; align-items: start; }
.payment-panel { padding: 32px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); background: rgba(255,255,255,.2); }
.countdown { margin-bottom: 24px; text-align: center; font: 22px var(--font-display); }.countdown strong { margin: 0 6px; color: var(--red); font-size: 32px; }
.order-strip { min-height: 190px; padding: 28px 35px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); display: grid; grid-template-columns: 1fr 230px; position: relative; }
.order-strip::before, .order-strip::after { content: ''; position: absolute; top: 76px; width: 24px; height: 24px; border: 1px solid var(--line-strong); border-radius: 50%; background: var(--paper); }.order-strip::before { left: -13px; }.order-strip::after { right: -13px; }
.order-strip > div:first-child { padding-right: 30px; border-right: 1px dashed var(--line-strong); }.order-strip small { color: var(--red); letter-spacing: .15em; }.order-strip h1 { margin: 12px 0 7px; font: 750 26px var(--font-display); }.order-strip p, .order-strip span { color: var(--muted); font-size: 12px; }.order-strip > div:last-child { display: grid; place-content: center; gap: 8px; text-align: center; }.order-strip > div:last-child strong { color: var(--red); font: 750 38px var(--font-display); }
.payment-panel > h2 { margin: 34px 0 18px; font: 700 22px var(--font-display); }
.payment-method { min-height: 92px; margin-top: 14px; padding: 18px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); display: grid; grid-template-columns: 22px 46px 1fr auto; gap: 14px; align-items: center; }.payment-method.selected { border-color: var(--red); box-shadow: 0 0 0 1px var(--red); }.payment-method.disabled { opacity: .52; }.payment-method input { accent-color: var(--red); }.payment-method > i { width: 38px; height: 38px; border-radius: var(--radius-sm); background: var(--red); color: white; display: grid; place-content: center; font: normal 24px var(--font-display); }.payment-method span { display: grid; gap: 6px; }.payment-method small { color: var(--muted); font-size: 11px; }.payment-method em { color: var(--muted); font-size: 11px; font-style: normal; text-align: right; }.payment-method em b { color: var(--ink); }.future-icons { display: block !important; color: var(--muted); font: 14px var(--font-display); }
.payment-agreement { margin-top: 27px; display: flex; align-items: center; gap: 9px; font-size: 12px; cursor: pointer; }.payment-agreement input { width: 17px; height: 17px; accent-color: var(--red); }
.payment-actions { margin-top: 24px; display: flex; gap: 14px; }
.primary-button, .outline-button { min-width: 150px; height: 48px; padding: 0 22px; border: 1px solid var(--red); border-radius: var(--radius-pill); background: var(--red); color: white; font-weight: 700; cursor: pointer; }.primary-button:disabled { opacity: .48; cursor: not-allowed; }.outline-button { border-color: var(--line-strong); background: transparent; color: var(--ink); }.payment-actions .primary-button { min-width: 270px; }.text-button { margin-top: 18px; border: 0; border-bottom: 1px solid var(--ink); background: transparent; cursor: pointer; }
.safety-note { padding: 20px; border-top: 3px solid var(--red); border-radius: var(--radius-md); background: var(--paper-deep); }.safety-note p { color: var(--muted); font-size: 12px; line-height: 1.75; }
.result-state { min-height: 520px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); display: flex; flex-direction: column; align-items: center; justify-content: center; text-align: center; }.result-state small { color: var(--red); letter-spacing: .18em; }.result-state h1 { margin: 16px 0 7px; font: 750 34px var(--font-display); }.result-state p { max-width: 520px; margin: 0 0 28px; color: var(--muted); line-height: 1.7; }.result-state > strong { margin: -10px 0 24px; color: var(--red); font: 750 30px var(--font-display); }.result-state .outline-button { margin-top: 12px; }
.state-mark { width: 62px; height: 62px; margin-bottom: 20px; border: 1px solid var(--red); border-radius: 50%; display: grid; place-content: center; color: var(--red); font: 32px var(--font-display); }.warning .state-mark { border-color: #c66a16; color: #c66a16; }.failure .state-mark { border-color: var(--red); }
.pulse-lines { height: 48px; display: flex; align-items: center; gap: 10px; }.pulse-lines i { width: 2px; height: 18px; background: var(--ink); animation: pulse 1s ease-in-out infinite; }.pulse-lines i:nth-child(2), .pulse-lines i:nth-child(4) { animation-delay: .15s; }.pulse-lines i:nth-child(3) { height: 36px; background: var(--red); animation-delay: .3s; }@keyframes pulse { 50% { transform: scaleY(1.6); } }
.modal-backdrop { position: fixed; inset: 0; z-index: 1000; background: rgba(20,17,14,.5); display: grid; place-content: center; padding: 20px; }.confirm-dialog { width: min(440px, calc(100vw - 40px)); padding: 28px; border: 1px solid var(--ink); border-radius: var(--radius-lg); background: var(--paper); box-shadow: var(--shadow-lift); position: relative; }.close-button { position: absolute; top: 16px; right: 16px; border: 0; background: transparent; font-size: 24px; cursor: pointer; }.confirm-dialog h2 { margin: 0 0 27px; font: 750 24px var(--font-display); }.confirm-dialog > span { color: var(--muted); font-size: 12px; }.confirm-dialog > strong { margin: 8px 0 25px; display: block; color: var(--red); font: 750 38px var(--font-display); }.confirm-dialog > div { padding: 16px 0; border-block: 1px solid var(--line-strong); display: flex; justify-content: space-between; }.confirm-dialog p { color: var(--muted); font-size: 12px; }.confirm-dialog footer { margin-top: 24px; display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }.confirm-dialog footer button { min-width: 0; }
@media (max-width: 800px) { .cashier-page { padding-inline: 18px; }.cashier-grid { grid-template-columns: 1fr; }.order-strip { grid-template-columns: 1fr; }.order-strip > div:first-child { padding: 0 0 20px; border: 0; border-bottom: 1px dashed var(--line-strong); }.order-strip > div:last-child { padding-top: 20px; }.payment-method { grid-template-columns: 22px 38px 1fr; }.payment-method em { grid-column: 3; text-align: left; }.safety-note { order: -1; } }
@media (max-width: 520px) { .steps span { display: none; }.steps li:not(:last-child)::after { margin: 0 8px; }.payment-panel { padding: 20px 15px; }.payment-actions { flex-direction: column; }.payment-actions .primary-button { min-width: 0; }.result-state { padding: 35px 20px; } }
@media (prefers-reduced-motion: reduce) { .pulse-lines i { animation: none; } }
</style>
