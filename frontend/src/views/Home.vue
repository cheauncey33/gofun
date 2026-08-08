<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import api from '../api'
import heroImage from '../assets/fuchang-hero.png'
import { useDiscovery } from '../stores/discovery'

const router = useRouter()
const loading = ref(true)
const events = ref([])
const eventTotal = ref(0)
const rushSales = ref([])
const loadError = ref('')
const heroProgress = ref(0)
const flipTrack = ref(null)
const {
  state,
  hasActiveFilters,
  toggleCategory,
  clearFilters,
  eventQuery,
  applyMeta,
} = useDiscovery()

/** 翻页只需滑过约 0.38 屏，动画就完成；进度与滚动线性同步，避免中间露白 */
const FLIP_TRAVEL_VH = 38

const featuredEvents = computed(() => events.value.slice(0, 12))
const heroFolded = computed(() => heroProgress.value > 0.92)
const filterSummary = computed(() => {
  const parts = []
  if (state.city) parts.push(state.city)
  if (state.selectedCategory) parts.push(state.selectedCategory)
  if (state.keyword) parts.push(`“${state.keyword}”`)
  return parts.join(' · ')
})

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value))
}

function updateHeroProgress() {
  const track = flipTrack.value
  if (!track) return
  const rect = track.getBoundingClientRect()
  const travel = Math.max(rect.height - window.innerHeight, 1)
  const next = clamp(-rect.top / travel, 0, 1)
  heroProgress.value = next
  document.documentElement.classList.toggle('home-flipping', next > 0.04)
  document.documentElement.style.setProperty('--home-hero-p', String(next))
}

async function loadCatalog() {
  loading.value = true
  loadError.value = ''
  try {
    const [eventRes, rushRes, metaRes] = await Promise.all([
      api.getEvents(eventQuery()),
      api.getRushSales(),
      api.getCatalogMeta().catch(() => null),
    ])
    events.value = eventRes.data?.list || []
    eventTotal.value = eventRes.data?.total ?? events.value.length
    rushSales.value = rushRes.data || []
    if (metaRes?.data) applyMeta(metaRes.data)
  } catch (error) {
    loadError.value = error.response?.data?.msg || '暂时无法连接票务服务'
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  document.documentElement.classList.add('home-scroll')
  window.addEventListener('scroll', updateHeroProgress, { passive: true })
  window.addEventListener('resize', updateHeroProgress)
  updateHeroProgress()
  await loadCatalog()
})

watch(
  () => [state.city, state.keyword, state.selectedCategory],
  () => {
    loadCatalog()
  },
)

onBeforeUnmount(() => {
  document.documentElement.classList.remove('home-scroll', 'home-flipping')
  document.documentElement.style.removeProperty('--home-hero-p')
  window.removeEventListener('scroll', updateHeroProgress)
  window.removeEventListener('resize', updateHeroProgress)
})

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

function scrollToListings() {
  const track = flipTrack.value
  if (!track) return
  const top = window.scrollY + track.getBoundingClientRect().top + (window.innerHeight * FLIP_TRAVEL_VH) / 100
  window.scrollTo({ top, behavior: 'smooth' })
}

function eventCity(event) {
  return event.sessions?.[0]?.venue?.city || ''
}

function onCoverError(event) {
  event.target.style.display = 'none'
}
</script>

