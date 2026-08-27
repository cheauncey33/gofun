<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'

const route = useRoute()
const router = useRouter()
const loading = ref(true)
const submitting = ref(false)
const step = ref(1)
const event = ref(null)
const profiles = ref([])
const selectedProfileIds = ref([])
const addingProfile = ref(false)
const newProfile = reactive({ name: '', id_number: '' })
const sessionSeats = ref([])
const form = reactive({
  contactName: '',
  contactPhone: '',
  termsAccepted: false,
})

const isWaitlist = computed(() => String(route.query.waitlist || '') === '1')
const quantity = computed(() => {
  const seats = String(route.query.seat_ids || '').split(',').filter(Boolean)
  if (seats.length) return seats.length
  return Math.max(1, Number(route.query.quantity) || 1)
})
const seatIds = computed(() => String(route.query.seat_ids || '').split(',').filter(Boolean))
const selected = computed(() => {
  const tierID = String(route.query.tier || '')
  for (const session of event.value?.sessions || []) {
    const tier = session.ticket_tiers?.find(item => String(item.id) === tierID)
    if (tier) return { session, tier }
  }
  return null
})
const pickedSeats = computed(() =>
  seatIds.value.map(id => sessionSeats.value.find(item => String(item.id) === String(id))).filter(Boolean),
)
const lineItems = computed(() => {
  if (!pickedSeats.value.length) {
    return selected.value ? [{ name: selected.value.tier.name, qty: quantity.value, cents: selected.value.tier.price_cents * quantity.value }] : []
  }
  const map = {}
  for (const seat of pickedSeats.value) {
    const tier = (selected.value?.session.ticket_tiers || []).find(item => String(item.id) === String(seat.ticket_tier_id))
    if (!tier) continue
    if (!map[tier.id]) map[tier.id] = { name: tier.name, qty: 0, cents: 0, price: tier.price_cents }
    map[tier.id].qty += 1
    map[tier.id].cents += tier.price_cents
  }
  return Object.values(map)
})
const totalCents = computed(() => lineItems.value.reduce((sum, item) => sum + item.cents, 0))

onMounted(async () => {
  try {
    const [eventRes, userRes, profileRes] = await Promise.all([
      api.getEventDetail(route.params.eventId),
      api.getUserInfo(),
      api.getUserAttendees().catch(() => ({ data: [] })),
    ])
    event.value = eventRes.data
    api.trackFunnelVisits('checkout', [event.value.id])
    profiles.value = profileRes.data || []
    form.contactName = userRes.data?.username || ''
    form.contactPhone = userRes.data?.phone || ''
    if (!selected.value) throw new Error('所选票档不属于当前活动')
    if (event.value.sale_mode === 'seated' && isWaitlist.value) {
      throw new Error('选座活动暂不支持候补')
    }
    if (event.value.sale_mode === 'seated' && !seatIds.value.length) {
      throw new Error('选座活动必须先选择座位')
    }
    if (seatIds.value.length && selected.value.session?.id) {
      const seatRes = await api.getSessionSeats(event.value.id, selected.value.session.id)
      sessionSeats.value = seatRes.data || []
    }
    if (event.value.real_name_required && profiles.value.length) {
      selectedProfileIds.value = profiles.value.slice(0, quantity.value).map(item => String(item.id))
    }
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || error.message || '购票信息加载失败')
  } finally {
    loading.value = false
  }
})

const money = cents => `¥${(cents / 100).toFixed(2)}`
const dateTime = value => value ? new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit', weekday: 'short',
  hour: '2-digit', minute: '2-digit',
}).format(new Date(value)) : '—'
const maskPhone = value => /^1\d{10}$/.test(value) ? `${value.slice(0, 3)}****${value.slice(-4)}` : value

function toggleProfile(id) {
  const key = String(id)
  if (selectedProfileIds.value.includes(key)) {
    selectedProfileIds.value = selectedProfileIds.value.filter(item => item !== key)
    return
  }
  const limit = seatIds.value.length || event.value.max_tickets_per_order
  if (selectedProfileIds.value.length >= limit) {
    ElMessage.warning(`本场最多选择 ${limit} 位观演人`)
    return
  }
  selectedProfileIds.value = [...selectedProfileIds.value, key]
}

