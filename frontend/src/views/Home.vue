<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import api from '../api'
import { useDiscovery } from '../stores/discovery'
import FeaturedCarousel from '../components/home/FeaturedCarousel.vue'

const router = useRouter()
const loading = ref(true)
const loadingMore = ref(false)
const events = ref([])
const eventTotal = ref(0)
const eventPage = ref(1)
const pageSize = 12
const rushSales = ref([])
const featuredEvents = ref([])
const loadError = ref('')
const featuredLimit = 8
const featuredRushPriority = 3
const {
  state,
  hasActiveFilters,
  toggleCategory,
  clearFilters,
  eventQuery,
  applyMeta,
} = useDiscovery()

const canLoadMore = computed(() => events.value.length < eventTotal.value)
const filterSummary = computed(() => {
  const parts = []
  if (state.city) parts.push(state.city)
  if (state.selectedCategory) parts.push(state.selectedCategory)
  if (state.keyword) parts.push(`“${state.keyword}”`)
  return parts.join(' · ')
})

function rushSlide(sale) {
  const event = featuredEvents.value.find(item => String(item.id) === String(sale.event_id))
    || events.value.find(item => String(item.id) === String(sale.event_id))
  return {
    kind: 'rush',
    id: `rush-${sale.id}`,
    title: sale.name,
    subtitle: sale.event_title || '限时票档',
    price: sale.rush_price_cents,
    badge: '限时开售',
    time: formatRushTime(sale.starts_at),
    cover: sale.cover_url || event?.cover_url || '',
    eventId: sale.event_id,
    sale,
  }
}

function eventSlide(event) {
  return {
    kind: 'event',
    id: `event-${event.id}`,
    title: event.title,
    subtitle: [eventCity(event), event.sessions?.[0]?.venue?.name].filter(Boolean).join(' · ') || event.subtitle,
    price: minPrice(event),
    badge: event.category || '在售',
    time: formatDate(event),
    cover: event.cover_url || '',
    eventId: event.id,
  }
}

const featuredSlides = computed(() => {
  const slides = []
  const seen = new Set()
  const rushes = rushSales.value
  for (const sale of rushes.slice(0, featuredRushPriority)) {
    slides.push(rushSlide(sale))
    if (sale.event_id) seen.add(String(sale.event_id))
  }
  const catalog = featuredEvents.value.length ? featuredEvents.value : events.value
  for (const event of catalog) {
    if (slides.length >= featuredLimit) break
    if (seen.has(String(event.id))) continue
    slides.push(eventSlide(event))
    seen.add(String(event.id))
  }
  for (const sale of rushes.slice(featuredRushPriority)) {
    if (slides.length >= featuredLimit) break
    slides.push(rushSlide(sale))
    if (sale.event_id) seen.add(String(sale.event_id))
  }
  return slides
})

