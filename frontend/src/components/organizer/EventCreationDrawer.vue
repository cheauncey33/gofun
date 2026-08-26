<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'
import CoverUpload from '../CoverUpload.vue'
import SeatLayoutEditor from './SeatLayoutEditor.vue'

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
const sessions = ref([])
const layoutRows = ref(8)
const layoutCols = ref(12)
const paintTierId = ref('')
const layoutCells = ref({})

const eventForm = reactive({
  title: '',
  subtitle: '',
  category: '',
  cover_url: '',
  description: '',
  max_tickets_per_order: 6,
  real_name_required: false,
  sale_mode: 'counter',
})

const isSeated = computed(() => eventForm.sale_mode === 'seated')
const isExhibition = computed(() => eventForm.category === '展览')
const publishStep = computed(() => isSeated.value ? 4 : 3)
const drawerTitle = computed(() => props.draftEvent ? '继续配置活动' : '创建活动')
const primaryLabel = computed(() => {
  if (submitting.value) return '正在提交'
  return step.value === publishStep.value ? '发布活动' : '下一步'
})
const createdTiers = computed(() =>
  sessions.value.flatMap(session => (session.tiers || []).filter(tier => tier.id).map(tier => ({
    id: tier.id,
    name: tier.name,
  }))),
)
const layoutSeatCount = computed(() => Object.keys(layoutCells.value).length)

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) resetFromDraft()
  },
)

watch(
  () => eventForm.category,
  (category) => {
    if (category === '展览') {
      eventForm.sale_mode = 'counter'
      eventForm.real_name_required = false
      for (const session of sessions.value) {
        for (const tier of session.tiers) tier.assign_place_no = false
      }
    }
  },
)

watch(isSeated, (seated) => {
  if (seated && sessions.value.length > 1) sessions.value = [sessions.value[0]]
})

function newTier() {
  return {
    id: '',
    name: '',
    description: '',
    price_yuan: 188,
    total_quota: 100,
    assign_place_no: true,
  }
}

function emptySession() {
  return {
    id: '',
    venue_id: props.venues[0]?.id || '',
    starts_at: null,
    ends_at: null,
    sale_starts_at: new Date(),
    sale_ends_at: null,
    tiers: [newTier()],
  }
}

function mapDraftSession(session) {
  return {
    id: session.id,
    venue_id: session.venue_id,
    starts_at: session.starts_at ? new Date(session.starts_at) : null,
    ends_at: session.ends_at ? new Date(session.ends_at) : null,
    sale_starts_at: session.sale_starts_at ? new Date(session.sale_starts_at) : null,
    sale_ends_at: session.sale_ends_at ? new Date(session.sale_ends_at) : null,
    tiers: session.ticket_tiers?.length
      ? session.ticket_tiers.map(tier => ({
          id: tier.id,
          name: tier.name,
          description: tier.description || '',
          price_yuan: Number(tier.price_cents || 0) / 100,
          total_quota: tier.total_quota || 1,
          assign_place_no: tier.assign_place_no !== false,
        }))
      : [newTier()],
  }
}

function cellKey(row, col) {
  return `${row}:${col}`
}

function rowLabel(row) {
  let n = row
  let label = ''
  while (n > 0) {
    n -= 1
    label = String.fromCharCode(65 + (n % 26)) + label
    n = Math.floor(n / 26)
  }
  return label || 'A'
}

function resetLayout(rows = 8, cols = 12) {
  layoutRows.value = rows
  layoutCols.value = cols
  layoutCells.value = {}
  paintTierId.value = String(createdTiers.value[0]?.id || '')
}

async function loadLayout() {
  if (!createdEventId.value || !isSeated.value) return
  try {
    const res = await api.organizerGetSeatLayout(props.organizerId, createdEventId.value)
    const layout = res.data
    layoutRows.value = layout.row_count || 8
    layoutCols.value = layout.col_count || 12
    const cells = {}
    for (const seat of layout.seats || []) {
      cells[cellKey(seat.row_no, seat.col_no)] = String(seat.ticket_tier_id)
    }
    layoutCells.value = cells
    paintTierId.value = String(createdTiers.value[0]?.id || '')
  } catch {
    resetLayout()
  }
}

