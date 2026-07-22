<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'
import heroImage from '../assets/fuchang-hero.png'

const route = useRoute()
const router = useRouter()
const event = ref(null)
const loading = ref(true)
const selectedTier = ref(null)
const quantity = ref(1)

const comments = ref([])
const commentTotal = ref(0)
const commentsLoading = ref(false)
const commentDraft = ref('')
const commentSubmitting = ref(false)
const likingId = ref(null)

const token = computed(() => localStorage.getItem('access_token') || localStorage.getItem('token'))
const selectedSession = computed(() =>
  event.value?.sessions?.find(item =>
    item.ticket_tiers?.some(tier => tier.id === selectedTier.value?.id)
  )
)

onMounted(async () => {
  try {
    const res = await api.getEventDetail(route.params.id)
    event.value = res.data
    selectedTier.value = event.value.sessions?.[0]?.ticket_tiers?.[0] || null
    await loadComments()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '活动不存在或尚未发布')
  } finally {
    loading.value = false
  }
})

async function loadComments() {
  commentsLoading.value = true
  try {
    const res = await api.getEventComments(route.params.id, { page: 1, page_size: 30 })
    comments.value = res.data?.list || []
    commentTotal.value = res.data?.total || 0
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '讨论区加载失败')
  } finally {
    commentsLoading.value = false
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
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long', day: 'numeric', weekday: 'short', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function commentTime(value) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function buy() {
  if (!token.value) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }
  if (!selectedTier.value) return
  router.push({
    name: 'Checkout',
    params: { eventId: event.value.id },
    query: { tier: selectedTier.value.id, quantity: quantity.value },
  })
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
            <div><dt>时间</dt><dd>{{ dateTime(event.sessions[0].starts_at) }}</dd></div>
            <div><dt>场馆</dt><dd>{{ event.sessions[0].venue?.name }} · {{ event.sessions[0].venue?.address }}</dd></div>
            <div><dt>规则</dt><dd>单笔最多 {{ event.max_tickets_per_order }} 张<span v-if="event.real_name_required"> · 实名制</span></dd></div>
          </dl>
        </div>
      </section>

      <section class="purchase-layout">
        <div class="session-panel">
          <div class="section-title"><span>01</span><h2>选择场次与票档</h2></div>
          <div v-for="session in event.sessions" :key="session.id" class="session-block">
            <h3>{{ dateTime(session.starts_at) }} · {{ session.venue?.name }}</h3>
            <div class="tier-grid">
              <button
                v-for="tier in session.ticket_tiers"
                :key="tier.id"
                type="button"
                :class="{ selected: selectedTier?.id === tier.id, soldout: tier.status === 'sold_out' }"
                :disabled="tier.status === 'sold_out'"
                @click="selectedTier = tier"
              >
                <span>{{ tier.name }}</span>
                <strong>{{ money(tier.price_cents) }}</strong>
                <small>余 {{ tier.remaining_quota }} 张 · 限 {{ tier.purchase_limit }} 张</small>
              </button>
            </div>
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
          <p v-if="commentTotal > comments.length" class="comment-more">共 {{ commentTotal }} 条 · 当前展示最新 {{ comments.length }} 条</p>
        </div>

        <aside class="checkout-card">
          <p>已选票档</p>
          <h3>{{ selectedTier?.name || '请选择票档' }}</h3>
          <span>{{ selectedSession ? dateTime(selectedSession.starts_at) : '—' }}</span>
          <div class="quantity-row">
            <label for="quantity">数量</label>
            <el-input-number
              id="quantity"
              v-model="quantity"
              :min="1"
              :max="Math.min(selectedTier?.purchase_limit || 1, selectedTier?.remaining_quota || 1)"
            />
          </div>
          <div class="total-row">
            <span>合计</span>
            <strong>{{ selectedTier ? money(selectedTier.price_cents * quantity) : '—' }}</strong>
          </div>
          <button class="primary-action" type="button" :disabled="!selectedTier" @click="buy">
            填写购票信息
          </button>
          <small>下一步填写联系人；实名制活动还需为每张票填写观演人。</small>
        </aside>
      </section>
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
.purchase-layout { display: grid; grid-template-columns: 1fr 330px; gap: 50px; padding-top: 44px; }
.section-title { display: flex; align-items: baseline; gap: 12px; border-bottom: 1px solid var(--line); }
.section-title span { color: var(--red); font-family: var(--font-display); }
.section-title h2 { font-family: var(--font-display); font-size: 24px; }
.session-block { padding: 22px 0; border-bottom: 1px solid var(--line); }
.session-block h3 { margin: 0 0 13px; font-size: 14px; }
.tier-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.tier-grid button {
  padding: 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: transparent;
  text-align: left;
  display: grid;
  gap: 7px;
  cursor: pointer;
}
.tier-grid button.selected {
  border-color: var(--red);
  background: rgba(181,52,41,.06);
  box-shadow: inset 0 0 0 1px var(--red);
}
.tier-grid button.soldout { opacity: .45; cursor: not-allowed; }
.tier-grid strong { color: var(--red); font-size: 19px; }
.tier-grid small { color: var(--muted); }
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
.comment-more { color: var(--muted); font-size: 12px; }
.checkout-card {
  align-self: start;
  position: sticky;
  top: 96px;
  padding: 25px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: #fbf7ef;
  box-shadow: var(--shadow-soft);
}
.checkout-card > p { margin: 0; color: var(--muted); font-size: 12px; }
.checkout-card h3 { margin: 8px 0; font-family: var(--font-display); font-size: 24px; }
.checkout-card > span, .checkout-card > small { color: var(--muted); font-size: 11px; }
.quantity-row, .total-row { margin-top: 24px; padding-top: 17px; border-top: 1px solid var(--line); display: flex; justify-content: space-between; align-items: center; }
.total-row strong { color: var(--red); font-size: 26px; }
.primary-action {
  width: 100%;
  height: 48px;
  margin: 22px 0 10px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: white;
  font-weight: 750;
  cursor: pointer;
}
.primary-action:disabled { opacity: .5; cursor: not-allowed; }
@media (max-width: 900px) {
  .event-hero, .purchase-layout, .tier-grid { grid-template-columns: 1fr; }
  .checkout-card { position: static; }
}
</style>
