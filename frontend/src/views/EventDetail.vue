<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'
import heroImage from '../assets/fuchang-hero.png'
import SeatPickerDialog from '../components/ticket/SeatPickerDialog.vue'

const route = useRoute()
const router = useRouter()
const event = ref(null)
const loading = ref(true)
const selectedSessionId = ref('')
const selectedTier = ref(null)
const quantity = ref(1)
const sessionSeats = ref([])
const pickerOpen = ref(false)
const buyOpen = ref(false)
const commentPage = ref(1)
const commentPageSize = 20
const commentsLoadingMore = ref(false)

const comments = ref([])
const commentTotal = ref(0)
const commentsLoading = ref(false)
const commentDraft = ref('')
const commentSubmitting = ref(false)
const likingId = ref(null)

const token = computed(() => localStorage.getItem('access_token') || localStorage.getItem('token'))
const isSeated = computed(() => event.value?.sale_mode === 'seated')
const isExhibition = computed(() => event.value?.category === '展览')
const selectedSession = computed(() =>
  event.value?.sessions?.find(item => String(item.id) === String(selectedSessionId.value))
  || event.value?.sessions?.[0]
)

function sessionRemaining(session) {
  return (session?.ticket_tiers || []).reduce((sum, tier) => sum + Number(tier.remaining_quota || 0), 0)
}

function sessionSaleState(session) {
  if (!session) return 'unavailable'
  const now = Date.now()
  const start = session.sale_starts_at ? new Date(session.sale_starts_at).getTime() : 0
  const end = session.sale_ends_at ? new Date(session.sale_ends_at).getTime() : Infinity
  if (start && now < start) return 'not_started'
  if (end && Number.isFinite(end) && now > end) return 'ended'
  if (sessionRemaining(session) <= 0) return 'sold_out'
  return 'on_sale'
}

const saleState = computed(() => sessionSaleState(selectedSession.value))
const canWaitlist = computed(() =>
  selectedTier.value?.status === 'waitlist'
  && !isSeated.value
  && (saleState.value === 'on_sale' || saleState.value === 'sold_out')
)
const canBuy = computed(() => !!selectedTier.value && (
  canWaitlist.value
  || (saleState.value === 'on_sale' && selectedTier.value.status === 'on_sale')
))
const buyLabel = computed(() => {
  if (canWaitlist.value) return '登记候补'
  if (saleState.value === 'not_started') return '尚未开售'
  if (saleState.value === 'ended') return '已停售'
  if (saleState.value === 'sold_out') return isSeated.value ? '已售罄' : '登记候补'
  return isSeated.value ? '选座购票' : '立即购票'
})
const lowestPrice = computed(() => {
  const prices = (selectedSession.value?.ticket_tiers || [])
    .filter(tier => Number(tier.remaining_quota || 0) > 0)
    .map(tier => tier.price_cents)
  if (prices.length) return Math.min(...prices)
  return selectedTier.value?.price_cents
})

function saleStateText(state) {
  return { sold_out: '已售罄', not_started: '未开售', ended: '已停售', on_sale: '售票中' }[state] || ''
}

function selectSession(session) {
  selectedSessionId.value = String(session.id)
  const tiers = session.ticket_tiers || []
  selectedTier.value = tiers.find(tier => Number(tier.remaining_quota || 0) > 0) || tiers[0] || null
  quantity.value = 1
}

watch(selectedSession, async (session) => {
  if (!isSeated.value || !session?.id || !event.value?.id) return
  try {
    const seatRes = await api.getSessionSeats(event.value.id, session.id)
    sessionSeats.value = seatRes.data || []
  } catch (error) {
    sessionSeats.value = []
    ElMessage.error(error.response?.data?.msg || '座位图加载失败')
  }
})

onMounted(async () => {
  try {
    const res = await api.getEventDetail(route.params.id)
    event.value = res.data
    const first = event.value.sessions?.[0]
    if (first) selectSession(first)
    api.trackFunnelVisits('detail', [event.value.id])
    await loadComments()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '活动不存在或尚未发布')
  } finally {
    loading.value = false
  }
})

async function loadComments({ append = false } = {}) {
  if (append) commentsLoadingMore.value = true
  else commentsLoading.value = true
  try {
    const page = append ? commentPage.value + 1 : 1
    const res = await api.getEventComments(route.params.id, { page, page_size: commentPageSize })
    const list = res.data?.list || []
    comments.value = append ? [...comments.value, ...list] : list
    commentTotal.value = res.data?.total || comments.value.length
    commentPage.value = page
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '讨论区加载失败')
  } finally {
    commentsLoading.value = false
    commentsLoadingMore.value = false
  }
}

