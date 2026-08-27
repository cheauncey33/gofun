<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../../api'
import SeatLayoutEditor from './SeatLayoutEditor.vue'

const props = defineProps({
  event: { type: Object, default: null },
  organizerId: { type: [String, Number], required: true },
  venues: { type: Array, default: () => [] },
})

const emit = defineEmits(['saved'])

const saving = ref(false)
const sessions = ref([])
const layoutRows = ref(8)
const layoutCols = ref(12)
const layoutCells = ref({})
const paintTierId = ref('')

const isDraft = computed(() => props.event?.status === 'draft')
const isPublished = computed(() => props.event?.status === 'published')
const isSeated = computed(() => props.event?.sale_mode === 'seated')
const isExhibition = computed(() => props.event?.category === '展览')
const canAddSession = computed(() => isDraft.value && !isSeated.value)
const layoutTiers = computed(() =>
  sessions.value.flatMap(session => (session.tiers || []).filter(tier => tier.id).map(tier => ({
    id: tier.id,
    name: `${sessionLabel(session)} · ${tier.name}`,
  }))),
)

watch(
  () => [props.event?.id, props.event?.status, (props.event?.sessions || []).length],
  () => hydrate(),
  { immediate: true },
)

function newTier() {
  return {
    id: '',
    name: '',
    description: '',
    price_yuan: 188,
    total_quota: 100,
    assign_place_no: !isSeated.value && !isExhibition.value,
    status: 'on_sale',
    remaining_quota: 100,
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

function hydrate() {
  const list = props.event?.sessions || []
  sessions.value = list.length
    ? list.map(session => ({
        id: session.id,
        venue_id: session.venue_id,
        starts_at: session.starts_at ? new Date(session.starts_at) : null,
        ends_at: session.ends_at ? new Date(session.ends_at) : null,
        sale_starts_at: session.sale_starts_at ? new Date(session.sale_starts_at) : null,
        sale_ends_at: session.sale_ends_at ? new Date(session.sale_ends_at) : null,
        tiers: (session.ticket_tiers || []).map(tier => ({
          id: tier.id,
          name: tier.name,
          description: tier.description || '',
          price_yuan: Number(tier.price_cents || 0) / 100,
          total_quota: tier.total_quota || 0,
          assign_place_no: tier.assign_place_no !== false,
          status: tier.status,
          remaining_quota: tier.remaining_quota,
        })),
      }))
    : (isDraft.value ? [emptySession()] : [])
  if (isSeated.value && props.event?.id) loadLayout()
}

function sessionLabel(session) {
  if (!session?.starts_at) return '未定时场次'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(session.starts_at))
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

function validSession(session) {
  if (!session.venue_id || !session.starts_at || !session.ends_at ||
      !session.sale_starts_at || !session.sale_ends_at) return false
  const start = new Date(session.starts_at)
  return start < new Date(session.ends_at) &&
    new Date(session.sale_starts_at) < new Date(session.sale_ends_at) &&
    new Date(session.sale_ends_at) <= start
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

async function loadLayout() {
  try {
    const res = await api.organizerGetSeatLayout(props.organizerId, props.event.id)
    const layout = res.data
    layoutRows.value = layout.row_count || 8
    layoutCols.value = layout.col_count || 12
    const cells = {}
    for (const seat of layout.seats || []) {
      cells[cellKey(seat.row_no, seat.col_no)] = String(seat.ticket_tier_id)
    }
    layoutCells.value = cells
    paintTierId.value = String(layoutTiers.value[0]?.id || '')
  } catch {
    layoutRows.value = 8
    layoutCols.value = 12
    layoutCells.value = {}
  }
}

async function persistSession(session) {
  if (!validSession(session)) throw new Error('请检查场次和售票时间，售票结束不能晚于开场时间')
  const payload = sessionPayload(session)
  if (session.id) {
    await api.organizerUpdateSession(props.organizerId, session.id, payload)
  } else {
    const res = await api.organizerCreateSession(props.organizerId, props.event.id, payload)
    session.id = res.data.id
  }
}

async function persistTier(session, tier) {
  if (!session.id) await persistSession(session)
  if (!tier.name.trim() || Number(tier.price_yuan) <= 0) throw new Error('票档需要名称和价格')
  if (!isSeated.value && Number(tier.total_quota) <= 0) throw new Error('票档需要票额')
  const payload = {
    name: tier.name.trim(),
    description: tier.description.trim(),
    price_cents: Math.round(Number(tier.price_yuan) * 100),
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

async function saveSession(session) {
  saving.value = true
  try {
    await persistSession(session)
    ElMessage.success('场次已保存')
    emit('saved')
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || error.message || '场次保存失败')
  } finally {
    saving.value = false
  }
}

async function saveTier(session, tier) {
  saving.value = true
  try {
    await persistTier(session, tier)
    ElMessage.success('票档已保存')
    emit('saved')
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || error.message || '票档保存失败')
  } finally {
    saving.value = false
  }
}

async function disableTier(tier) {
  try {
    await ElMessageBox.confirm('停售后前台不再出售该票档，已售出票不受影响。', '停售票档')
    saving.value = true
    await api.organizerDisableTicketTier(props.organizerId, tier.id)
    tier.status = 'disabled'
    ElMessage.success('票档已停售')
    emit('saved')
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.response?.data?.msg || '停售失败')
  } finally {
    saving.value = false
  }
}

async function saveLayout() {
  const seats = Object.entries(layoutCells.value)
  if (!seats.length) {
    ElMessage.warning('请至少涂一个可售座位')
    return
  }
  saving.value = true
  try {
    await api.organizerSaveSeatLayout(props.organizerId, props.event.id, {
      name: '主厅',
      row_count: Number(layoutRows.value),
      col_count: Number(layoutCols.value),
      seats: seats.map(([key, tierId]) => {
        const [row, col] = key.split(':').map(Number)
        return {
          row_no: row,
          col_no: col,
          ticket_tier_id: String(tierId),
          label: `${rowLabel(row)}${col}`,
        }
      }),
    })
    ElMessage.success('厅图已保存')
    emit('saved')
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '厅图保存失败')
  } finally {
    saving.value = false
  }
}

function addSession() {
  sessions.value.push(emptySession())
}

function addTier(session) {
  session.tiers.push(newTier())
}
</script>

<template>
  <div class="sales-panel">
    <article v-for="(session, sessionIndex) in sessions" :key="session.id || `new-${sessionIndex}`" class="session-card">
      <header>
        <strong>场次 {{ sessionIndex + 1 }}</strong>
        <span v-if="session.id">{{ sessionLabel(session) }}</span>
      </header>
      <label>场馆
        <el-select v-model="session.venue_id" :disabled="!isDraft">
          <el-option
            v-for="venue in venues"
            :key="venue.id"
            :label="`${venue.name} · ${venue.city}`"
            :value="venue.id"
          />
        </el-select>
      </label>
      <div class="paired">
        <label>开场时间<el-date-picker v-model="session.starts_at" type="datetime" /></label>
        <label>结束时间<el-date-picker v-model="session.ends_at" type="datetime" /></label>
      </div>
      <div class="paired">
        <label>开售时间<el-date-picker v-model="session.sale_starts_at" type="datetime" /></label>
        <label>停售时间<el-date-picker v-model="session.sale_ends_at" type="datetime" /></label>
      </div>
      <div class="row-actions">
        <el-button size="small" :loading="saving" @click="saveSession(session)">保存场次</el-button>
      </div>

      <div v-for="(tier, tierIndex) in session.tiers" :key="tier.id || `tier-${tierIndex}`" class="tier-card">
        <header>
          <strong>票档 {{ tierIndex + 1 }}</strong>
          <span v-if="tier.status === 'disabled'">已停售</span>
          <span v-else-if="tier.id && isPublished">余 {{ tier.remaining_quota }}</span>
        </header>
        <label>名称<el-input v-model="tier.name" maxlength="64" /></label>
        <label>说明<el-input v-model="tier.description" maxlength="256" /></label>
        <div class="paired">
          <label>价格（元）<el-input-number v-model="tier.price_yuan" :min="0.01" :precision="2" /></label>
          <label v-if="!isSeated">总票额<el-input-number v-model="tier.total_quota" :min="1" :disabled="isSeated" /></label>
        </div>
        <div class="row-actions">
          <el-button size="small" :loading="saving" @click="saveTier(session, tier)">保存票档</el-button>
          <el-button
            v-if="tier.id && tier.status !== 'disabled'"
            size="small"
            :loading="saving"
            @click="disableTier(tier)"
          >停售</el-button>
        </div>
      </div>
      <button v-if="isDraft" class="add-btn" type="button" @click="addTier(session)">＋ 添加票档</button>
    </article>

    <button v-if="canAddSession" class="add-btn" type="button" @click="addSession">＋ 添加场次</button>

    <section v-if="isSeated" class="layout-card">
      <header><strong>厅图</strong></header>
      <SeatLayoutEditor
        v-model:rows="layoutRows"
        v-model:cols="layoutCols"
        v-model:cells="layoutCells"
        v-model:paint-tier-id="paintTierId"
        :tiers="layoutTiers"
        :disabled="!isDraft"
      />
      <el-button v-if="isDraft" type="primary" size="small" :loading="saving" @click="saveLayout">保存厅图</el-button>
    </section>
  </div>
</template>

<style scoped>
.sales-panel { display: grid; gap: 16px; }
.lead { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.7; }
.session-card, .layout-card {
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: rgba(255,255,255,.28);
  display: grid;
  gap: 12px;
}
.session-card > header, .tier-card header, .layout-card header {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}
.session-card > header strong, .layout-card header strong { color: var(--ink); font-size: 14px; }
label { display: grid; gap: 6px; font-size: 13px; font-weight: 650; }
label :deep(.el-select), label :deep(.el-date-picker), label :deep(.el-input-number) { width: 100%; }
.paired { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.tier-card {
  padding: 12px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-sm);
  display: grid;
  gap: 10px;
}
.row-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.add-btn {
  min-height: 40px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--red);
  cursor: pointer;
}
@media (max-width: 600px) {
  .paired { grid-template-columns: 1fr; }
}
</style>
