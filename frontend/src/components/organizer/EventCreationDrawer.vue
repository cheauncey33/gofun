<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  organizerId: { type: [String, Number], required: true },
  venues: { type: Array, default: () => [] },
  draftEvent: { type: Object, default: null },
})

const emit = defineEmits(['update:modelValue', 'completed'])

const step = ref(0)
const submitting = ref(false)
const createdEventId = ref('')
const createdSessionId = ref('')
const createdTierIndexes = ref(new Set())

const eventForm = reactive({
  title: '',
  subtitle: '',
  category: '',
  cover_url: '',
  description: '',
  max_tickets_per_order: 6,
  real_name_required: false,
})

const sessionForm = reactive({
  venue_id: '',
  starts_at: null,
  ends_at: null,
  sale_starts_at: null,
  sale_ends_at: null,
})

const tiers = ref([newTier()])

const drawerTitle = computed(() => props.draftEvent ? '继续配置活动' : '创建活动')
const primaryLabel = computed(() => {
  if (submitting.value) return '正在提交'
  return step.value === 3 ? '发布活动' : '下一步'
})

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) resetFromDraft()
  },
)

function newTier() {
  return {
    name: '',
    description: '',
    price_yuan: 188,
    total_quota: 100,
    purchase_limit: 4,
  }
}

function resetFromDraft() {
  createdTierIndexes.value = new Set()
  if (!props.draftEvent) {
    step.value = 0
    createdEventId.value = ''
    createdSessionId.value = ''
    Object.assign(eventForm, {
      title: '',
      subtitle: '',
      category: '',
      cover_url: '',
      description: '',
      max_tickets_per_order: 6,
      real_name_required: false,
    })
    Object.assign(sessionForm, {
      venue_id: props.venues[0]?.id || '',
      starts_at: null,
      ends_at: null,
      sale_starts_at: new Date(),
      sale_ends_at: null,
    })
    tiers.value = [newTier()]
    return
  }

  const draft = props.draftEvent
  createdEventId.value = draft.id
  Object.assign(eventForm, {
    title: draft.title,
    subtitle: draft.subtitle,
    category: draft.category,
    cover_url: draft.cover_url || '',
    description: draft.description,
    max_tickets_per_order: draft.max_tickets_per_order,
    real_name_required: draft.real_name_required,
  })
  const existingSession = draft.sessions?.[0]
  if (!existingSession) {
    step.value = 1
    createdSessionId.value = ''
    Object.assign(sessionForm, {
      venue_id: props.venues[0]?.id || '',
      starts_at: null,
      ends_at: null,
      sale_starts_at: new Date(),
      sale_ends_at: null,
    })
    tiers.value = [newTier()]
    return
  }

  createdSessionId.value = existingSession.id
  Object.assign(sessionForm, {
    venue_id: existingSession.venue_id,
    starts_at: new Date(existingSession.starts_at),
    ends_at: new Date(existingSession.ends_at),
    sale_starts_at: new Date(existingSession.sale_starts_at),
    sale_ends_at: new Date(existingSession.sale_ends_at),
  })
  if (!existingSession.ticket_tiers?.length) {
    step.value = 2
    tiers.value = [newTier()]
    return
  }
  step.value = 3
  tiers.value = existingSession.ticket_tiers.map(tier => ({
    name: tier.name,
    description: tier.description,
    price_yuan: tier.price_cents / 100,
    total_quota: tier.total_quota,
    purchase_limit: tier.purchase_limit,
  }))
}

function close() {
  emit('update:modelValue', false)
}

function addTier() {
  tiers.value.push(newTier())
}

function removeTier(index) {
  if (tiers.value.length > 1) tiers.value.splice(index, 1)
}

function validEvent() {
  return eventForm.title.trim() && eventForm.category.trim() && eventForm.description.trim()
}

function validSession() {
  if (!sessionForm.venue_id || !sessionForm.starts_at || !sessionForm.ends_at ||
      !sessionForm.sale_starts_at || !sessionForm.sale_ends_at) return false
  const start = new Date(sessionForm.starts_at)
  return start < new Date(sessionForm.ends_at) &&
    new Date(sessionForm.sale_starts_at) < new Date(sessionForm.sale_ends_at) &&
    new Date(sessionForm.sale_ends_at) <= start
}