async function submitComment() {
  if (!token.value) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }
  const content = commentDraft.value.trim()
  if (content.length < 2) {
    ElMessage.warning('评论至少 2 个字')
    return
  }
  commentSubmitting.value = true
  try {
    const res = await api.createEventComment(route.params.id, content)
    comments.value = [res.data, ...comments.value]
    commentTotal.value += 1
    commentDraft.value = ''
    ElMessage.success('已发布到讨论区')
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '发布失败')
  } finally {
    commentSubmitting.value = false
  }
}

async function removeComment(item) {
  try {
    await api.deleteEventComment(item.id)
    comments.value = comments.value.filter(row => row.id !== item.id)
    commentTotal.value = Math.max(0, commentTotal.value - 1)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '删除失败')
  }
}

async function likeComment(item) {
  if (!token.value) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }
  likingId.value = item.id
  try {
    const res = await api.likeEventComment(item.id)
    item.like_count = res.data?.like_count ?? item.like_count
    if (res.data?.already_liked) {
      ElMessage.info('已经点过赞了')
    }
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '点赞失败')
  } finally {
    likingId.value = null
  }
}

function money(cents) {
  return `¥${(cents / 100).toFixed(cents % 100 ? 2 : 0)}`
}

function dateTime(value) {
  if (!value) return '时间待定'
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long', day: 'numeric', weekday: 'short', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function commentTime(value) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

async function buy() {
  if (!token.value) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }
  if (!canBuy.value) return
  if (isSeated.value) {
    if (!sessionSeats.value.length && selectedSession.value?.id) {
      try {
        const seatRes = await api.getSessionSeats(event.value.id, selectedSession.value.id)
        sessionSeats.value = seatRes.data || []
      } catch (error) {
        ElMessage.error(error.response?.data?.msg || '座位图加载失败')
        return
      }
    }
    pickerOpen.value = true
    return
  }
  buyOpen.value = true
}

function goCheckout({ tierId, quantity: qty, seatIds, waitlist }) {
  router.push({
    name: 'Checkout',
    params: { eventId: event.value.id },
    query: {
      ...(seatIds?.length
        ? { tier: tierId, seat_ids: seatIds.join(',') }
        : { tier: tierId, quantity: qty }),
      ...(waitlist ? { waitlist: '1' } : {}),
    },
  })
}

function confirmSeats(payload) {
  pickerOpen.value = false
  goCheckout({ tierId: payload.tierId, seatIds: payload.seatIds })
}

function confirmCounter() {
  if (!selectedTier.value) return
  buyOpen.value = false
  goCheckout({
    tierId: selectedTier.value.id,
    quantity: quantity.value,
    waitlist: canWaitlist.value,
  })
}

function onCounterTierChange(event) {
  const id = event.target.value
  selectedTier.value = (selectedSession.value?.ticket_tiers || []).find(item => String(item.id) === String(id)) || null
  quantity.value = 1
}
</script>

