<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'
import SeatLayoutEditor from './SeatLayoutEditor.vue'

const props = defineProps({ modelValue: Boolean, organizerId: [String, Number], venues: { type: Array, default: () => [] } })
const emit = defineEmits(['update:modelValue'])
const venueId = ref('')
const halls = ref([])
const hallPacks = ref([])
const hallId = ref('')
const layouts = ref([])
const hallName = ref('')
const layoutName = ref('')
const rows = ref(8)
const cols = ref(12)
const cells = ref({})
const zoneKey = ref('general')
const saving = ref(false)
const loading = ref(false)
const zones = [{ id: 'general', name: '普通区' }, { id: 'vip', name: 'VIP 区' }, { id: 'balcony', name: '楼座' }]
const draft = computed(() => layouts.value.find(item => item.status === 'draft'))
const selectedHall = computed(() => halls.value.find(item => String(item.id) === String(hallId.value)))
const MOCKS = [
  { name: '一层池座', rows: 10, cols: 14, kind: (r, c, rowCount, colCount) => (r <= 2 ? 'vip' : 'general') },
  { name: '小剧场', rows: 6, cols: 9, kind: (r, c, rowCount, colCount) => {
    if (c === 1 || c === colCount) return ''
    return r <= 1 ? 'vip' : 'general'
  } },
  { name: '楼座厅', rows: 8, cols: 16, kind: (r, c, rowCount, colCount) => {
    if (r <= 3 && (c <= 2 || c >= colCount - 1)) return ''
    return r <= 3 ? 'general' : 'balcony'
  } },
]

watch(() => props.modelValue, async open => {
  if (!open) return
  venueId.value = String(props.venues[0]?.id || '')
  hallId.value = ''
  await loadHalls()
})

function mockForHall(hall, index) {
  const spec = MOCKS[index % MOCKS.length]
  const next = {}
  for (let r = 1; r <= spec.rows; r += 1) {
    for (let c = 1; c <= spec.cols; c += 1) {
      const zone = spec.kind(r, c, spec.rows, spec.cols)
      if (zone) next[`${r}:${c}`] = zone
    }
  }
  return { name: `${hall.name} · ${spec.name}`, rows: spec.rows, cols: spec.cols, cells: next, mock: true }
}

function cellsFromLayout(layout) {
  return Object.fromEntries((layout.seats || []).map(seat => [`${seat.row_no}:${seat.col_no}`, seat.zone_key || 'general']))
}

function previewOf(pack, index) {
  const published = (pack.layouts || []).find(item => item.status === 'published')
  const draftLayout = (pack.layouts || []).find(item => item.status === 'draft')
  const layout = published || draftLayout
  if (layout) {
    return {
      name: layout.name,
      rows: layout.row_count,
      cols: layout.col_count,
      cells: cellsFromLayout(layout),
      status: published ? `已发布 V${published.version || 1}` : '草稿',
      mock: false,
    }
  }
  return { ...mockForHall(pack.hall, index), status: '示意，尚未保存' }
}

const hallCards = computed(() => hallPacks.value.map((pack, index) => ({
  hall: pack.hall,
  preview: previewOf(pack, index),
})))

async function loadHalls() {
  hallId.value = ''
  layouts.value = []
  if (!venueId.value) {
    halls.value = []
    hallPacks.value = []
    return
  }
  loading.value = true
  try {
    const res = await api.organizerGetHalls(props.organizerId, venueId.value)
    halls.value = res.data || []
    hallPacks.value = await Promise.all(halls.value.map(async (hall) => {
      const layoutRes = await api.organizerGetHallLayouts(props.organizerId, hall.id).catch(() => ({ data: [] }))
      return { hall, layouts: layoutRes.data || [] }
    }))
  } finally {
    loading.value = false
  }
}

async function openHall(hall) {
  hallId.value = String(hall.id)
  const index = halls.value.findIndex(item => String(item.id) === String(hall.id))
  const pack = hallPacks.value[index]
  layouts.value = pack?.layouts || []
  const editable = layouts.value.find(item => item.status === 'draft') || layouts.value.find(item => item.status === 'published')
  if (editable) hydrate(editable)
  else {
    const mock = mockForHall(hall, index < 0 ? 0 : index)
    layoutName.value = mock.name
    rows.value = mock.rows
    cols.value = mock.cols
    cells.value = mock.cells
  }
}

function hydrate(layout) {
  rows.value = layout.row_count
  cols.value = layout.col_count
  layoutName.value = layout.name
  cells.value = cellsFromLayout(layout)
}

function backToHalls() {
  hallId.value = ''
  loadHalls()
}

async function createHall() {
  if (!venueId.value || !hallName.value.trim()) return ElMessage.warning('请选择场馆并填写厅名称')
  saving.value = true
  try {
    const res = await api.organizerCreateHall(props.organizerId, venueId.value, { name: hallName.value.trim() })
    ElMessage.success('演出厅已创建')
    hallName.value = ''
    await loadHalls()
    const hall = halls.value.find(item => String(item.id) === String(res.data.id))
    if (hall) openHall(hall)
  } catch (error) { ElMessage.error(error.response?.data?.msg || '创建失败') }
  finally { saving.value = false }
}