function validTiers() {
  return tiers.value.length > 0 && tiers.value.every(tier =>
    tier.name.trim() && tier.price_yuan > 0 && tier.total_quota > 0 &&
    tier.purchase_limit > 0 && tier.purchase_limit <= tier.total_quota
  )
}

async function next() {
  if (submitting.value) return
  submitting.value = true
  try {
    if (step.value === 0) {
      if (!validEvent()) throw new Error('请完整填写活动名称、分类和活动介绍')
      const res = await api.organizerCreateEvent(props.organizerId, eventForm)
      createdEventId.value = res.data.id
      step.value = 1
      return
    }
    if (step.value === 1) {
      if (!validSession()) throw new Error('请检查场次和售票时间，售票结束不能晚于开场时间')
      const payload = {
        venue_id: String(sessionForm.venue_id),
        starts_at: new Date(sessionForm.starts_at).toISOString(),
        ends_at: new Date(sessionForm.ends_at).toISOString(),
        sale_starts_at: new Date(sessionForm.sale_starts_at).toISOString(),
        sale_ends_at: new Date(sessionForm.sale_ends_at).toISOString(),
      }
      const res = await api.organizerCreateSession(
        props.organizerId,
        createdEventId.value,
        payload,
      )
      createdSessionId.value = res.data.id
      step.value = 2
      return
    }
    if (step.value === 2) {
      if (!validTiers()) throw new Error('每个票档都需要名称、价格、票额和有效限购数')
      for (let index = 0; index < tiers.value.length; index += 1) {
        if (createdTierIndexes.value.has(index)) continue
        const tier = tiers.value[index]
        await api.organizerCreateTicketTier(props.organizerId, createdSessionId.value, {
          name: tier.name,
          description: tier.description,
          price_cents: Math.round(Number(tier.price_yuan) * 100),
          total_quota: Number(tier.total_quota),
          purchase_limit: Number(tier.purchase_limit),
        })
        createdTierIndexes.value.add(index)
      }
      step.value = 3
      return
    }

    await api.organizerPublishEvent(props.organizerId, createdEventId.value)
    ElMessage.success('活动已发布，购票站现在可以看到它')
    emit('completed')
    close()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || error.message || '提交失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    :title="drawerTitle"
    size="min(560px, 100%)"
    class="event-creation-drawer"
    destroy-on-close
    @close="close"
  >
    <el-steps :active="step" finish-status="success" class="creation-steps">
      <el-step title="基础信息" />
      <el-step title="场次场馆" />
      <el-step title="票档" />
      <el-step title="发布" />
    </el-steps>

    <section v-if="step === 0" class="drawer-section">
      <label>活动名称 <b>*</b><el-input v-model="eventForm.title" maxlength="160" show-word-limit /></label>
      <label>副标题<el-input v-model="eventForm.subtitle" maxlength="256" /></label>
      <label>活动分类 <b>*</b>
        <el-select v-model="eventForm.category" placeholder="请选择分类">
          <el-option v-for="item in ['音乐现场', '演唱会', '音乐节', '脱口秀', '展览', '戏剧', '体育']" :key="item" :label="item" :value="item" />
        </el-select>
      </label>
      <label>
        封面图 URL
        <el-input
          v-model="eventForm.cover_url"
          maxlength="512"
          placeholder="https://... 海报图地址，首页卡片与详情页会展示"
        />
      </label>
      <label>活动介绍 <b>*</b><el-input v-model="eventForm.description" type="textarea" :rows="5" maxlength="1200" show-word-limit /></label>
      <div class="paired-fields">
        <label>单笔限购<el-input-number v-model="eventForm.max_tickets_per_order" :min="1" :max="20" /></label>
        <label class="switch-field">实名制<el-switch v-model="eventForm.real_name_required" /></label>
      </div>
    </section>

    <section v-else-if="step === 1" class="drawer-section">
      <label>场馆 <b>*</b>
        <el-select v-model="sessionForm.venue_id" placeholder="请选择已创建的场馆">
          <el-option v-for="venue in venues" :key="venue.id" :label="`${venue.name} · ${venue.city}`" :value="venue.id" />
        </el-select>
      </label>
      <label>开始时间 <b>*</b><el-date-picker v-model="sessionForm.starts_at" type="datetime" placeholder="选择开场时间" /></label>
      <label>结束时间 <b>*</b><el-date-picker v-model="sessionForm.ends_at" type="datetime" placeholder="选择结束时间" /></label>
      <div class="paired-fields">
        <label>开售时间 <b>*</b><el-date-picker v-model="sessionForm.sale_starts_at" type="datetime" placeholder="选择开售时间" /></label>
        <label>停售时间 <b>*</b><el-date-picker v-model="sessionForm.sale_ends_at" type="datetime" placeholder="选择停售时间" /></label>
      </div>
      <p class="drawer-hint">售票结束时间必须早于或等于开场时间。第一版每场活动先配置一个场次。</p>
    </section>

    <section v-else-if="step === 2" class="drawer-section tier-section">
      <article v-for="(tier, index) in tiers" :key="index" class="tier-editor">
        <header><strong>票档 {{ index + 1 }}</strong><button type="button" @click="removeTier(index)">移除</button></header>
        <label>票档名称 <b>*</b><el-input v-model="tier.name" placeholder="例如：普通区" /></label>
        <label>说明<el-input v-model="tier.description" placeholder="例如：自由入场，按到场顺序入座" /></label>
        <div class="tier-numbers">
          <label>价格（元）<el-input-number v-model="tier.price_yuan" :min="0.01" :precision="2" /></label>
          <label>总票额<el-input-number v-model="tier.total_quota" :min="1" /></label>
          <label>每人限购<el-input-number v-model="tier.purchase_limit" :min="1" /></label>
        </div>
      </article>
      <button class="add-tier" type="button" @click="addTier">＋ 添加票档</button>
    </section>

    <section v-else class="publish-check">
      <span>活动</span><strong>{{ eventForm.title }}</strong>
      <span>场馆</span><strong>{{ venues.find(item => String(item.id) === String(sessionForm.venue_id))?.name }}</strong>
      <span>票档</span><strong>{{ tiers.map(item => item.name).join('、') }}</strong>
      <p>发布时后端会再次检查主办方状态、场次和票档，并把剩余票额预热到 Redis。发布成功后，活动立即进入购票站。</p>
    </section>

    <template #footer>
      <div class="drawer-actions">
        <el-button @click="close">稍后继续</el-button>
        <el-button v-if="step > 0 && !createdEventId" @click="step -= 1">上一步</el-button>
        <el-button type="primary" :loading="submitting" @click="next">{{ primaryLabel }}</el-button>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.creation-steps { margin: 4px 0 34px; }
.drawer-section { display: grid; gap: 22px; }
.drawer-section label { display: grid; gap: 8px; color: var(--ink); font-size: 13px; font-weight: 650; }
.drawer-section label b { color: var(--red); }
.drawer-section :deep(.el-select), .drawer-section :deep(.el-date-editor) { width: 100%; }
.paired-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.switch-field { align-content: start; }
.drawer-hint, .publish-check p { color: var(--muted); font-size: 12px; line-height: 1.7; }
.tier-section { gap: 15px; }
.tier-editor { padding: 18px; border: 1px solid var(--line); border-radius: var(--radius-md); background: rgba(255,255,255,.28); display: grid; gap: 14px; }
.tier-editor header { display: flex; justify-content: space-between; }
.tier-editor header button, .add-tier { border: 0; background: transparent; color: var(--red); cursor: pointer; }
.tier-numbers { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.tier-numbers :deep(.el-input-number) { width: 100%; }
.add-tier { min-height: 42px; border: 1px dashed var(--line-strong); border-radius: var(--radius-sm); }
.publish-check { padding: 24px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); display: grid; grid-template-columns: 80px 1fr; gap: 14px; }
.publish-check span { color: var(--muted); }
.publish-check p { grid-column: 1 / -1; margin: 14px 0 0; padding-top: 18px; border-top: 1px solid var(--line); }
.drawer-actions { display: flex; justify-content: flex-end; }
@media (max-width: 600px) {
  .paired-fields, .tier-numbers { grid-template-columns: 1fr; }
}
</style>