<template>
  <div class="detail-page">
    <div v-if="loading" class="detail-state">正在加载活动…</div>
    <div v-else-if="!event" class="detail-state">没有找到这个活动</div>
    <template v-else>
      <section class="event-hero">
        <img :src="event.cover_url || heroImage" :alt="event.title" />
        <div class="event-summary">
          <p>{{ event.category }} · {{ event.organizer?.name }}</p>
          <h1>{{ event.title }}</h1>
          <h2>{{ event.subtitle }}</h2>
          <dl v-if="event.sessions?.length">
            <div><dt>时间</dt><dd>{{ dateTime(selectedSession?.starts_at || event.sessions[0].starts_at) }}</dd></div>
            <div><dt>场馆</dt><dd>{{ selectedSession?.venue?.name || event.sessions[0].venue?.name }} · {{ selectedSession?.venue?.address || event.sessions[0].venue?.address }}</dd></div>
            <div><dt>规则</dt><dd>
              每账号限购 {{ event.max_tickets_per_order }} 张
              <span v-if="event.real_name_required"> · 实名制，购票时选择已绑定证件</span>
              <span v-if="isSeated"> · 选座入场</span>
              <span v-else-if="isExhibition"> · 门票入场</span>
              <span v-else> · 售罄可候补</span>
            </dd></div>
          </dl>
        </div>
      </section>

      <section class="detail-body">
        <div class="session-panel">
          <div class="section-title"><span>01</span><h2>场次</h2></div>
          <div v-for="session in event.sessions" :key="session.id" class="session-block" :class="{ active: String(session.id) === String(selectedSession?.id), disabled: sessionSaleState(session) !== 'on_sale' }" @click="selectSession(session)">
            <h3>{{ dateTime(session.starts_at) }} · {{ session.venue?.name }}</h3>
            <p class="session-meta">
              {{ saleStateText(sessionSaleState(session)) || (isSeated ? '选座入场' : (isExhibition ? '门票入场' : '按票档购买')) }}
              · 余 {{ sessionRemaining(session) }} 张
            </p>
          </div>

          <div class="section-title description-title"><span>02</span><h2>活动介绍</h2></div>
          <p class="description">{{ event.description || '主办方正在完善活动介绍。' }}</p>

          <div class="section-title description-title"><span>03</span><h2>讨论区</h2></div>
          <p class="discuss-lead">开售前聊阵容、问票、约人，登录即可参与。</p>

          <form class="comment-composer" @submit.prevent="submitComment">
            <textarea
              v-model="commentDraft"
              maxlength="500"
              rows="3"
              :placeholder="token ? '说点什么…' : '登录后参与讨论'"
              :disabled="!token || commentSubmitting"
            />
            <div class="composer-actions">
              <span>{{ commentDraft.trim().length }}/500</span>
              <button type="submit" :disabled="commentSubmitting">
                {{ token ? (commentSubmitting ? '发布中…' : '发布') : '去登录' }}
              </button>
            </div>
          </form>

          <div v-if="commentsLoading" class="comment-state">正在加载讨论…</div>
          <div v-else-if="!comments.length" class="comment-state">还没有人发言，来第一条吧。</div>
          <ul v-else class="comment-list">
            <li v-for="item in comments" :key="item.id">
              <header>
                <strong>{{ item.username }}</strong>
                <time>{{ commentTime(item.created_at) }}</time>
              </header>
              <p>{{ item.content }}</p>
              <footer>
                <button type="button" :disabled="likingId === item.id" @click="likeComment(item)">
                  赞 {{ item.like_count || 0 }}
                </button>
                <button v-if="item.is_owner" type="button" class="danger" @click="removeComment(item)">
                  删除
                </button>
              </footer>
            </li>
          </ul>
          <p v-if="commentTotal > comments.length" class="comment-more">
            共 {{ commentTotal }} 条 · 当前 {{ comments.length }} 条
            <button type="button" :disabled="commentsLoadingMore" @click="loadComments({ append: true })">
              {{ commentsLoadingMore ? '加载中…' : '查看更早评论' }}
            </button>
          </p>
        </div>
        <aside class="buy-bar">
          <div>
            <p>{{ isSeated ? '选座购票' : (isExhibition ? '购买门票' : '购买门票') }}</p>
            <strong>{{ lowestPrice != null ? money(lowestPrice) : '—' }} 起</strong>
          </div>
          <button class="primary-action" type="button" :disabled="!canBuy" @click="buy">
            {{ buyLabel }}
          </button>
        </aside>
      </section>

      <SeatPickerDialog
        v-if="isSeated"
        v-model="pickerOpen"
        :event="event"
        :session="selectedSession"
        :seats="sessionSeats"
        @confirm="confirmSeats"
      />
      <el-dialog v-model="buyOpen" :title="canWaitlist ? '登记候补' : '购买门票'" width="460px" align-center>
        <div class="buy-dialog">
          <p v-if="canWaitlist">售罄后先付沙箱款排队。有人退票按付款成功顺序派票，不用再抢；开场前仍未配到会原路退款。选座活动暂不支持候补。</p>
          <label>票档
            <select :value="String(selectedTier?.id || '')" @change="onCounterTierChange">
              <option
                v-for="tier in selectedSession?.ticket_tiers || []"
                :key="tier.id"
                :value="String(tier.id)"
                :disabled="tier.status !== 'waitlist' && Number(tier.remaining_quota || 0) <= 0"
              >
                {{ tier.name }} · {{ money(tier.price_cents) }}{{ tier.status === 'waitlist' ? ' · 候补中' : (Number(tier.remaining_quota || 0) <= 0 ? ' · 售罄' : '') }}
              </option>
            </select>
          </label>
          <label>数量
            <el-input-number
              v-model="quantity"
              :min="1"
              :max="Math.min(event.max_tickets_per_order || 1, canWaitlist ? (selectedTier?.purchase_limit || event.max_tickets_per_order || 1) : (selectedTier?.remaining_quota || 1))"
            />
          </label>
          <p>每账号本场限购 {{ event.max_tickets_per_order }} 张{{ event.real_name_required ? '，下一步勾选已绑定证件' : '' }}。</p>
        </div>
        <template #footer>
          <button
            class="primary-action"
            type="button"
            :disabled="!selectedTier || (!canWaitlist && Number(selectedTier.remaining_quota || 0) <= 0)"
            @click="confirmCounter"
          >{{ canWaitlist ? '去填写候补' : '去填写订单' }}</button>
        </template>
      </el-dialog>
    </template>
  </div>
</template>

