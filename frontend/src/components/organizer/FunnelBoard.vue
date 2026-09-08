<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'

const props = defineProps({
  organizerId: { type: [String, Number], required: true },
  events: { type: Array, default: () => [] },
})

const loading = ref(false)
const days = ref(7)
const eventId = ref('')
const funnel = ref(null)

const eventOptions = computed(() =>
  (props.events || []).map(item => ({
    id: String(item.id),
    title: item.title,
  })),
)

const steps = computed(() => {
  const list = funnel.value?.steps || []
  const byKey = Object.fromEntries(list.map(item => [item.key, item]))
  return [
    { key: 'browse', label: '曝光', value: Number(byKey.browse?.count || 0), unit: '人' },
    { key: 'detail', label: '进详情', value: Number(byKey.detail?.count || 0), unit: '人' },
    { key: 'ordered', label: '下单数', value: Number(byKey.submitted?.count || 0), unit: '单' },
    { key: 'paid', label: '付款数', value: Number(byKey.paid?.count || 0), unit: '单' },
  ]
})

async function loadFunnel() {
  if (!props.organizerId) return
  loading.value = true
  try {
    const params = { days: days.value }
    if (eventId.value) params.event_id = eventId.value
    const res = await api.organizerGetFunnel(props.organizerId, params)
    funnel.value = res.data
  } catch (error) {
    funnel.value = null
    ElMessage.error(error.response?.data?.msg || '转化数据加载失败')
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.organizerId, days.value, eventId.value],
  () => {
    loadFunnel()
  },
  { immediate: true },
)

watch(
  () => props.events,
  () => {
    if (eventId.value && !eventOptions.value.some(item => item.id === String(eventId.value))) {
      eventId.value = ''
    }
  },
)
</script>

<template>
  <section class="funnel-board" aria-label="购票转化">
    <header>
      <h2>购票转化</h2>
      <div class="funnel-filters">
        <el-select v-model="days" aria-label="统计周期">
          <el-option :value="7" label="近 7 天" />
          <el-option :value="30" label="近 30 天" />
        </el-select>
        <el-select v-model="eventId" clearable placeholder="全部活动" aria-label="筛选活动">
          <el-option
            v-for="item in eventOptions"
            :key="item.id"
            :label="item.title"
            :value="item.id"
          />
        </el-select>
      </div>
    </header>

    <div v-loading="loading">
      <template v-if="funnel">
        <ol class="funnel-path">
          <li v-for="step in steps" :key="step.key">
            <span>{{ step.label }}</span>
            <strong>{{ step.value }}<em>{{ step.unit }}</em></strong>
          </li>
        </ol>
      </template>
    </div>
  </section>
</template>

<style scoped>
.funnel-board {
  margin-top: 28px;
}
.funnel-board > header {
  display: flex;
  justify-content: space-between;
  align-items: end;
  gap: 16px;
  margin-bottom: 14px;
}
.funnel-board h2 {
  margin: 0;
  font: 720 22px var(--font-display);
}
.funnel-filters { display: flex; gap: 10px; }
.funnel-filters :deep(.el-select) { width: 150px; }
.funnel-path {
  margin: 0;
  padding: 0;
  list-style: none;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
.funnel-path li {
  padding: 22px 20px 18px;
  border-radius: 18px;
  background: #2c221c;
  color: #f4eee4;
}
.funnel-path li:nth-child(2) { background: #3a241f; }
.funnel-path li:nth-child(3) { background: #8a3228; }
.funnel-path li:nth-child(4) { background: #b53429; }
.funnel-path span {
  display: block;
  font-size: 13px;
  opacity: .82;
}
.funnel-path strong {
  display: block;
  margin-top: 10px;
  font: 760 34px/1 var(--font-display);
}
.funnel-path em {
  margin-left: 6px;
  font: 500 14px var(--font-body);
  opacity: .75;
}
@media (max-width: 720px) {
  .funnel-board > header { display: grid; }
  .funnel-path { grid-template-columns: 1fr; }
}
</style>