async function saveLayout() {
  if (!hallId.value || !Object.keys(cells.value).length) return ElMessage.warning('请选择演出厅并绘制座位')
  const payload = {
    name: layoutName.value.trim(), row_count: Number(rows.value), col_count: Number(cols.value),
    seats: Object.entries(cells.value).map(([key, zone]) => {
      const [row, col] = key.split(':').map(Number)
      return { row_no: row, col_no: col, label: `${String.fromCharCode(64 + row)}${col}`, zone_key: zone }
    }),
  }
  saving.value = true
  try {
    if (draft.value) await api.organizerUpdateHallLayout(props.organizerId, draft.value.id, payload)
    else await api.organizerCreateHallLayout(props.organizerId, hallId.value, payload)
    ElMessage.success('厅图草稿已保存')
    const res = await api.organizerGetHallLayouts(props.organizerId, hallId.value)
    layouts.value = res.data || []
  } catch (error) { ElMessage.error(error.response?.data?.msg || '保存失败') }
  finally { saving.value = false }
}

async function publishLayout() {
  if (!draft.value) return ElMessage.warning('请先保存厅图草稿')
  saving.value = true
  try {
    await api.organizerPublishHallLayout(props.organizerId, draft.value.id)
    ElMessage.success('厅图已发布，只有选座活动会用到')
    const res = await api.organizerGetHallLayouts(props.organizerId, hallId.value)
    layouts.value = res.data || []
  } catch (error) { ElMessage.error(error.response?.data?.msg || '发布失败') }
  finally { saving.value = false }
}

function previewDots(preview) {
  const dots = []
  const rowCount = preview.rows || 1
  const colCount = Math.min(preview.cols || 1, 16)
  for (let r = 1; r <= rowCount; r += 1) {
    for (let c = 1; c <= colCount; c += 1) {
      dots.push({ key: `${r}:${c}`, zone: preview.cells[`${r}:${c}`] || '' })
    }
  }
  return dots
}

function previewStyle(preview) {
  return { gridTemplateColumns: `repeat(${Math.min(preview.cols || 1, 16)}, 7px)` }
}
</script>

<template>
  <el-dialog :model-value="modelValue" title="厅图资产" width="min(880px, 94vw)" @update:model-value="emit('update:modelValue', $event)">
    <p class="lead">只给<strong>选座</strong>活动用。展览、通票等按张数卖的门票制，不用画座位。</p>
    <div class="toolbar">
      <label>场馆
        <el-select v-model="venueId" @change="loadHalls">
          <el-option v-for="v in venues" :key="v.id" :label="v.name" :value="String(v.id)" />
        </el-select>
      </label>
      <label v-if="!hallId">新增演出厅
        <el-input v-model="hallName" placeholder="例如 1 号厅">
          <template #append><el-button :loading="saving" @click="createHall">创建</el-button></template>
        </el-input>
      </label>
    </div>

    <div v-if="!hallId" v-loading="loading" class="hall-list">
      <p v-if="!hallCards.length" class="empty">这个场馆还没有演出厅。选座活动才需要；门票制活动可跳过。</p>
      <button
        v-for="card in hallCards"
        :key="card.hall.id"
        type="button"
        class="hall-card"
        @click="openHall(card.hall)"
      >
        <div class="mini-grid" :style="previewStyle(card.preview)">
          <i
            v-for="dot in previewDots(card.preview)"
            :key="dot.key"
            :class="dot.zone"
          />
        </div>
        <div>
          <strong>{{ card.hall.name }}</strong>
          <span>{{ card.preview.name }}</span>
          <em>{{ card.preview.status }} · {{ Object.keys(card.preview.cells).length }} 座</em>
        </div>
      </button>
    </div>

    <div v-else>
      <button class="back" type="button" @click="backToHalls">← 全部演出厅</button>
      <p class="editing">正在编辑 {{ selectedHall?.name }}</p>
      <label class="name-field">厅图名称<el-input v-model="layoutName" /></label>
      <SeatLayoutEditor v-model:rows="rows" v-model:cols="cols" v-model:cells="cells" v-model:paint-tier-id="zoneKey" :tiers="zones" />
    </div>

    <template v-if="hallId" #footer>
      <el-button :loading="saving" @click="saveLayout">保存草稿</el-button>
      <el-button type="primary" :loading="saving" @click="publishLayout">发布当前版本</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.lead { margin: 0 0 16px; color: var(--muted); font-size: 13px; line-height: 1.6; }
.lead strong { color: var(--ink); font-weight: 700; }
.toolbar { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 16px; }
label, .name-field { display: grid; gap: 7px; font-size: 13px; font-weight: 650; }
label :deep(.el-select) { width: 100%; }
.name-field { margin-bottom: 12px; }
.empty { color: var(--muted); font-size: 13px; }
.hall-list { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.hall-card {
  display: grid;
  grid-template-columns: 120px 1fr;
  gap: 14px;
  padding: 14px;
  border: 1px solid var(--line-strong);
  border-radius: 16px;
  background: #fff;
  text-align: left;
  cursor: pointer;
}
.hall-card:hover { border-color: var(--red); }
.hall-card strong { display: block; font-size: 15px; }
.hall-card span, .hall-card em { display: block; margin-top: 4px; color: var(--muted); font-size: 12px; font-style: normal; }
.mini-grid {
  display: grid;
  gap: 2px;
  align-content: start;
  min-height: 72px;
}
.mini-grid i { width: 7px; height: 7px; border-radius: 1px; background: transparent; }
.mini-grid i.general { background: #8a7a68; }
.mini-grid i.vip { background: #b53429; }
.mini-grid i.balcony { background: #244f85; }
.back, .editing { border: 0; background: transparent; color: var(--red); font: inherit; cursor: pointer; }
.editing { margin: 8px 0 12px; color: var(--ink); font-weight: 650; cursor: default; }
@media (max-width: 700px) {
  .toolbar, .hall-list { grid-template-columns: 1fr; }
  .hall-card { grid-template-columns: 1fr; }
}
</style>
