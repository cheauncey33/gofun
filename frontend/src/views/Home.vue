<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import api from '../api'
import heroImage from '../assets/fuchang-hero.png'

const router = useRouter()
const loading = ref(true)
const events = ref([])
const rushSales = ref([])
const loadError = ref('')

const featuredEvents = computed(() => events.value.slice(0, 5))

onMounted(async () => {
  try {
    const [eventRes, rushRes] = await Promise.all([
      api.getEvents({ page: 1, page_size: 10 }),
      api.getRushSales(),
    ])
    events.value = eventRes.data?.list || []
    rushSales.value = rushRes.data || []
  } catch (error) {
    loadError.value = error.response?.data?.msg || '暂时无法连接票务服务'
  } finally {
    loading.value = false
  }
})

function minPrice(event) {
  const prices = (event.sessions || []).flatMap(item =>
    (item.ticket_tiers || []).map(tier => tier.price_cents)
  )
  return prices.length ? Math.min(...prices) : null
}

function formatMoney(cents) {
  return cents == null ? '待公布' : `¥${(cents / 100).toFixed(cents % 100 ? 2 : 0)}`
}

function formatDate(event) {
  const date = event.sessions?.[0]?.starts_at
  if (!date) return '日期待定'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit' }).format(new Date(date))
}
</script>

<template>
  <div class="home-page">
    <section class="hero-section">
      <div class="hero-copy">
        <p class="eyebrow">LIVE FOR LOVE · 武汉</p>
        <h1><em>赴</em>热爱之场，<br />见想见的人。</h1>
        <p class="hero-lead">发现值得奔赴的现场</p>
        <div class="trust-row">
          <span><b>票</b> 精选现场<small>严选品牌演出</small></span>
          <span><b>盾</b> 官方票源<small>安心购票无忧</small></span>
          <span><b>心</b> 热爱连接<small>在现场遇见同频</small></span>
        </div>
      </div>
      <div class="hero-visual">
        <img :src="heroImage" alt="暖色灯光下的现场演出观众" />
        <span class="hero-stamp">赴<br />场</span>
      </div>
    </section>

    <section class="content-section">
      <div class="section-heading">
        <div><h2>近期推荐</h2><p>好现场，近在眼前</p></div>
        <span>{{ events.length }} 场活动</span>
      </div>

      <div v-if="loading" class="state-panel">正在收集值得奔赴的现场…</div>
      <div v-else-if="loadError" class="state-panel error">{{ loadError }}</div>
      <div v-else-if="!featuredEvents.length" class="state-panel empty">
        <strong>活动目录还是空的</strong>
        <p>先由平台管理员创建主办方，再由主办方录入场馆、活动、场次和票档并发布。</p>
      </div>
      <div v-else class="event-grid">
        <article
          v-for="(event, index) in featuredEvents"
          :key="event.id"
          class="event-card"
          @click="router.push(`/events/${event.id}`)"
        >
          <div class="event-poster" :class="`poster-${index % 5}`">
            <img v-if="event.cover_url" :src="event.cover_url" :alt="event.title" />
            <span>{{ event.category }}</span>
            <strong>{{ formatDate(event) }}</strong>
          </div>
          <div class="event-info">
            <h3>{{ event.title }}</h3>
            <p>{{ event.sessions?.[0]?.venue?.name || '场馆待公布' }}</p>
            <strong>{{ formatMoney(minPrice(event)) }}<small v-if="minPrice(event)"> 起</small></strong>
          </div>
        </article>
      </div>
    </section>

    <section class="rush-strip">
      <div class="rush-title">
        <i>↯</i>
        <div><h2>限时开售</h2><p>热门场次限量发售中</p></div>
      </div>
      <div v-if="rushSales.length" class="rush-items">
        <button
          v-for="sale in rushSales.slice(0, 3)"
          :key="sale.id"
          type="button"
          @click="router.push('/rush-sales')"
        >
          <span>{{ sale.name }}</span>
          <strong>¥{{ (sale.rush_price_cents / 100).toFixed(0) }}</strong>
          <small>余 {{ sale.remaining_quota }} / {{ sale.total_quota }} 张</small>
        </button>
      </div>
      <p v-else class="rush-empty">下一场开售正在准备中</p>
      <router-link to="/rush-sales">查看全部 →</router-link>
    </section>

    <section class="assurance-row">
      <div><b>◇</b><strong>官方票源</strong><span>与主办方直接连接</span></div>
      <div><b>票</b><strong>价格透明</strong><span>票价与余票公开可见</span></div>
      <div><b>○</b><strong>开售不卡顿</strong><span>热门场次也能稳定下单</span></div>
      <div><b>✓</b><strong>未付自动放票</strong><span>超时未支付，票会退回池中</span></div>
    </section>
  </div>
</template>