<template>
  <div
    class="home-page"
    :style="{ '--hero-p': heroProgress, '--flip-travel': `${FLIP_TRAVEL_VH}vh` }"
    :class="{ folded: heroFolded }"
  >
    <!-- 轨道高度 = 一屏 + 短行程：滑过约 0.42 屏翻页完成，sticky 随即释放 -->
    <div ref="flipTrack" class="flip-track">
      <section class="hero-panel" aria-label="Gofun 封面">
        <img class="hero-media" :src="heroImage" alt="暖色灯光下的现场演出" />
        <div class="hero-veil" />
        <div class="hero-copy">
            <p class="brand-mark">Gofun</p>
          <h1>赴热爱之场，见想见的人。</h1>
          <p class="hero-lead">发现值得奔赴的现场</p>
          <div class="hero-actions">
            <button type="button" class="primary" @click="scrollToListings">浏览场次</button>
            <router-link class="ghost" to="/rush-sales">限时开售</router-link>
          </div>
        </div>
        <button class="scroll-cue" type="button" @click="scrollToListings" aria-label="向下浏览场次">
          <span>下滑翻页</span>
          <i />
        </button>
      </section>
    </div>

    <!-- 上拉一整屏：翻页过程中场次始终贴在封面下方，无空白带 -->
    <div id="home-listings" class="listings">
      <section class="listing-block rush-block">
        <header class="block-heading">
          <div>
            <p>RUSH</p>
            <h2>限时开售</h2>
            <span>热门场次优先，限量发售</span>
          </div>
          <router-link to="/rush-sales">全部开售 →</router-link>
        </header>

        <div v-if="loading" class="state-panel">正在同步开售信息…</div>
        <div v-else-if="loadError" class="state-panel error">{{ loadError }}</div>
        <div v-else-if="!rushSales.length" class="state-panel empty">下一场开售正在准备中</div>
        <div v-else class="rush-list">
          <article
            v-for="sale in rushSales.slice(0, 4)"
            :key="sale.id"
            class="rush-card"
            @click="router.push('/rush-sales')"
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
            <span v-else>常规售票中的活动</span>
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
        <div v-else-if="!featuredEvents.length" class="state-panel empty">
          <strong>没有符合条件的场次</strong>
          <p>试试换个城市、分类，或清空搜索词。</p>
        </div>
        <div v-else class="event-grid">
          <article
            v-for="(event, index) in featuredEvents"
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
      </section>
    </div>
  </div>
</template>

<style scoped>
.home-page {
  --hero-p: 0;
  --flip-travel: 38vh;
  margin: 0;
}

.flip-track {
  height: calc(100dvh + var(--flip-travel));
  position: relative;
}

.hero-panel {
  position: sticky;
  top: 0;
  z-index: 3;
  height: 100dvh;
  min-height: 560px;
  overflow: hidden;
  transform-origin: top center;
  /* -100% 与场次上拉行程对齐：下滑多少，封面收多少 */
  transform: translate3d(0, calc(var(--hero-p) * -100%), 0);
  filter: brightness(calc(1 - var(--hero-p) * .2));
  opacity: calc(1 - var(--hero-p) * 1.05);
  will-change: transform, filter, opacity;
  pointer-events: auto;
}

.home-page.folded .hero-panel {
  pointer-events: none;
  visibility: hidden;
}

.hero-media {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 118%;
  object-fit: cover;
  transform: scale(calc(1.04 + var(--hero-p) * .06));
  animation: heroDrift 18s ease-in-out infinite alternate;
}

@keyframes heroDrift {
  from { object-position: 50% 42%; }
  to { object-position: 48% 50%; }
}

.hero-veil {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(90deg, rgba(12, 8, 6, .78) 0%, rgba(12, 8, 6, .28) 48%, rgba(12, 8, 6, .55) 100%),
    linear-gradient(180deg, rgba(12, 8, 6, .35), transparent 28%, rgba(12, 8, 6, .72) 100%);
}

.hero-copy {
  position: relative;
  z-index: 2;
  max-width: 720px;
  min-height: 100%;
  padding: clamp(96px, 16vh, 150px) 5vw 120px;
  color: #f6eee4;
  display: flex;
  flex-direction: column;
  justify-content: center;
  /* 很快淡出，避免与场次「RUSH / 限时开售」叠字 */
  opacity: calc(1 - var(--hero-p) * 5);
  transform: translateY(calc(var(--hero-p) * -36px));
}

.brand-mark {
  margin: 0 0 18px;
  color: #ef6d58;
  font: 780 clamp(42px, 6vw, 72px)/1 var(--font-display);
  letter-spacing: .12em;
  animation: brandIn .9s cubic-bezier(.2, .8, .2, 1) both;
}

.hero-copy h1 {
  margin: 0;
  font: 760 clamp(34px, 4.4vw, 58px)/1.18 var(--font-display);
  letter-spacing: -.02em;
  animation: copyIn .95s .08s cubic-bezier(.2, .8, .2, 1) both;
}

.hero-lead {
  margin: 18px 0 0;
  color: rgba(246, 238, 228, .78);
  letter-spacing: .16em;
  font-size: 13px;
  animation: copyIn 1s .16s cubic-bezier(.2, .8, .2, 1) both;
}

.hero-actions {
  display: flex;
  gap: 12px;
  margin-top: 34px;
  animation: copyIn 1.05s .22s cubic-bezier(.2, .8, .2, 1) both;
}

.hero-actions .primary,
.hero-actions .ghost {
  height: 46px;
  min-width: 128px;
  padding: 0 20px;
  border: 0;
  border-radius: var(--radius-pill);
  display: inline-grid;
  place-content: center;
  text-decoration: none;
  font-weight: 700;
  cursor: pointer;
}

