<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  event: { type: Object, default: null },
  session: { type: Object, default: null },
  seats: { type: Array, default: () => [] },
})

const emit = defineEmits(['update:modelValue', 'confirm'])

const selectedIds = ref([])
const focusTierId = ref('')
const TIER_COLORS = ['#2f9e6d', '#3b82f6', '#f0a202', '#d94f70', '#7c5cbf', '#0f9d8e']

const layoutRows = computed(() => props.seats.reduce((max, item) => Math.max(max, item.row_no), 0))
const layoutCols = computed(() => props.seats.reduce((max, item) => Math.max(max, item.col_no), 0))
const seatByCell = computed(() => {
  const map = {}
  for (const seat of props.seats) map[`${seat.row_no}:${seat.col_no}`] = seat
  return map
})
const aisleCols = computed(() => {
  const used = new Set(props.seats.map(item => item.col_no))
  return Array.from({ length: layoutCols.value }, (_, i) => i + 1).filter(col => !used.has(col))
})
const tiers = computed(() => {
  const byId = {}
  for (const tier of props.session?.ticket_tiers || []) byId[String(tier.id)] = tier
  const seen = []
  const order = []
  for (const seat of props.seats) {
    const id = String(seat.ticket_tier_id)
    if (!seen.includes(id) && byId[id]) {
      seen.push(id)
      order.push(byId[id])
    }
  }
  return order.sort((a, b) => a.price_cents - b.price_cents)
})
const colorByTier = computed(() => {
  const map = {}
  tiers.value.forEach((tier, index) => {
    map[String(tier.id)] = TIER_COLORS[index % TIER_COLORS.length]
  })
  return map
})
const selectedSeats = computed(() =>
  selectedIds.value.map(id => props.seats.find(item => String(item.id) === String(id))).filter(Boolean),
)
const totalCents = computed(() => selectedSeats.value.reduce((sum, seat) => {
  const tier = (props.session?.ticket_tiers || []).find(item => String(item.id) === String(seat.ticket_tier_id))
  return sum + (tier?.price_cents || 0)
}, 0))
const maxSelectable = computed(() => props.event?.max_tickets_per_order || 1)

watch(() => props.modelValue, (open) => {
  if (!open) {
    selectedIds.value = []
    focusTierId.value = ''
  }
})

