<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'

const props = defineProps({
  organizerId: { type: [String, Number], required: true },
  events: { type: Array, default: () => [] },
})

const COLORS = ['#d07a72', '#c45b52', '#b53429', '#9a2d24', '#7d241d']
const TOP_PCT = 100
const BOT_PCT = 44

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

const bands = computed(() => {
  const steps = funnel.value?.steps || []
  const n = Math.max(steps.length, 1)
  return steps.map((step, index) => {
    const topPct = TOP_PCT - ((TOP_PCT - BOT_PCT) * index) / n
    const botPct = TOP_PCT - ((TOP_PCT - BOT_PCT) * (index + 1)) / n
    const inset = ((1 - botPct / topPct) / 2) * 100
    return {
      ...step,
      width: `${topPct}%`,
      clip: `polygon(0 0, 100% 0, ${100 - inset}% 100%, ${inset}% 100%)`,
      fill: COLORS[index] || COLORS[COLORS.length - 1],
      isLeak: funnel.value?.leak?.step_key === step.key,
    }
  })
})

const paidConversion = computed(() => {
  const paid = funnel.value?.steps?.find(step => step.key === 'paid')
  return paid?.from_top ?? 0
})

function formatPct(value) {
  const n = Number(value || 0)
  if (!Number.isFinite(n)) return '—'
  return `${n.toFixed(n % 1 ? 1 : 0)}%`
}

function formatRange(from, days) {
  if (!from) return ''
  const start = new Date(from)
  if (Number.isNaN(start.getTime())) return ''
  const end = new Date()
  const fmt = (d) => `${d.getMonth() + 1}/${d.getDate()}`
  return `${fmt(start)} – ${fmt(end)} · ${days} 天`
}

function payBar(channel) {
  return `${Math.min(100, Number(channel.pay_rate || 0))}%`
}

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
    ElMessage.error(error.response?.data?.msg || '用户路径诊断加载失败')
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
  <section class="funnel-board" aria-label="用户路径诊断">
    <header>
      <div>
        <h2>用户路径诊断</h2>
        <p v-if="funnel" class="funnel-range">{{ formatRange(funnel.from, funnel.days) }}</p>
      </div>
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

    <div v-loading="loading" class="funnel-body">
      <template v-if="funnel">
        <p class="funnel-insight">{{ funnel.insight }}</p>
        <p class="funnel-definition">
          {{ eventId
            ? '前三步按同一浏览器访客、当前活动和所选时间窗去重。'
            : '前三步按“访客－活动”组合统计；评估单场转化时请筛选活动。' }}
          提交/支付只统计真实订单，不读演示汇总表。
        </p>
        <p v-if="funnel.leak" class="funnel-leak">
          最大流失在「{{ funnel.leak.label }}」，相对上一步掉了 {{ formatPct(funnel.leak.drop) }}。
        </p>

        <div class="funnel-kpis" aria-label="票务转化关键指标">
          <article>
            <span>浏览至支付</span>
            <strong>{{ formatPct(paidConversion) }}</strong>
            <small>同一统计窗口</small>
          </article>
          <article>
            <span>待支付</span>
            <strong>{{ funnel.pending_open }}</strong>
            <small>窗口内未完成订单</small>
          </article>
          <article>
            <span>退款率</span>
            <strong>{{ formatPct(funnel.refund_rate) }}</strong>
            <small>{{ funnel.refunded }} 笔已退款</small>
          </article>
          <article>
            <span>已核销 / 有效已售</span>
            <strong>{{ funnel.used_tickets || 0 }} / {{ funnel.valid_sold_tickets || 0 }}</strong>
            <small>{{ formatPct(funnel.checkin_rate) }} · 不含退票作废</small>
          </article>
        </div>

        <div class="funnel-chart" role="img" :aria-label="funnel.insight">
          <div class="funnel-stack">
            <article
              v-for="band in bands"
              :key="band.key"
              class="funnel-slice"
              :class="{ leak: band.isLeak }"
              :style="{
                width: band.width,
                clipPath: band.clip,
                background: band.fill,
              }"
            >
              <span>{{ band.label }}</span>
              <strong>{{ band.count }} {{ band.unit }}</strong>
              <small v-if="band.key === 'browse'">起点</small>
              <small v-else>
                较上步 {{ formatPct(band.from_prev) }}
                <template v-if="band.isLeak"> · 流失最大</template>
              </small>
            </article>
          </div>
        </div>

        <h3 class="funnel-subtitle">购票方式表现 <small>订单口径</small></h3>
        <div class="funnel-channels">
          <article v-for="channel in funnel.channels" :key="channel.key">
            <h3>{{ channel.label }}</h3>
            <div class="channel-bar" aria-hidden="true">
              <i :style="{ width: payBar(channel) }"></i>
            </div>
            <p>支付率 {{ formatPct(channel.pay_rate) }} · 提交 {{ channel.submitted }} · 支付 {{ channel.paid }}</p>
            <p>退款 {{ channel.refunded }} · {{ formatPct(channel.refund_rate) }}</p>
          </article>
        </div>

        <p v-if="!funnel.visit_tracked" class="funnel-hint">暂无浏览数据</p>
      </template>
    </div>
  </section>
