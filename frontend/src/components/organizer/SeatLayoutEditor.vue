<script setup>
import { computed } from 'vue'

const props = defineProps({
  rows: { type: Number, default: 8 },
  cols: { type: Number, default: 12 },
  cells: { type: Object, default: () => ({}) },
  tiers: { type: Array, default: () => [] },
  paintTierId: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
})

const emit = defineEmits(['update:rows', 'update:cols', 'update:cells', 'update:paintTierId'])

const seatCount = computed(() => Object.keys(props.cells || {}).length)

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

function toggleCell(row, col) {
  if (props.disabled || !props.paintTierId) return
  const key = cellKey(row, col)
  const next = { ...props.cells }
  if (next[key] === props.paintTierId) delete next[key]
  else next[key] = props.paintTierId
  emit('update:cells', next)
}
</script>

<template>
  <div class="layout-editor">
    <div class="paired-fields">
      <label>行数
        <el-input-number
          :model-value="rows"
          :min="1"
          :max="16"
          :disabled="disabled"
          @update:model-value="emit('update:rows', $event)"
        />
      </label>
      <label>列数
        <el-input-number
          :model-value="cols"
          :min="1"
          :max="24"
          :disabled="disabled"
          @update:model-value="emit('update:cols', $event)"
        />
      </label>
    </div>
    <label>当前涂色票档
      <el-select
        :model-value="paintTierId"
        :disabled="disabled"
        @update:model-value="emit('update:paintTierId', $event)"
      >
        <el-option v-for="tier in tiers" :key="tier.id" :label="tier.name" :value="String(tier.id)" />
      </el-select>
    </label>
    <p class="hint">
      点击格子分配到当前票档，再点一次取消。空白格不出售。已涂 {{ seatCount }} 座。
      <template v-if="disabled">发布后不能改厅图，请先下架。</template>
    </p>
    <div class="seat-grid" :style="{ gridTemplateColumns: `repeat(${cols}, 28px)` }">
      <button
        v-for="col in cols"
        :key="'h' + col"
        class="seat-cell head"
        type="button"
        disabled
      >{{ col }}</button>
      <template v-for="row in rows" :key="row">
        <button
          v-for="col in cols"
          :key="`${row}-${col}`"
          class="seat-cell"
          type="button"
          :class="{ painted: !!cells[cellKey(row, col)] }"
          :disabled="disabled"
          :title="`${rowLabel(row)}${col}`"
          @click="toggleCell(row, col)"
        >{{ cells[cellKey(row, col)] ? rowLabel(row) : '' }}</button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.layout-editor { display: grid; gap: 14px; }
.paired-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
label { display: grid; gap: 8px; font-size: 13px; font-weight: 650; }
label :deep(.el-select), label :deep(.el-input-number) { width: 100%; }
.hint { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.7; }
.seat-grid { display: grid; gap: 4px; justify-content: center; overflow: auto; padding-bottom: 8px; }
.seat-cell {
  width: 28px; height: 28px; padding: 0; border: 1px solid var(--line-strong);
  border-radius: 4px; background: transparent; cursor: pointer; font-size: 10px;
}
.seat-cell.head { border: 0; background: transparent; color: var(--muted); cursor: default; }
.seat-cell.painted { background: rgba(181,52,41,.18); border-color: var(--red); color: var(--red); }
.seat-cell:disabled:not(.head) { cursor: default; opacity: .7; }
@media (max-width: 600px) {
  .paired-fields { grid-template-columns: 1fr; }
  .seat-grid { justify-content: start; }
}
</style>
