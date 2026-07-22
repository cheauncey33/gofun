<script setup>
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'

const router = useRouter()
const orders = ref([])
const loading = ref(true)

const statusText = {
  queued: '排队确认中',
  pending_payment: '待支付',
  paid: '已支付',
  cancelled: '已取消',
  failed: '创建失败',
}

onMounted(async () => {
  try {
    const res = await api.getOrders({ page: 1, page_size: 30 })
    orders.value = res.data?.list || []
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '订单加载失败')
  } finally {
    loading.value = false
  }
})

const money = cents => `¥${(cents / 100).toFixed(2)}`
const dateTime = value => new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
}).format(new Date(value))
</script>

<template>
  <div class="orders-page">
    <header><p>MY TICKETS</p><h1>我的订单</h1><span>每一次奔赴，都从一张票开始。</span></header>
    <div v-if="loading" class="order-state">正在加载订单…</div>
    <div v-else-if="!orders.length" class="order-state">
      <strong>还没有票务订单</strong>
      <router-link to="/">去发现活动 →</router-link>
    </div>
    <div v-else class="order-list">
      <article v-for="order in orders" :key="order.id" @click="router.push(`/orders/${order.id}`)">
        <div class="date-block"><strong>{{ dateTime(order.create_time).slice(0, 5) }}</strong><span>{{ dateTime(order.create_time).slice(6) }}</span></div>
        <div class="order-main">
          <small>{{ order.order_no }}</small>
          <h2>{{ order.items?.[0]?.event_title_snapshot }}</h2>
          <p>{{ order.items?.[0]?.tier_name_snapshot }} × {{ order.items?.[0]?.quantity }} · {{ order.items?.[0]?.venue_name_snapshot }}</p>
        </div>
        <div class="order-status"><span :class="order.status">{{ statusText[order.status] || order.status }}</span><strong>{{ money(order.total_amount_cents) }}</strong></div>
        <b>→</b>
      </article>
    </div>
  </div>
</template>

<style scoped>
.orders-page { max-width: 1120px; min-height: 70vh; margin: 0 auto; padding: 55px 30px; }
header { margin-bottom: 38px; }
header p { color: var(--red); font-size: 11px; letter-spacing: .2em; }
header h1 { margin: 6px 0; font-family: var(--font-display); font-size: 44px; }
header span { color: var(--muted); }
.order-state { min-height: 320px; border: 1px dashed var(--line-strong); display: grid; place-content: center; gap: 15px; text-align: center; color: var(--muted); }
.order-state a { color: var(--red); text-decoration: none; }
.order-list { border-top: 1px solid var(--line-strong); }
article { min-height: 128px; padding: 20px 10px; border-bottom: 1px solid var(--line); display: grid; grid-template-columns: 90px 1fr 150px 30px; gap: 22px; align-items: center; cursor: pointer; transition: background .2s; }
article:hover { background: rgba(255,255,255,.32); }
.date-block { border-right: 1px solid var(--line); display: grid; }
.date-block strong { font-family: var(--font-display); font-size: 22px; }
.date-block span, .order-main small, .order-main p { color: var(--muted); font-size: 11px; }
.order-main h2 { margin: 7px 0; font-family: var(--font-display); font-size: 20px; }
.order-main p { margin: 0; }
.order-status { text-align: right; display: grid; gap: 12px; }
.order-status span { color: var(--blue); font-size: 12px; }
.order-status span.failed, .order-status span.cancelled { color: var(--muted); }
.order-status span.pending_payment { color: var(--red); }
.order-status strong { font-size: 20px; }
article > b { color: var(--red); }
@media (max-width: 680px) {
  article { grid-template-columns: 60px 1fr; }
  .order-status { text-align: left; grid-column: 2; }
  article > b { display: none; }
}
</style>