.hero-actions .primary {
  background: var(--red);
  color: white;
}

.hero-actions .ghost {
  border: 1px solid rgba(246, 238, 228, .55);
  color: #f6eee4;
  background: transparent;
}

.scroll-cue {
  position: absolute;
  left: 50%;
  bottom: 28px;
  z-index: 3;
  transform: translateX(-50%);
  border: 0;
  background: transparent;
  color: rgba(246, 238, 228, .82);
  display: grid;
  justify-items: center;
  gap: 8px;
  cursor: pointer;
  opacity: calc(1 - var(--hero-p) * 2.2);
}

.scroll-cue span {
  font-size: 11px;
  letter-spacing: .18em;
}

.scroll-cue i {
  width: 1px;
  height: 28px;
  background: rgba(246, 238, 228, .75);
  animation: cuePulse 1.4s ease-in-out infinite;
}

.listings {
  position: relative;
  z-index: 6;
  /* 文档起点 = flip-travel；再用 translate 与封面底边同步上推，全程无空白带 */
  margin-top: -100dvh;
  min-height: 100dvh;
  /* 顶出固定顶栏高度，避免 Gofun logo 压到 RUSH / 限时开售 */
  padding: calc(68px + 20px) 3.2vw 48px;
  background: var(--paper);
  transform: translate3d(
    0,
    calc((1 - var(--hero-p)) * (100dvh - var(--flip-travel))),
    0
  );
  box-shadow: 0 -18px 40px rgba(20, 14, 10, calc(var(--hero-p) * .16));
  will-change: transform;
  isolation: isolate;
}

.listing-block + .listing-block {
  margin-top: 48px;
}

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
  border-style: solid;
  border-radius: 50%;
  color: var(--muted);
  font-size: 18px;
  line-height: 1;
}

.chip.clear:hover {
  border-color: var(--red);
  color: var(--red);
}

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

.rush-block .block-heading h2 {
  color: var(--red);
}

.rush-list {
  display: grid;
  gap: 12px;
}

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

.rush-card:hover {
  transform: translateY(-3px);
  box-shadow: var(--shadow-soft);
}

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

.rush-copy small,
.rush-copy p,
.rush-stock {
  color: var(--muted);
  font-size: 12px;
}

.rush-copy h3 {
  margin: 6px 0;
  font: 700 22px var(--font-display);
}

.rush-card > strong {
  color: var(--red);
  font-size: 28px;
  grid-row: 1 / 3;
  grid-column: 3;
}

.rush-stock {
  grid-column: 2;
}

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

.event-card:hover {
  transform: translateY(-4px);
  box-shadow: var(--shadow-lift);
}

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

.event-poster span,
.event-poster strong {
  position: relative;
  z-index: 1;
}

.event-poster span {
  align-self: flex-start;
  padding: 4px 10px;
  border-radius: var(--radius-pill);
  background: rgba(18, 18, 18, .7);
  font-size: 10px;
}

.event-poster strong {
  font-family: var(--font-display);
  font-size: 24px;
}

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

.event-info h3 {
  margin: 0;
  font-size: 14px;
  line-height: 1.45;
}

.event-info p {
  margin: 8px 0 14px;
  color: var(--muted);
  font-size: 11px;
}

.event-info > strong {
  margin-top: auto;
  color: var(--red);
}

.event-info small {
  color: var(--muted);
  font-weight: 400;
}

.state-panel {
  min-height: 160px;
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

@keyframes brandIn {
  from { opacity: 0; transform: translateY(18px); }
  to { opacity: 1; transform: none; }
}

@keyframes copyIn {
  from { opacity: 0; transform: translateY(22px); }
  to { opacity: 1; transform: none; }
}

@keyframes cuePulse {
  0%, 100% { transform: scaleY(.55); opacity: .4; }
  50% { transform: scaleY(1); opacity: 1; }
}

@media (max-width: 1000px) {
  .event-grid { grid-template-columns: repeat(2, 1fr); }
  .rush-card { grid-template-columns: 52px 1fr; }
  .rush-card > strong { grid-row: auto; grid-column: 2; font-size: 24px; }
}

@media (max-width: 640px) {
  .hero-copy { padding: 92px 20px 110px; }
  .hero-actions { flex-direction: column; align-items: stretch; }
  .listings { padding: 24px 16px 12px; }
  .event-grid { grid-template-columns: 1fr; }
  .block-heading { align-items: start; flex-direction: column; }
  .hero-panel { min-height: 100dvh; }
}
</style>
