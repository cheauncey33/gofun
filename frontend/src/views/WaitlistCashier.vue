<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CircleCheckFilled, Ticket, Timer, Wallet } from '@element-plus/icons-vue'
import api from '../api'
import { remainingSecondsUntil, waitlistStatusText } from '../utils/display.js'

const route = useRoute()
const router = useRouter()
const entry = ref(null)
const phase = ref('loading')
const failureReason = ref('')
const confirmOpen = ref(false)
const now = ref(Date.now())
let clockTimer
let pollTimer

const money = cents => `¥${((cents || 0) / 100).toFixed(2)}`
const remainingSeconds = computed(() => remainingSecondsUntil(
  entry.value?.expires_at,
  entry.value?.expires_at_unix,
  now.value,
  entry.value?.id,
) ?? 0)
const countdown = computed(() => {
  const seconds = remainingSeconds.value
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
})

function resolvePhase(data) {
  if (data.status === 'pending_payment') {
    const left = remainingSecondsUntil(data.expires_at, data.expires_at_unix, Date.now(), data.id)
    if (left == null) return 'cashier'
    return left > 0 ? 'cashier' : 'expired'
  }
  if (data.status === 'queued') return 'queued'
  if (data.status === 'fulfilled') return 'fulfilled'
  failureReason.value = data.cancel_reason || waitlistStatusText[data.status] || '候补已结束'
  return 'failure'
}

async function loadEntry({ keepPaying = false } = {}) {
  try {
    const result = await api.getWaitlistDetail(route.params.id)
    entry.value = result.data
    const next = resolvePhase(result.data)
    phase.value = keepPaying && next === 'cashier' ? 'paying' : next
    if (phase.value === 'queued' || phase.value === 'paying') schedulePoll()
    if (phase.value === 'fulfilled' && result.data.fulfilled_order_id) {
      router.replace(`/orders/${result.data.fulfilled_order_id}`)
    }
  } catch (error) {
    phase.value = 'failure'
    failureReason.value = error.response?.data?.msg || '候补信息加载失败'
  }
}

function schedulePoll() {
  clearTimeout(pollTimer)
  pollTimer = setTimeout(async () => {
    await loadEntry({ keepPaying: phase.value === 'paying' })
  }, 1200)
}

async function confirmPayment() {
  confirmOpen.value = false
  phase.value = 'paying'
  try {
    await api.payWaitlist(entry.value.id)
    await loadEntry({ keepPaying: true })
  } catch (error) {
    phase.value = 'failure'
    failureReason.value = error.response?.data?.msg || '支付未完成'
  }
}

onMounted(async () => {
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  await loadEntry()
})

onBeforeUnmount(() => {
  clearInterval(clockTimer)
  clearTimeout(pollTimer)
})
</script>

<template>
  <main class="cashier-page">
    <nav class="breadcrumb">我的候补 <i>/</i> {{ phase === 'cashier' ? '支付' : '候补状态' }}</nav>

    <section v-if="phase === 'loading'" class="result-state">
      <h1>正在加载候补单</h1>
    </section>

    <template v-else-if="phase === 'cashier' && entry">
      <header class="countdown">
        <el-icon><Timer /></el-icon>
        <span>请在</span>
        <strong>{{ countdown }}</strong>
        <span>内完成支付，付款后进入候补队列</span>
      </header>
      <article class="order-strip">
        <el-icon><Ticket /></el-icon>
        <div>
          <h1>{{ entry.event_title_snapshot }}</h1>
          <p>{{ entry.tier_name_snapshot }} · {{ entry.quantity }} 张 · 候补 {{ entry.waitlist_no }}</p>
        </div>
        <strong>{{ money(entry.amount_cents) }}</strong>
      </article>
      <label class="payment-method">
        <el-icon><Wallet /></el-icon>
        <span>在线支付（沙箱）</span>
      </label>
      <button class="primary-action" type="button" @click="confirmOpen = true">确认支付</button>
      <el-dialog v-model="confirmOpen" title="确认候补付款" width="400px">
        <p>确认支付 {{ money(entry.amount_cents) }}？</p>
        <template #footer>
          <el-button @click="confirmOpen = false">取消</el-button>
          <el-button type="primary" @click="confirmPayment">确认支付 {{ money(entry.amount_cents) }}</el-button>
        </template>
      </el-dialog>
    </template>

    <section v-else-if="phase === 'paying'" class="result-state">
      <h1>正在确认付款</h1>
      <p>请稍候，不要关闭页面。</p>
    </section>

    <section v-else-if="phase === 'queued' && entry" class="result-state success">
      <el-icon class="ok"><CircleCheckFilled /></el-icon>
      <h1>已进入候补队列</h1>
      <p>
        当前第 {{ entry.queue_position || '—' }} 位。有人退票时按顺序派给你，无需刷新抢票。
      </p>
      <p>{{ entry.tier_name_snapshot }} · {{ entry.quantity }} 张 · {{ money(entry.amount_cents) }}</p>
      <button class="text-link" type="button" @click="router.push('/orders?tab=waitlist')">查看我的候补</button>
    </section>

    <section v-else-if="phase === 'expired'" class="result-state">
      <h1>候补支付已超时</h1>
      <button class="text-link" type="button" @click="router.push('/orders?tab=waitlist')">返回候补列表</button>
    </section>

    <section v-else class="result-state">
      <h1>{{ failureReason || '候补已结束' }}</h1>
      <button class="text-link" type="button" @click="router.push('/orders?tab=waitlist')">返回候补列表</button>
    </section>
  </main>
</template>

<style scoped>
.cashier-page { max-width: 720px; margin: 0 auto; padding: 32px 4vw 80px; }
.breadcrumb { color: var(--muted); font-size: 12px; margin-bottom: 24px; }
.breadcrumb i { margin: 0 8px; }
.countdown { display: flex; align-items: center; gap: 8px; margin-bottom: 22px; }
.countdown strong { color: var(--red); font-size: 28px; }
.order-strip, .payment-method {
  display: flex; align-items: center; gap: 16px;
  padding: 18px; border: 1px solid var(--line-strong); border-radius: var(--radius-md);
  margin-bottom: 16px;
}
.order-strip h1 { margin: 0 0 6px; font-size: 18px; }
.order-strip p { margin: 0; color: var(--muted); font-size: 13px; }
.order-strip strong { margin-left: auto; color: var(--red); font-size: 22px; }
.primary-action {
  width: 100%; height: 48px; border: 0; border-radius: var(--radius-pill);
  background: var(--red); color: #fff; font-weight: 750; cursor: pointer;
}
.result-state { min-height: 40vh; display: grid; place-content: center; text-align: center; gap: 12px; }
.result-state.success .ok { font-size: 48px; color: var(--red); }
.text-link { border: 0; background: transparent; color: var(--red); cursor: pointer; }
</style>
