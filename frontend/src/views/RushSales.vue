<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'

const router = useRouter()
const sales = ref([])
const loading = ref(true)
const acting = ref(null)
const activeSale = ref(null)
const userHint = reactive({ contactName: '', contactPhone: '' })
const form = reactive({
  contactName: '',
  contactPhone: '',
  termsAccepted: false,
  attendees: [],
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
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '限时开售加载失败')
  } finally {
    loading.value = false
  }
})

const dateTime = value => new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
}).format(new Date(value))

function openRush(sale) {
  if (!localStorage.getItem('access_token') && !localStorage.getItem('token')) {
    router.push('/login?redirect=/rush-sales')
    return
  }
  activeSale.value = sale
  form.contactName = userHint.contactName
  form.contactPhone = userHint.contactPhone
  form.termsAccepted = false
  form.attendees = sale.real_name_required
    ? [{ name: '', id_type: 'id_card', id_number: '' }]
    : []
}

function closeRush() {
  if (acting.value) return
  activeSale.value = null
}

function validate() {
  if (form.contactName.trim().length < 2) return '请填写至少 2 个字符的联系人姓名'
  if (!/^1[3-9]\d{9}$/.test(form.contactPhone.trim())) return '请填写正确的 11 位手机号'
  if (activeSale.value?.real_name_required) {
    const attendee = form.attendees[0]
    if (!attendee || attendee.name.trim().length < 2) return '请填写观演人姓名'
    if (!/^\d{17}[\dXx]$/.test(attendee.id_number.trim())) return '观演人身份证格式不正确'
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
    const tokenRes = await api.getRushSaleToken(sale.id)
    const orderRes = await api.executeRushSale(sale.id, tokenRes.data.token, 1, {
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
    ElMessage.success('抢票请求已进入队列')
    activeSale.value = null
    router.push(`/orders/${orderRes.data.order_id}`)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '本次抢票未成功')
  } finally {
    acting.value = null
  }
}
</script>

<template>
  <div class="rush-page">
    <header>
      <p>RUSH SALE</p>
      <h1>限时开售</h1>
      <span>先确认购票信息，再领取令牌并原子扣减票额。</span>
    </header>
    <div v-if="loading" class="rush-state">正在同步开售信息…</div>
    <div v-else-if="!sales.length" class="rush-state">目前没有进行中的限时开售</div>
    <div v-else class="sale-list">
      <article v-for="sale in sales" :key="sale.id">
        <div class="sale-mark">赴<br />场</div>
        <div class="sale-copy">
          <small>{{ dateTime(sale.starts_at) }} 开售</small>
          <h2>{{ sale.name }}</h2>
          <p>
            {{ sale.event_title || '活动票档' }}
            · 每人限 {{ sale.per_user_limit }} 张
            · 剩余 {{ sale.remaining_quota }} / {{ sale.total_quota }} 张
          </p>
        </div>
        <strong>¥{{ (sale.rush_price_cents / 100).toFixed(0) }}</strong>
        <button type="button" @click="openRush(sale)">立即抢票</button>
      </article>
    </div>

    <div v-if="activeSale" class="rush-modal" @click.self="closeRush">
      <section class="rush-panel">
        <header>
          <p>确认购票信息</p>
          <h2>{{ activeSale.name }}</h2>
          <span>¥{{ (activeSale.rush_price_cents / 100).toFixed(2) }} · 1 张</span>
        </header>
        <label>联系人姓名 *<input v-model.trim="form.contactName" type="text" maxlength="64" /></label>
        <label>联系人手机 *<input v-model.trim="form.contactPhone" type="tel" maxlength="11" /></label>
        <template v-if="activeSale.real_name_required">
          <label>观演人姓名 *<input v-model.trim="form.attendees[0].name" type="text" maxlength="64" /></label>
          <label>身份证号 *<input v-model.trim="form.attendees[0].id_number" type="text" maxlength="18" /></label>
        </template>
        <label class="terms">
          <input v-model="form.termsAccepted" type="checkbox" />
          <span>我已阅读并同意购票须知与退改规则</span>
        </label>
        <div class="actions">
          <button type="button" class="ghost" :disabled="!!acting" @click="closeRush">取消</button>
          <button type="button" :disabled="acting === activeSale.id" @click="confirmRush">
            {{ acting === activeSale.id ? '提交中…' : '确认抢票' }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.rush-page { max-width: 1120px; min-height: 70vh; margin: 0 auto; padding: 55px 30px; }
header { margin-bottom: 40px; }
header p { color: var(--red); font-size: 11px; letter-spacing: .2em; }
header h1 { margin: 6px 0; font-family: var(--font-display); font-size: 44px; }
header span { color: var(--muted); }
.rush-state { min-height: 300px; border: 1px dashed var(--line-strong); border-radius: var(--radius-lg); display: grid; place-content: center; color: var(--muted); }
.sale-list { display: grid; gap: 15px; }
article { min-height: 135px; padding: 20px; border: 1px solid rgba(181,52,41,.55); border-radius: var(--radius-md); display: grid; grid-template-columns: 72px 1fr 100px 120px; gap: 24px; align-items: center; }
.sale-mark { width: 66px; height: 82px; border-radius: var(--radius-sm); background: var(--red); color: white; display: grid; place-content: center; text-align: center; font: 700 22px var(--font-display); }
.sale-copy small, .sale-copy p { color: var(--muted); }
.sale-copy h2 { margin: 8px 0; font: 700 23px var(--font-display); }
article > strong { color: var(--red); font-size: 27px; }
article button, .actions button { height: 44px; border: 0; border-radius: var(--radius-pill); background: var(--red); color: white; font-weight: 700; cursor: pointer; }
.rush-modal { position: fixed; inset: 0; z-index: 40; background: rgba(20, 16, 14, .42); display: grid; place-items: center; padding: 20px; }
.rush-panel { width: min(480px, 100%); padding: 24px; border: 1px solid var(--line-strong); border-radius: var(--radius-lg); background: var(--paper, #fff7f0); box-shadow: var(--shadow-lift); display: grid; gap: 12px; }
.rush-panel header { margin: 0 0 8px; }
.rush-panel header h2 { margin: 4px 0; font: 700 28px var(--font-display); }
.rush-panel label { display: grid; gap: 6px; color: var(--muted); font-size: 13px; }
.rush-panel input[type="text"], .rush-panel input:not([type]), .rush-panel input[type="tel"] {
  height: 42px; padding: 0 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: white; color: var(--ink); font: 15px var(--font-body, sans-serif);
}
.terms { grid-template-columns: 18px 1fr; align-items: start; gap: 10px; color: var(--ink); }
.terms input { width: 18px; height: 18px; margin-top: 2px; }
.actions { display: grid; grid-template-columns: 1fr 1.4fr; gap: 10px; margin-top: 8px; }
.actions .ghost { background: transparent; border: 1px solid var(--line-strong); border-radius: var(--radius-pill); color: var(--ink); }
@media (max-width: 680px) {
  article { grid-template-columns: 65px 1fr; }
  article > strong, article button { grid-column: 2; }
}
</style>