<style scoped>
.detail-page { max-width: 1280px; margin: 0 auto; padding: 38px 4vw; }
.detail-state { min-height: 60vh; display: grid; place-content: center; color: var(--muted); }
.event-hero {
  min-height: 330px;
  display: grid;
  grid-template-columns: 46% 1fr;
  background: #1c1713;
  color: #f9f4ec;
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.event-hero > img { width: 100%; height: 100%; max-height: 390px; object-fit: cover; }
.event-summary { padding: 42px 46px; }
.event-summary > p { color: #e36854; letter-spacing: .1em; font-size: 12px; }
.event-summary h1 { margin: 18px 0 8px; font-family: var(--font-display); font-size: clamp(32px, 4vw, 54px); line-height: 1.12; }
.event-summary h2 { margin: 0; color: #bdb5ac; font-size: 17px; font-weight: 400; }
dl { margin-top: 35px; border-top: 1px solid rgba(255,255,255,.16); }
dl div { display: grid; grid-template-columns: 52px 1fr; padding: 11px 0; border-bottom: 1px solid rgba(255,255,255,.12); font-size: 13px; }
dt { color: #9f978f; }
dd { margin: 0; }
.detail-body { display: grid; grid-template-columns: 1fr 280px; gap: 50px; padding-top: 44px; }
.section-title { display: flex; align-items: baseline; gap: 12px; border-bottom: 1px solid var(--line); }
.section-title span { color: var(--red); font-family: var(--font-display); }
.section-title h2 { font-family: var(--font-display); font-size: 24px; }
.session-block { padding: 22px 0; border-bottom: 1px solid var(--line); cursor: pointer; }
.session-block.active { color: var(--red); }
.session-block.disabled h3 { color: var(--muted); }
.session-block h3 { margin: 0 0 8px; font-size: 14px; }
.session-meta { margin: 0; color: var(--muted); font-size: 13px; }
.description-title { margin-top: 30px; }
.description { color: #4d4842; line-height: 1.9; white-space: pre-line; }
.discuss-lead { color: var(--muted); font-size: 13px; }
.comment-composer { margin: 16px 0 22px; display: grid; gap: 10px; }
.comment-composer textarea {
  width: 100%;
  padding: 12px 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: rgba(255,255,255,.4);
  resize: vertical;
  font: inherit;
}
.composer-actions { display: flex; justify-content: space-between; align-items: center; }
.composer-actions span { color: var(--muted); font-size: 12px; }
.composer-actions button {
  height: 36px;
  padding: 0 18px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: #fff;
  font-weight: 700;
  cursor: pointer;
}
.composer-actions button:disabled { opacity: .55; cursor: not-allowed; }
.comment-state { padding: 28px 0; color: var(--muted); }
.comment-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 14px; }
.comment-list li { padding: 14px 0; border-bottom: 1px solid var(--line); }
.comment-list header { display: flex; gap: 12px; align-items: baseline; margin-bottom: 8px; }
.comment-list header strong { font-size: 14px; }
.comment-list header time { color: var(--muted); font-size: 12px; }
.comment-list p { margin: 0; line-height: 1.7; white-space: pre-wrap; }
.comment-list footer { margin-top: 10px; display: flex; gap: 12px; }
.comment-list footer button {
  border: 0; background: transparent; color: var(--muted); cursor: pointer; font-size: 12px; padding: 0;
}
.comment-list footer button.danger { color: var(--red); }
.comment-more { color: var(--muted); font-size: 12px; display: flex; gap: 12px; align-items: center; }
.comment-more button { border: 0; background: transparent; color: var(--red); cursor: pointer; font: inherit; }
.buy-bar {
  align-self: start;
  position: sticky;
  top: 96px;
  padding: 22px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: #fbf7ef;
  display: grid;
  gap: 16px;
}
.buy-bar p { margin: 0; color: var(--muted); font-size: 12px; }
.buy-bar strong { color: var(--red); font-size: 24px; }
.quantity-row, .total-row { margin-top: 24px; padding-top: 17px; border-top: 1px solid var(--line); display: flex; justify-content: space-between; align-items: center; }
.total-row strong { color: var(--red); font-size: 26px; }
.primary-action {
  width: 100%;
  height: 48px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: white;
  font-weight: 750;
  cursor: pointer;
}
.primary-action:disabled { opacity: .5; cursor: not-allowed; }
.buy-dialog { display: grid; gap: 16px; }
.buy-dialog label { display: grid; gap: 8px; font-size: 13px; }
.buy-dialog select { height: 42px; border: 1px solid var(--line-strong); border-radius: 8px; padding: 0 10px; }
.buy-dialog p { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.6; }
@media (max-width: 900px) {
  .event-hero, .detail-body { grid-template-columns: 1fr; }
  .detail-page { padding-bottom: 96px; }
  .buy-bar {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 24;
    border-radius: 0;
    border-left: 0;
    border-right: 0;
    border-bottom: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 16px calc(12px + env(safe-area-inset-bottom));
    box-shadow: 0 -8px 24px rgba(43, 32, 24, .08);
  }
  .buy-bar .primary-action { width: auto; min-width: 148px; }
}
</style>