async function loadCatalog({ append = false } = {}) {
  if (append) loadingMore.value = true
  else {
    loading.value = true
    eventPage.value = 1
  }
  loadError.value = ''
  try {
    const page = append ? eventPage.value + 1 : 1
    const tasks = [
      api.getEvents(eventQuery({ page, page_size: pageSize })),
    ]
    if (!append) {
      tasks.push(api.getRushSales())
      tasks.push(api.getCatalogMeta().catch(() => null))
      const featuredQuery = { page: 1, page_size: featuredLimit }
      if (state.city) featuredQuery.city = state.city
      tasks.push(api.getEvents(featuredQuery).catch(() => null))
    }
    const [eventRes, rushRes, metaRes, featuredRes] = await Promise.all(tasks)
    const list = eventRes.data?.list || []
    eventTotal.value = eventRes.data?.total ?? (append ? eventTotal.value : list.length)
    events.value = append ? [...events.value, ...list] : list
    eventPage.value = page
    if (!append) {
      rushSales.value = rushRes?.data || []
      if (metaRes?.data) applyMeta(metaRes.data)
      featuredEvents.value = featuredRes?.data?.list || list.slice(0, featuredLimit)
    }
    api.trackFunnelVisits('browse', [
      ...list.map(item => item.id),
      ...(!append ? featuredEvents.value.map(item => item.id) : []),
      ...(!append ? rushSales.value.map(item => item.event_id) : []),
    ])
  } catch (error) {
    loadError.value = error.response?.data?.msg || '暂时无法连接票务服务'
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function loadMore() {
  if (!canLoadMore.value || loadingMore.value) return
  loadCatalog({ append: true })
}

function openRush(sale) {
  if (sale?.event_id) router.push(`/events/${sale.event_id}`)
  else router.push('/rush-sales')
}

function openSlide(slide) {
  if (slide.kind === 'rush') openRush(slide.sale || slide)
  else if (slide.eventId) router.push(`/events/${slide.eventId}`)
}

onMounted(() => {
  loadCatalog()
})

watch(
  () => [state.city, state.keyword, state.selectedCategory],
  () => {
    loadCatalog()
  },
)

function minPrice(event) {
  const prices = (event.sessions || []).flatMap(item =>
    (item.ticket_tiers || []).map(tier => tier.price_cents),
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

function formatRushTime(value) {
  if (!value) return '开售时间待定'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function eventCity(event) {
  return event.sessions?.[0]?.venue?.city || ''
}

function onCoverError(event) {
  event.target.style.display = 'none'
}
</script>

<template>
  <div class="home-page">
    <div id="home-listings" class="listings">
      <FeaturedCarousel :slides="featuredSlides" @select="openSlide" />

      <section class="listing-block rush-block">
        <header class="block-heading">
          <div>
            <p>RUSH</p>
            <h2>限时开售</h2>
            <span>热门场次优先，限量发售</span>
          </div>
          <router-link to="/rush-sales">全部开售 →</router-link>
        </header>

        <div v-if="loading" class="state-panel">正在加载…</div>
        <div v-else-if="loadError" class="state-panel error">{{ loadError }}</div>
        <div v-else-if="!rushSales.length" class="state-panel empty">下一场开售正在准备中</div>
        <div v-else class="rush-list">
          <article
            v-for="sale in rushSales.slice(0, 4)"
            :key="sale.id"
            class="rush-card"
            @click="openRush(sale)"
          >
            <div class="rush-mark">赴<br />场</div>
            <div class="rush-copy">
              <small>{{ formatRushTime(sale.starts_at) }} 开售</small>
              <h3>{{ sale.name }}</h3>
              <p>{{ sale.event_title || '限时票档' }} · 每人限 {{ sale.per_user_limit }} 张</p>
            </div>
            <strong>¥{{ (sale.rush_price_cents / 100).toFixed(0) }}</strong>
            <span class="rush-stock">余 {{ sale.remaining_quota }}/{{ sale.total_quota }}</span>
          </article>
        </div>
      </section>

      <section class="listing-block">
        <header class="block-heading">
          <div>
            <p>ON SALE</p>
            <h2>近期场次</h2>
            <span v-if="filterSummary">筛选 · {{ filterSummary }}</span>
          </div>
          <span>{{ eventTotal }} 场</span>
        </header>

        <div v-if="state.categories.length" class="category-row" aria-label="分类筛选">
          <button
            type="button"
            class="chip"
            :class="{ active: !state.selectedCategory }"
            @click="toggleCategory('')"
          >全部</button>
          <button
            v-for="item in state.categories"
            :key="item"
            type="button"
            class="chip"
            :class="{ active: state.selectedCategory === item }"
            :aria-pressed="state.selectedCategory === item"
            @click="toggleCategory(item)"
          >{{ item }}</button>
          <button
            v-if="hasActiveFilters"
            type="button"
            class="chip clear"
            aria-label="清除关键词和分类"
            title="清除关键词和分类"
            @click="clearFilters"
          >×</button>
        </div>

        <div v-if="loading" class="state-panel">正在收集值得奔赴的现场…</div>
        <div v-else-if="loadError" class="state-panel error">{{ loadError }}</div>
        <div v-else-if="!events.length" class="state-panel empty">
          <strong>没有符合条件的场次</strong>
          <p>试试换个城市、分类，或清空搜索词。</p>
        </div>
        <div v-else class="event-grid">
          <article
            v-for="(event, index) in events"
            :key="event.id"
            class="event-card"
            @click="router.push(`/events/${event.id}`)"
          >
            <div class="event-poster" :class="`poster-${index % 5}`">
              <img
                v-if="event.cover_url"
                :src="event.cover_url"
                :alt="event.title"
                @error="onCoverError"
              />
              <span>{{ event.category }}</span>
              <strong>{{ formatDate(event) }}</strong>
            </div>
            <div class="event-info">
              <h3>{{ event.title }}</h3>
              <p>
                <template v-if="eventCity(event)">{{ eventCity(event) }} · </template>
                {{ event.sessions?.[0]?.venue?.name || '场馆待公布' }}
              </p>
              <strong>
                {{ formatMoney(minPrice(event)) }}
                <small v-if="minPrice(event)"> 起</small>
              </strong>
            </div>
          </article>
        </div>
        <button
          v-if="canLoadMore"
          class="load-more"
          type="button"
          :disabled="loadingMore"
          @click="loadMore"
        >{{ loadingMore ? '加载中…' : '加载更多场次' }}</button>
      </section>
    </div>
  </div>
</template>

<style scoped>
.home-page { margin: 0; }
.listings {
  padding: 22px 3.2vw 48px;
  min-height: 70vh;
}
.listing-block { margin-top: 36px; }
.category-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: -4px 0 18px;
}
.chip {
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: rgba(255, 255, 255, .35);
  color: var(--ink);
  font-size: 12px;
  cursor: pointer;
}
.chip.active {
  border-color: var(--red);
  background: rgba(181, 52, 41, .08);
  color: var(--red);
  font-weight: 700;
}
.chip.clear {
  width: 32px;
  padding: 0;
  border-radius: 50%;
  color: var(--muted);
  font-size: 18px;
  line-height: 1;
}
.chip.clear:hover { border-color: var(--red); color: var(--red); }
.block-heading {
  display: flex;
  justify-content: space-between;
  align-items: end;
  gap: 18px;
  margin-bottom: 18px;
}
.block-heading p {
  margin: 0;
  color: var(--red);
  font-size: 11px;
  letter-spacing: .18em;
}
.block-heading h2 {
  margin: 6px 0 4px;
  font: 740 30px var(--font-display);
}
.block-heading span,
.block-heading a {
  color: var(--muted);
  font-size: 12px;
  text-decoration: none;
}
.rush-block .block-heading h2 { color: var(--red); }
.rush-list { display: grid; gap: 12px; }
.rush-card {
  min-height: 112px;
  padding: 16px 18px;
  border: 1px solid rgba(181, 52, 41, .55);
  border-radius: var(--radius-md);
  display: grid;
  grid-template-columns: 58px 1fr auto;
  gap: 10px 18px;
  align-items: center;
  cursor: pointer;
  background: rgba(255, 255, 255, .28);
  transition: transform .2s ease, box-shadow .2s ease;
}
.rush-card:hover { transform: translateY(-3px); box-shadow: var(--shadow-soft); }
.rush-mark {
  width: 52px;
  height: 68px;
  border-radius: var(--radius-sm);
  background: var(--red);
  color: white;
  display: grid;
  place-content: center;
  text-align: center;
  font: 700 18px/1.1 var(--font-display);
}
.rush-copy small, .rush-copy p, .rush-stock { color: var(--muted); font-size: 12px; }
.rush-copy h3 { margin: 6px 0; font: 700 22px var(--font-display); }
.rush-card > strong {
  color: var(--red);
  font-size: 28px;
  grid-row: 1 / 3;
  grid-column: 3;
}
.rush-stock { grid-column: 2; }
.event-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
}
.event-card {
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  overflow: hidden;
  background: rgba(255, 255, 255, .22);
  cursor: pointer;
  transition: transform .2s, box-shadow .2s;
}
.event-card:hover { transform: translateY(-4px); box-shadow: var(--shadow-lift); }
.event-poster {
  height: 180px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  color: white;
  position: relative;
  overflow: hidden;
}
.event-poster img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.event-poster::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(180deg, rgba(0, 0, 0, .08), rgba(0, 0, 0, .58));
}
.event-poster span, .event-poster strong { position: relative; z-index: 1; }
.event-poster span {
  align-self: flex-start;
  padding: 4px 10px;
  border-radius: var(--radius-pill);
  background: rgba(18, 18, 18, .7);
  font-size: 10px;
}
.event-poster strong { font-family: var(--font-display); font-size: 24px; }
.poster-0 { background: linear-gradient(145deg, #1b1b1b 10%, #75402f 58%, #d63f2e); }
.poster-1 { background: linear-gradient(145deg, #18324a, #287b86 64%, #b7d4c5); }
.poster-2 { background: linear-gradient(145deg, #27223b, #315ba5 55%, #e5ad5f); }
.poster-3 { background: linear-gradient(145deg, #1b1512, #674f34 55%, #a92e24); }
.poster-4 { background: linear-gradient(145deg, #294b3f, #67a86c 62%, #f3ca61); }
.event-info {
  min-height: 112px;
  padding: 12px;
  display: flex;
  flex-direction: column;
}
.event-info h3 { margin: 0; font-size: 14px; line-height: 1.45; }
.event-info p { margin: 8px 0 14px; color: var(--muted); font-size: 11px; }
.event-info > strong { margin-top: auto; color: var(--red); }
.event-info small { color: var(--muted); font-weight: 400; }
.state-panel {
  min-height: 120px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-lg);
  display: grid;
  place-content: center;
  text-align: center;
  color: var(--muted);
  gap: 8px;
}
.state-panel strong { color: var(--ink); font-size: 20px; }
.state-panel.error { color: var(--red); }
.load-more {
  width: 100%;
  height: 44px;
  margin-top: 16px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--ink);
  font-weight: 650;
  cursor: pointer;
}
.load-more:disabled { opacity: .6; cursor: wait; }
@media (max-width: 1000px) {
  .event-grid { grid-template-columns: repeat(2, 1fr); }
  .rush-card { grid-template-columns: 52px 1fr; }
  .rush-card > strong { grid-row: auto; grid-column: 2; font-size: 24px; }
}
@media (max-width: 640px) {
  .listings { padding: 16px 16px 12px; }
  .event-grid { grid-template-columns: 1fr; }
  .block-heading { align-items: start; flex-direction: column; }
}
</style>
