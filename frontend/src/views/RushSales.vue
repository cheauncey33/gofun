<script setup>
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'
import { rushStockLabel } from '../utils/display'
import { useNow } from '../utils/now'
import { canRush, matchesRushFilter, phaseLabel, salePhase } from '../utils/rush'
import heroImage from '../assets/fuchang-hero.png'

const route = useRoute()
const router = useRouter()
const sales = ref([])
const loading = ref(true)
const acting = ref(null)
const activeSale = ref(null)
const nowTs = useNow()
const quantity = ref(1)
const userHint = reactive({ contactName: '', contactPhone: '' })
const form = reactive({
  contactName: '',
  contactPhone: '',
  termsAccepted: false,
  attendees: [],
})

const maxQuantity = computed(() => Math.max(1, Number(activeSale.value?.per_user_limit) || 1))
const rushTotalCents = computed(() => (activeSale.value?.rush_price_cents || 0) * quantity.value)

const phaseFilter = computed(() => {
  const value = String(route.query.phase || '')
  return value === 'live' || value === 'scheduled' ? value : 'all'
})
const liveCount = computed(() => sales.value.filter(item => canRush(item, nowTs.value)).length)
const scheduledCount = computed(() => sales.value.filter(item => salePhase(item, nowTs.value) === 'scheduled').length)
const visibleSales = computed(() => sales.value.filter(item => matchesRushFilter(item, phaseFilter.value, nowTs.value)))

function setPhaseFilter(value) {
  const query = { ...route.query }
  if (value === 'all') delete query.phase
  else query.phase = value
  router.replace({ query })
}

const focusedSale = computed(() => {
  const saleId = route.query.sale
  if (saleId) return sales.value.find(item => String(item.id) === String(saleId)) || null
  const eventId = route.query.event
  if (eventId) return sales.value.find(item => String(item.event_id) === String(eventId)) || null
  return null
})

onMounted(async () => {
  try {
    const tasks = [api.getRushSales()]
    if (localStorage.getItem('access_token') || localStorage.getItem('token')) {
      tasks.push(api.getUserInfo().catch(() => null))
    }
    const [salesRes, userRes] = await Promise.all(tasks)
    sales.value = salesRes.data || []
    userHint.contactName = userRes?.data?.username || ''
    userHint.contactPhone = userRes?.data?.phone || ''
    api.trackFunnelVisits('browse', sales.value.map(item => item.event_id))
    await nextTick()
    applyDeepLink()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '限时开售加载失败')
  } finally {
    loading.value = false
  }
})