<style scoped>
.home-page { padding: 0 3.2vw; }
.hero-section {
  min-height: 350px;
  padding: 38px 3.5vw 28px;
  border-bottom: 1px solid var(--line);
  display: grid;
  grid-template-columns: .86fr 1.34fr;
  gap: 38px;
  align-items: center;
}
.eyebrow { color: var(--red); font-size: 11px; letter-spacing: .18em; }
h1 {
  margin: 10px 0 12px;
  font-family: var(--font-display);
  font-size: clamp(42px, 4.25vw, 68px);
  line-height: 1.13;
  letter-spacing: -.035em;
  font-weight: 780;
}
h1 em { color: var(--red); font-style: normal; }
.hero-lead { color: var(--muted); letter-spacing: .12em; }
.trust-row { display: flex; gap: 28px; margin-top: 42px; }
.trust-row span { display: grid; grid-template-columns: auto 1fr; gap: 1px 8px; font-size: 13px; font-weight: 650; }
.trust-row b { grid-row: 1 / 3; color: var(--red); font-family: var(--font-display); }
.trust-row small { color: var(--muted); font-size: 10px; font-weight: 400; }
.hero-visual { height: 300px; position: relative; overflow: hidden; background: #1b110e; }
.hero-visual::after { content: ''; position: absolute; inset: 0; border: 1px solid rgba(255,255,255,.13); }
.hero-visual img { width: 100%; height: 100%; object-fit: cover; filter: saturate(.9) contrast(1.05); }
.hero-stamp { position: absolute; right: 18px; bottom: 17px; padding: 5px 7px; border: 2px solid #f6e9dc; color: #f6e9dc; line-height: 1; font-family: var(--font-display); }
.content-section { padding: 38px 3.5vw 22px; }
.section-heading { display: flex; justify-content: space-between; align-items: end; margin-bottom: 16px; }
.section-heading > div { display: flex; align-items: baseline; gap: 16px; }
.section-heading h2, .rush-title h2 { margin: 0; font-family: var(--font-display); font-size: 24px; }
.section-heading p, .rush-title p { margin: 0; color: var(--muted); font-size: 12px; }
.section-heading > span { color: var(--muted); font-size: 12px; }
.event-grid { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 13px; }
.event-card { border: 1px solid var(--line-strong); background: rgba(255,255,255,.22); cursor: pointer; transition: transform .2s, box-shadow .2s; }
.event-card:hover { transform: translateY(-4px); box-shadow: 0 14px 28px rgba(43,32,24,.1); }
.event-poster { height: 170px; padding: 12px; display: flex; flex-direction: column; justify-content: space-between; color: white; position: relative; overflow: hidden; }
.event-poster img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
.event-poster::after { content: ''; position: absolute; inset: 0; background: linear-gradient(180deg, rgba(0,0,0,.1), rgba(0,0,0,.58)); }
.event-poster span, .event-poster strong { position: relative; z-index: 1; }
.event-poster span { align-self: flex-start; padding: 3px 6px; background: rgba(18,18,18,.7); font-size: 10px; }
.event-poster strong { font-family: var(--font-display); font-size: 24px; }
.poster-0 { background: linear-gradient(145deg, #1b1b1b 10%, #75402f 58%, #d63f2e); }
.poster-1 { background: linear-gradient(145deg, #18324a, #287b86 64%, #b7d4c5); }
.poster-2 { background: linear-gradient(145deg, #27223b, #315ba5 55%, #e5ad5f); }
.poster-3 { background: linear-gradient(145deg, #1b1512, #674f34 55%, #a92e24); }
.poster-4 { background: linear-gradient(145deg, #294b3f, #67a86c 62%, #f3ca61); }
.event-info { min-height: 112px; padding: 11px; display: flex; flex-direction: column; }
.event-info h3 { margin: 0; font-size: 14px; line-height: 1.45; }
.event-info p { margin: 8px 0 14px; color: var(--muted); font-size: 11px; }
.event-info > strong { margin-top: auto; color: var(--red); }
.event-info small { color: var(--muted); font-weight: 400; }
.state-panel { min-height: 210px; border: 1px dashed var(--line-strong); display: grid; place-content: center; text-align: center; color: var(--muted); }
.state-panel strong { color: var(--ink); font-size: 20px; }
.state-panel p { max-width: 540px; }
.state-panel.error { color: var(--red); }
.rush-strip { margin: 0 3.5vw; min-height: 126px; padding: 18px 20px; border: 1px solid rgba(181,52,41,.62); display: flex; align-items: center; gap: 25px; }
.rush-title { min-width: 190px; display: flex; align-items: center; gap: 13px; }
.rush-title i { color: var(--red); font-size: 30px; }
.rush-items { flex: 1; display: grid; grid-template-columns: repeat(3, 1fr); gap: 9px; }
.rush-items button { min-height: 80px; padding: 10px 12px; border: 0; border-left: 1px solid var(--line); background: transparent; text-align: left; display: grid; cursor: pointer; }
.rush-items strong { color: var(--red); font-size: 20px; }
.rush-items small, .rush-empty { color: var(--muted); }
.rush-strip > a { color: var(--ink); font-size: 12px; text-decoration: none; white-space: nowrap; }
.assurance-row { margin: 18px 0; padding: 24px 5vw; border: 1px solid var(--line); display: grid; grid-template-columns: repeat(4, 1fr); }
.assurance-row div { display: grid; grid-template-columns: auto 1fr; gap: 2px 12px; padding: 0 28px; border-right: 1px solid var(--line); }
.assurance-row div:last-child { border: 0; }
.assurance-row b { grid-row: 1 / 3; color: var(--red); font-size: 23px; }
.assurance-row span { color: var(--muted); font-size: 10px; }
@media (max-width: 1000px) {
  .hero-section { grid-template-columns: 1fr; }
  .event-grid { grid-template-columns: repeat(2, 1fr); }
  .event-card:last-child { display: none; }
  .rush-strip { align-items: flex-start; flex-wrap: wrap; }
  .assurance-row { grid-template-columns: repeat(2, 1fr); gap: 24px; }
}
@media (max-width: 620px) {
  .home-page { padding: 0 16px; }
  .hero-section, .content-section { padding-left: 0; padding-right: 0; }
  .hero-section { padding-top: 25px; }
  .hero-visual { height: 220px; }
  .trust-row { gap: 12px; overflow-x: auto; }
  .event-grid { grid-template-columns: 1fr; }
  .rush-strip { margin: 0; }
  .rush-items { grid-template-columns: 1fr; }
  .assurance-row { grid-template-columns: 1fr; padding: 24px; }
  .assurance-row div { border-right: 0; padding: 0; }
}
</style>