function resetFromDraft() {
  resetLayout()
  if (!props.draftEvent) {
    step.value = 0
    createdEventId.value = ''
    Object.assign(eventForm, {
      title: '',
      subtitle: '',
      category: '',
      cover_url: '',
      description: '',
      max_tickets_per_order: 6,
      real_name_required: false,
      sale_mode: 'counter',
    })
    sessions.value = [emptySession()]
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
    sale_mode: draft.sale_mode || 'counter',
  })
  const existing = draft.sessions || []
  if (!existing.length) {
    step.value = 1
    sessions.value = [emptySession()]
    return
  }
  sessions.value = existing.map(mapDraftSession)
  if (existing.some(item => !item.ticket_tiers?.length)) {
    step.value = 2
    return
  }
  step.value = eventForm.sale_mode === 'seated' ? 3 : 3
  if (eventForm.sale_mode === 'seated') loadLayout()
}

function close() {
  emit('update:modelValue', false)
}

function addSession() {
  if (isSeated.value) return
  sessions.value.push(emptySession())
}

function removeSession(index) {
  if (sessions.value.length > 1 && !sessions.value[index].id) sessions.value.splice(index, 1)
}

function addTier(session) {
  session.tiers.push(newTier())
}

function removeTier(session, index) {
  if (session.tiers.length > 1 && !session.tiers[index].id) session.tiers.splice(index, 1)
}

function validEvent() {
  return eventForm.title.trim() && eventForm.category.trim() && eventForm.description.trim()
}

function validSession(session) {
  if (!session.venue_id || !session.starts_at || !session.ends_at ||
      !session.sale_starts_at || !session.sale_ends_at) return false
  const start = new Date(session.starts_at)
  return start < new Date(session.ends_at) &&
    new Date(session.sale_starts_at) < new Date(session.sale_ends_at) &&
    new Date(session.sale_ends_at) <= start
}

function validTiers() {
  return sessions.value.length > 0 && sessions.value.every(session =>
    session.tiers.length > 0 && session.tiers.every(tier =>
      tier.name.trim() && tier.price_yuan > 0 && (isSeated.value || tier.total_quota > 0),
    ),
  )
}

function sessionPayload(session) {
  return {
    venue_id: String(session.venue_id),
    starts_at: new Date(session.starts_at).toISOString(),
    ends_at: new Date(session.ends_at).toISOString(),
    sale_starts_at: new Date(session.sale_starts_at).toISOString(),
    sale_ends_at: new Date(session.sale_ends_at).toISOString(),
  }
}

async function persistSessions() {
  if (!sessions.value.length) throw new Error('至少需要一个场次')
  if (!sessions.value.every(validSession)) {
    throw new Error('请检查场次和售票时间，售票结束不能晚于开场时间')
  }
  for (const session of sessions.value) {
    const payload = sessionPayload(session)
    if (session.id) {
      await api.organizerUpdateSession(props.organizerId, session.id, payload)
    } else {
      const res = await api.organizerCreateSession(props.organizerId, createdEventId.value, payload)
      session.id = res.data.id
    }
  }
}

async function persistTiers() {
  if (!validTiers()) {
    throw new Error(isSeated.value
      ? '每个票档都需要名称和价格'
      : '每个票档都需要名称、价格和票额')
  }
  for (const session of sessions.value) {
    for (const tier of session.tiers) {
      const payload = {
        name: tier.name,
        description: tier.description,
        price_cents: Math.round(Number(tier.price_yuan) * 100),
        purchase_limit: Number(eventForm.max_tickets_per_order),
        assign_place_no: isSeated.value || isExhibition.value ? false : !!tier.assign_place_no,
      }
      if (!isSeated.value) payload.total_quota = Number(tier.total_quota)
      if (tier.id) {
        await api.organizerUpdateTicketTier(props.organizerId, tier.id, payload)
      } else {
        const res = await api.organizerCreateTicketTier(props.organizerId, session.id, payload)
        tier.id = res.data.id
      }
    }
  }
  paintTierId.value = String(createdTiers.value[0]?.id || '')
}