function applyDeepLink() {
  const sale = focusedSale.value
  if (!sale) return
  document.getElementById(`rush-${sale.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  if (canRush(sale, nowTs.value)) openRush(sale)
}

function money(cents) {
  if (cents == null) return '—'
  return `¥${(cents / 100).toFixed(cents % 100 ? 2 : 0)}`
}

function pad(value) {
  return String(value).padStart(2, '0')
}

function countdownLabel(sale, now = Date.now()) {
  const phase = salePhase(sale, now)
  const target = phase === 'scheduled' ? sale.starts_at : sale.ends_at
  if (!target || phase === 'sold_out' || phase === 'ended') return ''
  const diff = new Date(target).getTime() - now
  if (diff <= 0) return ''
  const totalSec = Math.floor(diff / 1000)
  const days = Math.floor(totalSec / 86400)
  const hours = Math.floor((totalSec % 86400) / 3600)
  const minutes = Math.floor((totalSec % 3600) / 60)
  const seconds = totalSec % 60
  const clockText = days > 0
    ? `${days}天 ${pad(hours)}:${pad(minutes)}:${pad(seconds)}`
    : `${pad(hours)}:${pad(minutes)}:${pad(seconds)}`
  return phase === 'scheduled' ? `距开售 ${clockText}` : `距结束 ${clockText}`
}

function ctaLabel(sale, now = Date.now()) {
  const phase = salePhase(sale, now)
  if (phase === 'scheduled') return '即将开售'
  if (phase === 'sold_out' || phase === 'ended') return '已结束'
  return '立即抢票'
}

function salePlace(sale) {
  return [sale.city, sale.venue_name].filter(Boolean).join(' · ')
}

function saleTime(sale) {
  const value = sale.session_starts_at
  if (!value) return dateTime(sale.starts_at)
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', weekday: 'short', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function dateTime(value) {
  if (!value) return '时间待定'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function emptyAttendee() {
  return { name: '', id_type: 'id_card', id_number: '' }
}

function syncAttendees(sale, qty) {
  if (!sale?.real_name_required) {
    form.attendees = []
    return
  }
  const next = Array.from({ length: qty }, (_, index) => form.attendees[index] || emptyAttendee())
  form.attendees = next
}

function loginRedirect(sale) {
  const path = sale?.id ? `/rush-sales?sale=${sale.id}` : '/rush-sales'
  return `/login?redirect=${encodeURIComponent(path)}`
}

function openRush(sale) {
  if (!canRush(sale, Date.now())) {
    const phase = salePhase(sale)
    ElMessage.info(phase === 'scheduled' ? '这场还没开售' : '这场已经结束')
    return
  }
  if (!localStorage.getItem('access_token') && !localStorage.getItem('token')) {
    router.push(loginRedirect(sale))
    return
  }
  activeSale.value = sale
  quantity.value = 1
  form.contactName = userHint.contactName
  form.contactPhone = userHint.contactPhone
  form.termsAccepted = false
  syncAttendees(sale, 1)
  api.trackFunnelVisits('detail', [sale.event_id])
  api.trackFunnelVisits('checkout', [sale.event_id])
}

function onCoverError(event) {
  event.target.style.display = 'none'
}

watch(quantity, (value) => {
  const sale = activeSale.value
  if (!sale) return
  const max = Math.max(1, Number(sale.per_user_limit) || 1)
  const next = Math.min(max, Math.max(1, Number.parseInt(value, 10) || 1))
  if (next !== Number(value)) {
    quantity.value = next
    return
  }
  syncAttendees(sale, next)
})

function closeRush() {
  if (acting.value) return
  activeSale.value = null
}

function validate() {
  if (form.contactName.trim().length < 2) return '请填写至少 2 个字符的联系人姓名'
  if (!/^1[3-9]\d{9}$/.test(form.contactPhone.trim())) return '请填写正确的 11 位手机号'
  if (quantity.value < 1 || quantity.value > maxQuantity.value) return `每人限购 ${maxQuantity.value} 张`
  if (activeSale.value?.real_name_required) {
    for (let index = 0; index < form.attendees.length; index += 1) {
      const attendee = form.attendees[index]
      if (!attendee || attendee.name.trim().length < 2) return `请填写观演人 ${index + 1} 的姓名`
      if (!/^\d{17}[\dXx]$/.test(attendee.id_number.trim())) return `观演人 ${index + 1} 的身份证格式不正确`
    }
    const ids = form.attendees.map(item => item.id_number.trim().toUpperCase())
    if (new Set(ids).size !== ids.length) return '同一证件不能重复绑定多张票'
  }
  if (!form.termsAccepted) return '请先阅读并同意购票须知'
  return ''
}

async function confirmRush() {
  const sale = activeSale.value
  if (!sale || acting.value) return
  const message = validate()
  if (message) {
    ElMessage.warning(message)
    return
  }
  acting.value = sale.id
  try {
    const orderRes = await api.executeRushSale(sale.id, quantity.value, {
      contact_name: form.contactName.trim(),
      contact_phone: form.contactPhone.trim(),
      terms_accepted: form.termsAccepted,
      attendees: sale.real_name_required
        ? form.attendees.map(item => ({
          name: item.name.trim(),
          id_type: 'id_card',
          id_number: item.id_number.trim(),
        }))
        : [],
    })
    ElMessage.success('抢票已提交')
    activeSale.value = null
    router.push(`/cashier/${orderRes.data.order_id}`)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '本次抢票未成功')
  } finally {
    acting.value = null
  }
}
</script>

<template>
  <div class="rush-page">
    <header class="rush-heading">
      <p>RUSH</p>
      <h1>限时开售</h1>
      <span>同一场活动上的限量特惠档，原价仍在活动页。</span>
      <small v-if="sales.length">
        {{ liveCount }} 场抢票中
        <template v-if="scheduledCount"> · {{ scheduledCount }} 场即将开售</template>
      </small>
      <div v-if="sales.length" class="rush-filters">
        <button type="button" :class="{ active: phaseFilter === 'all' }" @click="setPhaseFilter('all')">全部</button>
        <button type="button" :class="{ active: phaseFilter === 'live' }" @click="setPhaseFilter('live')">抢票中 {{ liveCount }}</button>
        <button type="button" :class="{ active: phaseFilter === 'scheduled' }" @click="setPhaseFilter('scheduled')">即将开售 {{ scheduledCount }}</button>
      </div>
    </header>

    <div v-if="loading" class="rush-state">正在加载…</div>
    <div v-else-if="!sales.length" class="rush-state">目前没有进行中的限时开售</div>
    <div v-else-if="!visibleSales.length" class="rush-state">这一档暂时没有场次</div>
    <div v-else class="ticket-stack">
      <article
        v-for="sale in visibleSales"
        :id="`rush-${sale.id}`"
        :key="sale.id"
        class="rush-ticket"
        :class="[salePhase(sale, nowTs), { focused: String(focusedSale?.id) === String(sale.id) }]"
      >
        <figure class="cover">
          <img :src="sale.cover_url || heroImage" :alt="sale.event_title || sale.name" @error="onCoverError" />
        </figure>
        <div class="copy">
          <div class="tags">
            <em>{{ phaseLabel(salePhase(sale, nowTs)) }}</em>
            <span>{{ countdownLabel(sale, nowTs) || dateTime(sale.starts_at) }}</span>
          </div>
          <h2>{{ sale.name }}</h2>
          <p>
            {{ sale.event_title || '活动票档' }}
            <template v-if="salePlace(sale)"> · {{ salePlace(sale) }}</template>
          </p>
          <p class="meta">{{ [rushStockLabel(sale), `每人限 ${sale.per_user_limit} 张`, saleTime(sale)].filter(Boolean).join(' · ') }}</p>
        </div>
        <div class="side">
          <div class="price">
            <strong>{{ money(sale.rush_price_cents) }}</strong>
            <s v-if="sale.original_price_cents > sale.rush_price_cents">{{ money(sale.original_price_cents) }}</s>
          </div>
          <button
            type="button"
            :disabled="!canRush(sale, nowTs)"
            @click="openRush(sale)"
          >{{ ctaLabel(sale, nowTs) }}</button>
          <div class="side-links">
            <router-link v-if="sale.event_id" :to="`/events/${sale.event_id}`">查看详情</router-link>
          </div>
        </div>
      </article>
    </div>

    <div v-if="activeSale" class="rush-modal" @click.self="closeRush">
      <section class="rush-panel">
        <header>
          <p>确认购票信息</p>
          <h2>{{ activeSale.name }}</h2>
          <span>{{ money(activeSale.rush_price_cents) }} · 每人限 {{ maxQuantity }} 张</span>
        </header>
        <label>购买数量 *
          <input
            v-model.number="quantity"
            type="number"
            min="1"
            :max="maxQuantity"
          />
        </label>
        <label>联系人姓名 *<input v-model.trim="form.contactName" type="text" maxlength="64" /></label>
        <label>联系人手机 *<input v-model.trim="form.contactPhone" type="tel" maxlength="11" /></label>
        <template v-if="activeSale.real_name_required">
          <div v-for="(attendee, index) in form.attendees" :key="index" class="attendee-block">
            <strong>观演人 {{ String(index + 1).padStart(2, '0') }}</strong>
            <label>姓名 *<input v-model.trim="attendee.name" type="text" maxlength="64" /></label>
            <label>身份证号 *<input v-model.trim="attendee.id_number" type="text" maxlength="18" /></label>
          </div>
        </template>
        <label class="terms">
          <input v-model="form.termsAccepted" type="checkbox" />
          <span>我已阅读并同意购票须知与退改规则</span>
        </label>
        <div class="actions">
          <button type="button" class="ghost" :disabled="!!acting" @click="closeRush">取消</button>
          <button type="button" :disabled="acting === activeSale.id" @click="confirmRush">
            {{ acting === activeSale.id ? '提交中…' : `确认抢票 ${money(rushTotalCents)}` }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.rush-page {
  max-width: 1040px;
  min-height: 70vh;
  margin: 0 auto;
  padding: 40px 4vw 80px;
}
.rush-heading { margin-bottom: 28px; }
.rush-heading p { margin: 0; color: var(--red); font-size: 11px; letter-spacing: .18em; }
.rush-heading h1 { margin: 6px 0 8px; font-family: var(--font-display); font-size: clamp(30px, 4vw, 40px); }
.rush-heading span { display: block; color: var(--muted); line-height: 1.6; }
.rush-heading small { display: block; margin-top: 10px; color: var(--ink); font-size: 13px; }
.rush-filters { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
.rush-filters button {
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--ink);
  font: 650 13px inherit;
  cursor: pointer;
}
.rush-filters button.active { border-color: var(--red); background: var(--red); color: #fff; }
.rush-state {
  min-height: 260px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-lg);
  display: grid;
  place-content: center;
  color: var(--muted);
}
.ticket-stack { display: grid; gap: 12px; }
.rush-ticket {
  display: grid;
  grid-template-columns: 136px minmax(0, 1fr) 148px;
  gap: 20px;
  align-items: center;
  padding: 16px 18px;
  border: 1px solid var(--line);
  border-radius: 18px;
  background: rgba(255, 250, 243, .88);
}
.rush-ticket.focused { border-color: rgba(181, 52, 41, .35); }
.cover {
  margin: 0;
  width: 136px;
  height: 86px;
  border-radius: 10px;
  overflow: hidden;
  background: var(--paper-deep);
}
.cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
.copy { display: grid; gap: 5px; min-width: 0; }
.tags { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.tags em {
  font-style: normal;
  color: var(--red);
  font-size: 11px;
  font-weight: 700;
}
.rush-ticket.scheduled .tags em { color: var(--blue); }
.rush-ticket.ending .tags em { color: #a14b10; }
.tags span { color: var(--muted); font-size: 12px; }
.copy h2 { margin: 0; font: 700 22px/1.25 var(--font-display); }
.copy p, .copy .meta { margin: 0; color: var(--muted); font-size: 13px; line-height: 1.5; }
.side {
  display: grid;
  justify-items: end;
  align-content: center;
  gap: 8px;
}
.price { display: flex; align-items: baseline; gap: 8px; }
.price strong { color: var(--red); font-size: 24px; }
.price s { color: var(--muted); font-size: 13px; }
.side button, .actions button {
  height: 40px;
  padding: 0 18px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: white;
  font-weight: 700;
  cursor: pointer;
}
.side button:disabled {
  background: var(--paper-deep);
  color: var(--muted);
  cursor: not-allowed;
}
.side-links { display: flex; align-items: center; gap: 10px; }
.side a { color: var(--muted); text-decoration: none; font-size: 12px; }
.side a:hover { color: var(--red); }
.rush-modal { position: fixed; inset: 0; z-index: 40; background: rgba(20, 16, 14, .42); display: grid; place-items: center; padding: 20px; }
.rush-panel { width: min(480px, 100%); padding: 24px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); background: var(--paper, #fff7f0); box-shadow: var(--shadow-lift); display: grid; gap: 12px; }
.rush-panel header { margin: 0 0 8px; }
.rush-panel header h2 { margin: 4px 0; font: 700 28px var(--font-display); }
.rush-panel label { display: grid; gap: 6px; color: var(--muted); font-size: 13px; }
.rush-panel input[type="text"], .rush-panel input:not([type]), .rush-panel input[type="tel"], .rush-panel input[type="number"] {
  height: 42px; padding: 0 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: white; color: var(--ink); font: 15px var(--font-body, sans-serif);
}
.attendee-block { display: grid; gap: 10px; padding: 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: rgba(255,255,255,.4); }
.attendee-block strong { font-size: 13px; }
.terms { grid-template-columns: 18px 1fr; align-items: start; gap: 10px; color: var(--ink); }
.terms input { width: 18px; height: 18px; margin-top: 2px; }
.actions { display: grid; grid-template-columns: 1fr 1.4fr; gap: 10px; margin-top: 8px; }
.actions .ghost { background: transparent; border: 1px solid var(--line-strong); border-radius: var(--radius-pill); color: var(--ink); }
@media (max-width: 720px) {
  .rush-ticket { grid-template-columns: 1fr; gap: 12px; }
  .cover { width: 100%; height: 140px; }
  .side { justify-items: start; }
}
</style>
