<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'
import SeatLayoutEditor from './SeatLayoutEditor.vue'

const props = defineProps({ modelValue: Boolean, organizerId: [String, Number], venues: { type: Array, default: () => [] } })
const emit = defineEmits(['update:modelValue'])
const venueId = ref('')
const halls = ref([])
const hallId = ref('')
const layouts = ref([])
const hallName = ref('')
const layoutName = ref('标准厅图')
const rows = ref(8)
const cols = ref(12)
const cells = ref({})
const zoneKey = ref('general')
const saving = ref(false)
const zones = [{ id: 'general', name: '普通区' }, { id: 'vip', name: 'VIP 区' }, { id: 'balcony', name: '楼座' }]
const draft = computed(() => layouts.value.find(item => item.status === 'draft'))

watch(() => props.modelValue, async open => {
  if (!open) return
  venueId.value = String(props.venues[0]?.id || '')
  await loadHalls()
})

async function loadHalls() {
  if (!venueId.value) return
  const res = await api.organizerGetHalls(props.organizerId, venueId.value)
  halls.value = res.data || []
  hallId.value = String(halls.value[0]?.id || '')
  await loadLayouts()
}

async function loadLayouts() {
  if (!hallId.value) { layouts.value = []; return }
  const res = await api.organizerGetHallLayouts(props.organizerId, hallId.value)
  layouts.value = res.data || []
  const editable = layouts.value.find(item => item.status === 'draft')
  if (editable) hydrate(editable)
  else { cells.value = {}; rows.value = 8; cols.value = 12; layoutName.value = '标准厅图' }
}

function hydrate(layout) {
  rows.value = layout.row_count
  cols.value = layout.col_count
  layoutName.value = layout.name
  cells.value = Object.fromEntries((layout.seats || []).map(seat => [`${seat.row_no}:${seat.col_no}`, seat.zone_key || 'general']))
}

async function createHall() {
  if (!venueId.value || !hallName.value.trim()) return ElMessage.warning('请选择场馆并填写厅名称')
  saving.value = true
  try {
    const res = await api.organizerCreateHall(props.organizerId, venueId.value, { name: hallName.value.trim() })
    ElMessage.success('演出厅已创建')
    hallName.value = ''
    await loadHalls()
    hallId.value = String(res.data.id)
    await loadLayouts()
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
    await loadLayouts()
  } catch (error) { ElMessage.error(error.response?.data?.msg || '保存失败') }
  finally { saving.value = false }
}

async function publishLayout() {
  if (!draft.value) return ElMessage.warning('请先保存厅图草稿')
  saving.value = true
  try { await api.organizerPublishHallLayout(props.organizerId, draft.value.id); ElMessage.success('厅图版本已发布，可用于新场次'); await loadLayouts() }
  catch (error) { ElMessage.error(error.response?.data?.msg || '发布失败') }
  finally { saving.value = false }
}
</script>

<template>
  <el-dialog :model-value="modelValue" title="场馆与厅图资产" width="min(820px, 94vw)" @update:model-value="emit('update:modelValue', $event)">
    <div class="asset-grid">
      <label>场馆<el-select v-model="venueId" @change="loadHalls"><el-option v-for="v in venues" :key="v.id" :label="v.name" :value="String(v.id)" /></el-select></label>
      <label>演出厅<el-select v-model="hallId" placeholder="先创建演出厅" @change="loadLayouts"><el-option v-for="h in halls" :key="h.id" :label="h.name" :value="String(h.id)" /></el-select></label>
      <label>新增演出厅<el-input v-model="hallName" placeholder="例如 1 号厅"><template #append><el-button :loading="saving" @click="createHall">创建</el-button></template></el-input></label>
      <label>厅图名称<el-input v-model="layoutName" /></label>
    </div>
    <p class="hint">物理厅图只描述座位和分区。票价在每个场次中单独配置；发布后的版本保持冻结。</p>
    <SeatLayoutEditor v-model:rows="rows" v-model:cols="cols" v-model:cells="cells" v-model:paint-tier-id="zoneKey" :tiers="zones" />
    <template #footer>
      <el-button :loading="saving" @click="saveLayout">保存草稿</el-button>
      <el-button type="primary" :loading="saving" @click="publishLayout">发布当前版本</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.asset-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 14px; }
label { display: grid; gap: 7px; font-size: 13px; font-weight: 650; }
label :deep(.el-select) { width: 100%; }
.hint { color: var(--muted); font-size: 12px; }
@media (max-width: 600px) { .asset-grid { grid-template-columns: 1fr; } }
</style>