async function next() {
  if (submitting.value) return
  submitting.value = true
  try {
    if (step.value === 0) {
      if (!validEvent()) throw new Error('请完整填写活动名称、分类和活动介绍')
      if (createdEventId.value) {
        await api.organizerUpdateEvent(props.organizerId, createdEventId.value, eventForm)
      } else {
        const res = await api.organizerCreateEvent(props.organizerId, eventForm)
        createdEventId.value = res.data.id
      }
      step.value = 1
      return
    }
    if (step.value === 1) {
      await persistSessions()
      step.value = 2
      return
    }
    if (step.value === 2) {
      await persistTiers()
      step.value = 3
      return
    }
    if (isSeated.value && step.value === 3) {
      if (layoutSeatCount.value < 1) throw new Error('请至少涂一个可售座位')
      await api.organizerSaveSeatLayout(props.organizerId, createdEventId.value, {
        name: '主厅',
        row_count: Number(layoutRows.value),
        col_count: Number(layoutCols.value),
        seats: Object.entries(layoutCells.value).map(([key, tierId]) => {
          const [row, col] = key.split(':').map(Number)
          return {
            row_no: row,
            col_no: col,
            ticket_tier_id: String(tierId),
            label: `${rowLabel(row)}${col}`,
          }
        }),
      })
      step.value = 4
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

function venueName(id) {
  return props.venues.find(item => String(item.id) === String(id))?.name || '场馆待定'
}

function sessionTime(session) {
  if (!session.starts_at) return '未定时'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(session.starts_at))
}
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    :title="drawerTitle"
    :size="isSeated && step === 3 ? 'min(760px, 100%)' : 'min(560px, 100%)'"
    class="event-creation-drawer"
    destroy-on-close
    @close="close"
  >
    <el-steps :active="step" finish-status="success" class="creation-steps">
      <el-step title="基础信息" />
      <el-step title="场次场馆" />
      <el-step title="票档" />
      <el-step v-if="isSeated" title="厅图" />
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
      <CoverUpload v-model="eventForm.cover_url" />
      <label>活动介绍 <b>*</b><el-input v-model="eventForm.description" type="textarea" :rows="5" maxlength="1200" show-word-limit /></label>
      <div class="paired-fields">
        <label>每账号限购<el-input-number v-model="eventForm.max_tickets_per_order" :min="1" :max="20" /></label>
        <label class="switch-field">实名制<el-switch v-model="eventForm.real_name_required" /></label>
      </div>
      <label>售卖方式 <b>*</b>
        <el-select v-model="eventForm.sale_mode">
          <el-option label="计数售卖：展览 / 演唱会分区" value="counter" />
          <el-option label="必须选座：电影 / 脱口秀" value="seated" :disabled="isExhibition" />
        </el-select>
      </label>
      <p class="drawer-hint">限购按账号累计本场已买张数（含待支付）。实名制购票时勾选已绑定证件，一证一场一张。</p>
      <p class="drawer-hint">计数可配多场次；选座共用一张厅图，目前只支持一场。发布后不能改卖法。</p>
    </section>

    <section v-else-if="step === 1" class="drawer-section">
      <article v-for="(session, index) in sessions" :key="session.id || `new-${index}`" class="session-editor">
        <header>
          <strong>场次 {{ index + 1 }}</strong>
          <button v-if="sessions.length > 1 && !session.id" type="button" @click="removeSession(index)">移除</button>
        </header>
        <label>场馆 <b>*</b>
          <el-select v-model="session.venue_id" placeholder="请选择已创建的场馆">
            <el-option v-for="venue in venues" :key="venue.id" :label="`${venue.name} · ${venue.city}`" :value="venue.id" />
          </el-select>
        </label>
        <label>开始时间 <b>*</b><el-date-picker v-model="session.starts_at" type="datetime" placeholder="选择开场时间" /></label>
        <label>结束时间 <b>*</b><el-date-picker v-model="session.ends_at" type="datetime" placeholder="选择结束时间" /></label>
        <div class="paired-fields">
          <label>开售时间 <b>*</b><el-date-picker v-model="session.sale_starts_at" type="datetime" placeholder="选择开售时间" /></label>
          <label>停售时间 <b>*</b><el-date-picker v-model="session.sale_ends_at" type="datetime" placeholder="选择停售时间" /></label>
        </div>
      </article>
      <button v-if="!isSeated" class="add-tier" type="button" @click="addSession">＋ 添加场次</button>
      <p class="drawer-hint">售票结束时间必须早于或等于开场时间。{{ isSeated ? '选座活动目前只支持一场。' : '多场次各自独立售票和库存。' }}</p>
    </section>

    <section v-else-if="step === 2" class="drawer-section tier-section">
      <div v-for="(session, sessionIndex) in sessions" :key="session.id || sessionIndex">
        <p class="session-label">场次 {{ sessionIndex + 1 }} · {{ venueName(session.venue_id) }} · {{ sessionTime(session) }}</p>
        <article v-for="(tier, index) in session.tiers" :key="tier.id || index" class="tier-editor">
          <header>
            <strong>票档 {{ index + 1 }}</strong>
            <button v-if="session.tiers.length > 1 && !tier.id" type="button" @click="removeTier(session, index)">移除</button>
          </header>
          <label>票档名称 <b>*</b><el-input v-model="tier.name" :placeholder="isSeated ? '例如：普通座' : '例如：普通区'" /></label>
          <label>说明<el-input v-model="tier.description" placeholder="价区说明" /></label>
          <div class="tier-numbers" :class="{ seated: isSeated || isExhibition }">
            <label>价格（元）<el-input-number v-model="tier.price_yuan" :min="0.01" :precision="2" /></label>
            <label v-if="!isSeated">总票额<el-input-number v-model="tier.total_quota" :min="1" /></label>
          </div>
          <label v-if="!isSeated && !isExhibition" class="switch-field">出票分配区内编号<el-switch v-model="tier.assign_place_no" /></label>
        </article>
        <button class="add-tier" type="button" @click="addTier(session)">＋ 添加票档</button>
      </div>
    </section>

    <section v-else-if="isSeated && step === 3" class="drawer-section">
      <SeatLayoutEditor
        v-model:rows="layoutRows"
        v-model:cols="layoutCols"
        v-model:cells="layoutCells"
        v-model:paint-tier-id="paintTierId"
        :tiers="createdTiers"
      />
    </section>

    <section v-else class="publish-check">
      <span>活动</span><strong>{{ eventForm.title }}</strong>
      <span>场次</span><strong>{{ sessions.length }} 场</strong>
      <span>售卖</span><strong>{{ isSeated ? '必须选座' : '计数售卖' }}</strong>
      <span>票档</span><strong>{{ sessions.flatMap(item => item.tiers.map(tier => tier.name)).filter(Boolean).join('、') }}</strong>
      <p v-if="isSeated">发布后按厅图生成座位库存，选座活动不支持限时开售。</p>
      <p v-else>发布后即可在购票站售卖。</p>
    </section>

    <template #footer>
      <div class="drawer-actions">
        <el-button @click="close">稍后继续</el-button>
        <el-button v-if="step > 0" @click="step -= 1">上一步</el-button>
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
.drawer-section :deep(.el-select), .drawer-section :deep(.el-date-picker) { width: 100%; }
.paired-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.switch-field { align-content: start; }
.drawer-hint, .publish-check p { color: var(--muted); font-size: 12px; line-height: 1.7; }
.session-editor, .tier-editor {
  padding: 18px; border: 1px solid var(--line); border-radius: var(--radius-md);
  background: rgba(255,255,255,.28); display: grid; gap: 14px;
}
.session-editor header, .tier-editor header { display: flex; justify-content: space-between; }
.session-editor header button, .tier-editor header button, .add-tier {
  border: 0; background: transparent; color: var(--red); cursor: pointer;
}
.session-label { margin: 0 0 8px; color: var(--muted); font-size: 12px; font-weight: 650; }
.tier-section { gap: 18px; }
.tier-numbers { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.tier-numbers.seated { grid-template-columns: 1fr 1fr; }
.tier-numbers :deep(.el-input-number) { width: 100%; }
.add-tier { min-height: 42px; border: 1px dashed var(--line-strong); border-radius: var(--radius-sm); }
.publish-check { padding: 24px; border: 1px solid var(--line-strong); border-radius: var(--radius-md); display: grid; grid-template-columns: 80px 1fr; gap: 14px; }
.publish-check span { color: var(--muted); }
.publish-check p { grid-column: 1 / -1; margin: 14px 0 0; padding-top: 18px; border-top: 1px solid var(--line); }
.drawer-actions { display: flex; justify-content: flex-end; }
@media (max-width: 600px) {
  .paired-fields, .tier-numbers, .tier-numbers.seated { grid-template-columns: 1fr; }
}
</style>