function money(cents) {
  return `¥${((cents || 0) / 100).toFixed(cents % 100 ? 2 : 0)}`
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

function colStyle(col) {
  return aisleCols.value.includes(col) ? { width: '14px' } : { width: '28px' }
}

function seatStyle(seat) {
  if (!seat || seat.status !== 'available') return {}
  const color = colorByTier.value[String(seat.ticket_tier_id)]
  if (selectedIds.value.includes(String(seat.id))) {
    return { background: color, borderColor: color, color: '#fff' }
  }
  return { background: `${color}22`, borderColor: color, color }
}

function seatClass(seat) {
  if (!seat) return 'empty'
  if (seat.status === 'sold') return 'sold'
  if (seat.status === 'held') return 'held'
  if (seat.status !== 'available') return 'taken'
  if (focusTierId.value && String(seat.ticket_tier_id) !== String(focusTierId.value)) return 'dimmed'
  return ''
}

function toggleSeat(seat) {
  if (!seat || seat.status !== 'available') return
  const id = String(seat.id)
  if (selectedIds.value.includes(id)) {
    selectedIds.value = selectedIds.value.filter(item => item !== id)
    return
  }
  if (selectedIds.value.length >= maxSelectable.value) return
  selectedIds.value = [...selectedIds.value, id]
}

function confirm() {
  if (!selectedSeats.value.length) return
  emit('confirm', {
    seatIds: selectedIds.value,
    tierId: selectedSeats.value[0].ticket_tier_id,
    totalCents: totalCents.value,
  })
}

function close() {
  emit('update:modelValue', false)
}
</script>

<template>
  <el-dialog
    class="seat-dialog"
    :model-value="modelValue"
    :show-close="false"
    width="min(960px, 96vw)"
    align-center
    @update:model-value="close"
  >
    <template #header>
      <div class="dialog-head">
        <div>
          <small>选座购票</small>
          <h3>{{ event?.title }}</h3>
        </div>
        <button type="button" @click="close">关闭</button>
      </div>
    </template>

    <div class="stage">
      <div class="screen">舞台 / 银幕</div>
      <div class="map-scroll">
        <div class="seat-map" :style="{ gridTemplateColumns: `28px repeat(${layoutCols || 1}, minmax(14px, 28px)) 28px` }">
          <template v-for="row in layoutRows" :key="row">
            <span class="row-label">{{ rowLabel(row) }}</span>
            <template v-for="col in layoutCols" :key="`${row}-${col}`">
              <span v-if="aisleCols.includes(col)" class="aisle" :style="colStyle(col)" />
              <button
                v-else
                class="seat-btn"
                type="button"
                :style="seatStyle(seatByCell[`${row}:${col}`])"
                :class="seatClass(seatByCell[`${row}:${col}`])"
                :title="seatByCell[`${row}:${col}`]?.label"
                :disabled="!seatByCell[`${row}:${col}`] || seatByCell[`${row}:${col}`].status !== 'available'"
                @click="toggleSeat(seatByCell[`${row}:${col}`])"
              >{{ seatByCell[`${row}:${col}`]?.label?.replace(/^[A-Z]+/, '') || '' }}</button>
            </template>
            <span class="row-label">{{ rowLabel(row) }}</span>
          </template>
        </div>
      </div>
    </div>

    <ul class="legend">
      <li
        v-for="tier in tiers"
        :key="tier.id"
        :class="{ active: focusTierId === String(tier.id) }"
        @click="focusTierId = focusTierId === String(tier.id) ? '' : String(tier.id)"
      >
        <i :style="{ background: colorByTier[String(tier.id)] }" />
        {{ tier.name }} {{ money(tier.price_cents) }}
      </li>
      <li><i class="sold" />已售</li>
      <li><i class="held" />锁定中</li>
      <li><i class="picked" />已选</li>
    </ul>

    <template #footer>
      <div class="picker-footer">
        <div>
          <p v-if="selectedSeats.length">已选 {{ selectedSeats.map(item => item.label).join('、') }}</p>
          <p v-else>点击座位选择，点价区可只看该档；每账号最多 {{ maxSelectable }} 张</p>
          <strong>{{ selectedSeats.length ? money(totalCents) : '' }}</strong>
        </div>
        <button type="button" :disabled="!selectedSeats.length" @click="confirm">确认选座</button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
.dialog-head { display: flex; justify-content: space-between; align-items: start; gap: 16px; }
.dialog-head small { color: var(--red); letter-spacing: .16em; font-size: 10px; }
.dialog-head h3 { margin: 4px 0 0; font: 700 20px var(--font-display); }
.dialog-head button { border: 0; background: transparent; color: var(--muted); cursor: pointer; }
.stage { padding: 8px 0 18px; }
.screen {
  width: min(520px, 88%);
  margin: 0 auto 18px;
  padding: 10px 0 14px;
  border-radius: 0 0 50% 50% / 0 0 28px 28px;
  background: linear-gradient(#ece7de, #c4bdb3);
  color: #5c564f;
  text-align: center;
  font-size: 12px;
  box-shadow: inset 0 -8px 16px rgba(0,0,0,.06);
}
.map-scroll { overflow: auto; padding-bottom: 6px; }
.seat-map {
  display: grid;
  gap: 6px 4px;
  justify-content: center;
  margin: 0 auto;
  min-width: min-content;
}
.row-label {
  display: grid;
  place-content: center;
  color: var(--muted);
  font-size: 11px;
}
.aisle { display: block; }
.seat-btn {
  height: 28px;
  min-width: 28px;
  padding: 0;
  border: 1px solid var(--line-strong);
  border-radius: 6px 6px 8px 8px;
  background: #fff;
  font-size: 9px;
  cursor: pointer;
}
.seat-btn.empty, .seat-btn.taken, .seat-btn.sold, .seat-btn.held {
  opacity: .34;
  background: #d8d3cb;
  border-color: #c9c3ba;
  color: transparent;
  cursor: not-allowed;
}
.seat-btn.held { background: repeating-linear-gradient(45deg, #d8d3cb, #d8d3cb 4px, #c9c3ba 4px, #c9c3ba 8px); }
.seat-btn.dimmed { opacity: .28; }
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 18px;
  margin: 8px 0 0;
  padding: 14px 0 0;
  border-top: 1px dashed var(--line-strong);
  list-style: none;
  color: var(--muted);
  font-size: 12px;
}
.legend li { cursor: pointer; }
.legend li.active { color: var(--ink); font-weight: 700; }
.legend i {
  display: inline-block;
  width: 12px;
  height: 12px;
  margin-right: 6px;
  border-radius: 3px;
  vertical-align: -1px;
}
.legend .sold { background: #d8d3cb; }
.legend .held {
  background: repeating-linear-gradient(45deg, #d8d3cb, #d8d3cb 3px, #b9b3aa 3px, #b9b3aa 6px);
}
.legend .picked { background: #2f9e6d; }
.picker-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.picker-footer p { margin: 0; color: var(--muted); font-size: 13px; }
.picker-footer strong { color: var(--red); font-size: 22px; }
.picker-footer button {
  height: 44px;
  padding: 0 22px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: #fff;
  font-weight: 750;
  cursor: pointer;
}
.picker-footer button:disabled { opacity: .45; cursor: not-allowed; }
@media (max-width: 640px) {
  .picker-footer { flex-direction: column; align-items: stretch; }
  .picker-footer button { width: 100%; }
}
</style>