</template>

<style scoped>
.funnel-board {
  margin-top: 22px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  padding: 22px 28px 24px;
  background: rgba(255, 255, 255, .28);
}
.funnel-board > header {
  display: flex;
  justify-content: space-between;
  align-items: end;
  gap: 16px;
  margin-bottom: 16px;
}
.funnel-board h2 {
  margin: 0;
  font: 720 22px var(--font-display);
}
.funnel-board header p,
.funnel-hint {
  margin: 6px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}
.funnel-range {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 12px;
}
.funnel-filters {
  display: flex;
  gap: 10px;
}
.funnel-filters :deep(.el-select) { width: 150px; }
.funnel-insight {
  margin: 0 0 8px;
  color: var(--ink);
  font-size: 14px;
  font-weight: 650;
}
.funnel-definition {
  margin: 0 0 12px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}
.funnel-leak {
  margin: 0 0 16px;
  color: var(--red);
  font-size: 12px;
}
.funnel-chart {
  width: 100%;
  max-width: 520px;
  margin-inline: auto;
}
.funnel-kpis {
  margin: 0 0 20px;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.funnel-kpis article {
  display: grid;
  gap: 3px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  padding: 12px 14px;
  background: rgba(255, 255, 255, .32);
}
.funnel-kpis span,
.funnel-kpis small {
  color: var(--muted);
  font-size: 11px;
}
.funnel-kpis strong {
  color: var(--ink);
  font: 700 20px var(--font-display);
}
.funnel-stack {
  display: grid;
  gap: 6px;
}
.funnel-slice {
  height: 68px;
  margin: 0 auto;
  display: grid;
  place-items: center;
  align-content: center;
  color: #fff;
  text-align: center;
  line-height: 1.2;
}
.funnel-slice span {
  font-size: 12px;
  opacity: .9;
}
.funnel-slice strong {
  font-size: 15px;
  font-weight: 700;
}
.funnel-slice small {
  font-size: 11px;
  opacity: .88;
}
.funnel-slice.leak {
  box-shadow: inset 0 0 0 2px rgba(255, 255, 255, .35);
}
.funnel-slice.leak small {
  font-weight: 650;
}
.funnel-channels article {
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  padding: 14px 16px;
}
.funnel-channels p {
  display: block;
  color: var(--muted);
  font-size: 12px;
}
.funnel-subtitle { margin: 22px 0 10px; font: 680 15px var(--font-body); }
.funnel-subtitle small { margin-left: 6px; color: var(--muted); font-size: 11px; font-weight: 500; }
.funnel-channels {
  margin-top: 12px;
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
}
.funnel-channels h3 {
  margin: 0 0 10px;
  font: 650 14px var(--font-body);
}
.funnel-channels p { margin: 4px 0 0; }
.channel-bar {
  height: 8px;
  border-radius: 99px;
  background: rgba(181, 52, 41, .1);
  overflow: hidden;
}
.channel-bar i {
  display: block;
  height: 100%;
  background: var(--red);
}
@media (max-width: 640px) {
  .funnel-board > header { display: grid; }
  .funnel-kpis { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .funnel-channels { grid-template-columns: 1fr; }
}
</style>