async function bindProfile() {
  addingProfile.value = true
  try {
    const res = await api.createUserAttendee({
      name: newProfile.name,
      id_type: 'id_card',
      id_number: newProfile.id_number,
    })
    profiles.value = [...profiles.value, res.data]
    newProfile.name = ''
    newProfile.id_number = ''
    toggleProfile(res.data.id)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '绑定失败')
  } finally {
    addingProfile.value = false
  }
}

function validate() {
  if (form.contactName.trim().length < 2) return '请填写至少 2 个字符的联系人姓名'
  if (!/^1[3-9]\d{9}$/.test(form.contactPhone.trim())) return '请填写正确的 11 位手机号'
  if (event.value.real_name_required) {
    if (selectedProfileIds.value.length !== quantity.value) {
      return `请选择 ${quantity.value} 位观演人`
    }
  }
  if (!form.termsAccepted) return '请先阅读并同意购票须知'
  return ''
}

function review() {
  const message = validate()
  if (message) {
    ElMessage.warning(message)
    return
  }
  step.value = 2
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

async function submitOrder() {
  if (submitting.value) return
  const message = validate()
  if (message) {
    step.value = 1
    ElMessage.warning(message)
    return
  }
  submitting.value = true
  try {
    const payload = {
      contact_name: form.contactName.trim(),
      contact_phone: form.contactPhone.trim(),
      terms_accepted: form.termsAccepted,
      attendees: [],
      attendee_profile_ids: event.value.real_name_required ? selectedProfileIds.value : [],
      ...(seatIds.value.length ? { seat_ids: seatIds.value } : {}),
    }
    const result = isWaitlist.value
      ? await api.createWaitlist(selected.value.tier.id, quantity.value, payload)
      : await api.createOrder(selected.value.tier.id, quantity.value, payload)
    router.replace(isWaitlist.value
      ? `/waitlist/${result.data.waitlist_id}`
      : `/cashier/${result.data.order_id}`)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || (isWaitlist.value ? '候补提交失败，请检查信息后重试' : '订单提交失败，请检查信息后重试'))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="checkout-page">
    <div v-if="loading" class="page-state">正在准备购票信息…</div>
    <div v-else-if="!event || !selected" class="page-state">
      <strong>无法继续购票</strong>
      <button type="button" @click="router.push(`/events/${route.params.eventId}`)">返回活动详情</button>
    </div>
    <template v-else>
      <nav class="breadcrumb">活动详情 <i>/</i> {{ isWaitlist ? '确认候补' : '确认购票' }}</nav>
      <ol class="steps" aria-label="购票进度">
        <li :class="{ active: step === 1, done: step > 1 }"><b>1</b><span>填写信息</span></li>
        <li :class="{ active: step === 2 }"><b>2</b><span>核对订单</span></li>
        <li><b>3</b><span>支付</span></li>
      </ol>

      <div class="checkout-grid">
        <section class="form-panel">
          <template v-if="step === 1">
            <header class="section-heading">
              <div><h1>{{ isWaitlist ? '候补人信息' : '购票人信息' }}</h1></div>
              <span>带 * 为必填项</span>
            </header>
            <p v-if="isWaitlist" class="waitlist-note">售罄候补：先付沙箱款进入队列。退票按提交顺序派票，配到即出票；开场前仍未配到会退款。不是抢票。</p>

            <div class="contact-grid">
              <label>联系人姓名 *<input v-model.trim="form.contactName" maxlength="64" placeholder="用于订单通知与现场联系" /></label>
              <label>手机号码 *<input v-model.trim="form.contactPhone" maxlength="11" inputmode="numeric" placeholder="请输入 11 位手机号" /></label>
            </div>

            <section v-if="event.real_name_required" class="real-name-block">
              <div class="real-name-note">
                <strong>本场实名制</strong>
                <span>请勾选 {{ quantity }} 位已绑定观演人；一证一场一张。</span>
              </div>
              <button
                v-for="item in profiles"
                :key="item.id"
                type="button"
                class="profile-chip"
                :class="{ selected: selectedProfileIds.includes(String(item.id)) }"
                @click="toggleProfile(item.id)"
              >
                <b>{{ item.name }}</b>
                <span>{{ item.id_number_masked }}</span>
              </button>
              <p v-if="!profiles.length" class="profile-empty">还没有绑定证件，先在下方添加，或去个人中心管理。</p>
              <div class="bind-row">
                <input v-model.trim="newProfile.name" maxlength="64" placeholder="姓名" />
                <input v-model.trim="newProfile.id_number" maxlength="18" placeholder="身份证号" />
                <button type="button" :disabled="addingProfile" @click="bindProfile">绑定并选中</button>
              </div>
            </section>
            <section v-else class="non-real-name-note">
              <b>非实名制活动</b>
              <span>本场无需填写观演人证件信息，电子票可由购票人自行分配。</span>
            </section>

            <section class="notice">
              <h2>购票须知</h2>
              <dl>
                <div><dt>实名规则</dt><dd>{{ event.real_name_required ? '一票一证；观演人与证件信息不一致时可能无法入场。' : '本场不要求实名信息，请妥善保管电子票。' }}</dd></div>
                <div><dt>退票规则</dt><dd>未使用的电子票可申请退款，具体规则以主办方说明为准。</dd></div>
                <div><dt>入场规则</dt><dd>请提前到场并出示有效电子票；同一张票入场后不能再次使用。</dd></div>
              </dl>
              <label class="agreement"><input v-model="form.termsAccepted" type="checkbox" />我已阅读并同意《购票须知》</label>
            </section>

            <footer class="form-actions">
              <button class="text-button" type="button" @click="router.push(`/events/${event.id}`)">返回活动详情</button>
              <button class="primary-button" type="button" @click="review">核对订单</button>
            </footer>
          </template>

          <template v-else>
            <header class="section-heading">
              <div><h1>核对订单</h1></div>
              <button class="edit-button" type="button" @click="step = 1">修改信息</button>
            </header>
            <div class="review-section">
              <h2>联系人</h2>
              <p><b>{{ form.contactName }}</b><span>{{ maskPhone(form.contactPhone) }}</span></p>
            </div>
            <div v-if="event.real_name_required" class="review-section">
              <h2>实名观演人 · {{ selectedProfileIds.length }} 位</h2>
              <p v-for="id in selectedProfileIds" :key="id">
                <b>{{ profiles.find(item => String(item.id) === String(id))?.name }}</b>
                <span>居民身份证 {{ profiles.find(item => String(item.id) === String(id))?.id_number_masked }}</span>
              </p>
            </div>
            <div class="review-warning">
              <b>提交前请再次确认</b>
              <p>提交后进入支付。实名信息确认后不可自行修改。</p>
            </div>
            <footer class="form-actions review-actions">
              <button class="text-button" type="button" @click="step = 1">上一步</button>
              <button class="primary-button" type="button" :disabled="submitting" @click="submitOrder">
                {{ submitting ? '正在提交…' : `提交订单 · ${money(totalCents)}` }}
              </button>
            </footer>
          </template>
        </section>

        <aside class="ticket-summary">
          <span v-if="event.real_name_required" class="real-seal">实名制</span>
          <h2>{{ event.title }}</h2>
          <p>{{ dateTime(selected.session.starts_at) }}</p>
          <p>{{ selected.session.venue?.name }} · {{ selected.session.venue?.address }}</p>
          <div class="tear-line"></div>
          <dl>
            <div><dt>票档</dt><dd>{{ lineItems.map(item => `${item.name}×${item.qty}`).join('、') }}</dd></div>
            <div><dt>数量</dt><dd>{{ quantity }} 张</dd></div>
            <div v-if="pickedSeats.length"><dt>座位</dt><dd>{{ pickedSeats.map(item => item.label).join('、') }}</dd></div>
          </dl>
          <div class="summary-total"><span>应付</span><strong>{{ money(totalCents) }}</strong></div>
          <footer>{{ event.real_name_required ? '入场时须持本人有效身份证件' : '请妥善保管电子票凭证' }}</footer>
        </aside>
      </div>
    </template>
  </main>
</template>

<style scoped>
.checkout-page { max-width: 1420px; min-height: 75vh; margin: 0 auto; padding: 34px 4vw 72px; }
.page-state { min-height: 58vh; display: grid; place-content: center; gap: 18px; color: var(--muted); text-align: center; }
.page-state button { height: 42px; border: 1px solid var(--line-strong); border-radius: var(--radius-pill); background: transparent; cursor: pointer; }
.breadcrumb { color: var(--muted); font-size: 12px; }
.breadcrumb i { margin: 0 10px; color: var(--red); font-style: normal; }
.steps { margin: 34px 0 32px; padding: 0; display: grid; grid-template-columns: repeat(3, 1fr); list-style: none; }
.steps li { display: flex; align-items: center; gap: 11px; color: #8c847c; position: relative; }
.steps li:not(:last-child)::after { content: ''; height: 1px; flex: 1; margin: 0 18px; background: var(--line-strong); }
.steps b { width: 33px; height: 33px; border: 1px solid currentColor; border-radius: 50%; display: grid; place-content: center; font: 500 18px var(--font-display); }
.steps .active, .steps .done { color: var(--red); }
.steps .active b, .steps .done b { background: var(--red); color: white; border-color: var(--red); }
.checkout-grid { display: grid; grid-template-columns: minmax(0, 1fr) 390px; gap: 30px; align-items: start; }
.form-panel {
  min-height: 650px;
  padding: 30px 32px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: rgba(255,255,255,.2);
}
.section-heading { margin-bottom: 24px; display: flex; justify-content: space-between; align-items: end; }
.section-heading small { color: var(--red); font-size: 10px; letter-spacing: .18em; }
.section-heading h1 { margin: 5px 0 0; font: 750 30px var(--font-display); }
.section-heading > span { color: var(--muted); font-size: 11px; }
.contact-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 20px; }
label { color: #49443f; font-size: 12px; }
input, select {
  width: 100%;
  height: 43px;
  margin-top: 8px;
  padding: 0 13px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  outline: none;
  background: rgba(255,255,255,.34);
  color: var(--ink);
}
input:focus, select:focus { border-color: var(--red); box-shadow: 0 0 0 3px rgba(181,52,41,.12); }
.real-name-block { margin-top: 28px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); overflow: hidden; }
.real-name-note { min-height: 45px; padding: 10px 13px; display: flex; align-items: center; gap: 12px; border-bottom: 1px solid var(--line-strong); }
.real-name-note strong { padding: 5px 10px; border-radius: var(--radius-pill); background: var(--red); color: white; font-size: 11px; }
.real-name-note span { color: var(--muted); font-size: 12px; }
.profile-chip {
  width: calc(100% - 24px);
  margin: 10px 12px;
  padding: 12px 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  text-align: left;
  display: grid;
  gap: 4px;
  cursor: pointer;
}
.profile-chip.selected { border-color: var(--red); background: rgba(181,52,41,.06); }
.profile-chip span { color: var(--muted); font-size: 12px; }
.profile-empty { margin: 12px; color: var(--muted); font-size: 13px; }
.bind-row { margin: 12px; display: grid; grid-template-columns: 1fr 1.4fr auto; gap: 8px; }
.bind-row button { height: 43px; padding: 0 12px; border: 0; border-radius: var(--radius-sm); background: var(--red); color: #fff; cursor: pointer; }
.non-real-name-note { margin-top: 28px; padding: 18px; border-left: 3px solid var(--red); border-radius: 0 var(--radius-md) var(--radius-md) 0; background: var(--paper-deep); display: grid; gap: 5px; }
.non-real-name-note span { color: var(--muted); font-size: 12px; }
.waitlist-note { margin: 0 0 18px; padding: 14px 16px; border-left: 3px solid var(--red); background: var(--paper-deep); color: var(--muted); font-size: 12px; line-height: 1.7; }
.notice { margin-top: 28px; padding-top: 20px; border-top: 1px dashed var(--line-strong); }
.notice h2, .review-section h2 { margin: 0 0 14px; font: 700 19px var(--font-display); }
.notice dl { margin: 0; }
.notice dl div { display: grid; grid-template-columns: 76px 1fr; padding: 6px 0; font-size: 12px; line-height: 1.6; }
.notice dt { color: var(--ink); }
.notice dd { margin: 0; color: var(--muted); }
.agreement { margin-top: 14px; display: flex; align-items: center; gap: 9px; cursor: pointer; }
.agreement input { width: 17px; height: 17px; margin: 0; accent-color: var(--red); }
.form-actions { margin-top: 24px; display: flex; justify-content: space-between; align-items: center; }
.text-button, .edit-button { border: 0; border-bottom: 1px solid var(--ink); padding: 7px 1px; background: transparent; cursor: pointer; }
.primary-button {
  min-width: 270px;
  height: 50px;
  border: 1px solid var(--red);
  border-radius: var(--radius-pill);
  background: var(--red);
  color: white;
  font-weight: 750;
  cursor: pointer;
}
.primary-button:disabled { opacity: .55; cursor: wait; }
.edit-button { color: var(--red); border-color: var(--red); }
.review-section { margin-top: 18px; padding: 20px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); }
.review-section p { margin: 0; min-height: 43px; display: flex; align-items: center; gap: 18px; border-bottom: 1px solid var(--line); }
.review-section p:last-child { border: 0; }
.review-section p i { color: var(--red); font: 20px var(--font-display); }
.review-section p b { min-width: 90px; }
.review-section p span { color: var(--muted); font-size: 12px; }
.review-warning { margin-top: 22px; padding: 18px; background: #eee4d7; border-left: 3px solid #b87521; border-radius: 0 var(--radius-md) var(--radius-md) 0; }
.review-warning p { margin: 7px 0 0; color: var(--muted); font-size: 12px; line-height: 1.7; }
.ticket-summary {
  position: sticky;
  top: 92px;
  min-height: 590px;
  padding: 34px 28px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: #fbf7ef;
  box-shadow: var(--shadow-soft);
  overflow: hidden;
}
.ticket-summary::before, .ticket-summary::after { content: ''; position: absolute; top: 280px; width: 25px; height: 25px; border: 1px solid var(--line-strong); border-radius: 50%; background: var(--paper); }
.ticket-summary::before { left: -14px; }.ticket-summary::after { right: -14px; }
.ticket-summary > small { color: var(--red); letter-spacing: .16em; }
.ticket-summary h2 { margin: 28px 0 22px; font: 750 30px/1.35 var(--font-display); }
.ticket-summary > p { margin: 9px 0; color: #4f4943; font-size: 12px; line-height: 1.6; }
.real-seal {
  position: absolute;
  top: 24px;
  right: 24px;
  padding: 7px 10px;
  border: 2px double var(--red);
  border-radius: var(--radius-sm);
  color: var(--red);
  font: 700 16px var(--font-display);
  transform: rotate(-2deg);
}
.tear-line { margin: 34px 0 25px; border-top: 1px dashed var(--line-strong); }
.ticket-summary dl { display: grid; gap: 18px; }
.ticket-summary dl div { display: flex; justify-content: space-between; }
.ticket-summary dd { margin: 0; }
.summary-total { margin-top: 28px; padding-top: 25px; border-top: 1px dashed var(--line-strong); display: flex; justify-content: space-between; align-items: end; }
.summary-total span { font: 700 20px var(--font-display); }
.summary-total strong { color: var(--red); font: 750 36px var(--font-display); }
.ticket-summary footer { margin-top: 76px; color: var(--muted); font-size: 11px; }
@media (max-width: 900px) {
  .checkout-page { padding-inline: 18px; }
  .checkout-grid { grid-template-columns: 1fr; }
  .ticket-summary { position: static; min-height: auto; order: -1; }
  .ticket-summary footer { margin-top: 32px; }
}
@media (max-width: 600px) {
  .steps li:not(:last-child)::after { margin: 0 7px; }
  .steps span { display: none; }
  .form-panel { padding: 22px 16px; }
  .contact-grid, .attendee-fields { grid-template-columns: 1fr; }
  .attendee-row { grid-template-columns: 65px 1fr; }
  .attendee-fields .id-field { grid-column: auto; }
  .form-actions { gap: 15px; }
  .primary-button { min-width: 0; flex: 1; }
}
</style>
