<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  organizerId: { type: [String, Number], required: true },
  events: { type: Array, default: () => [] },
})

const emit = defineEmits(['update:modelValue', 'created'])

const submitting = ref(false)
const form = reactive({
  ticket_tier_id: '',
  name: '',
  rush_price_yuan: 99,
  total_quota: 20,
  per_user_limit: 1,
  starts_at: null,
  ends_at: null,
})

const tierOptions = computed(() => {
  const rows = []
  for (const event of props.events) {
    if (event.status !== 'published' || event.sale_mode === 'seated') continue
    for (const session of event.sessions || []) {
      for (const tier of session.ticket_tiers || []) {
        if (tier.status === 'disabled') continue
        rows.push({
          id: String(tier.id),
          eventTitle: event.title,
          name: tier.name,
          remaining: Number(tier.remaining_quota || 0),
          purchaseLimit: Number(tier.purchase_limit || 1),
          priceCents: Number(tier.price_cents || 0),
        })
      }
    }
  }
  return rows
})

const selectedTier = computed(() =>
  tierOptions.value.find(item => item.id === String(form.ticket_tier_id)) || null,
)

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    const first = tierOptions.value[0]
    form.ticket_tier_id = first?.id || ''
    form.name = first ? `${first.eventTitle} 限时开售` : ''
    form.rush_price_yuan = first ? Math.max(1, Math.floor(first.priceCents / 100) - 20) : 99
    form.total_quota = Math.min(20, first?.remaining || 20)
    form.per_user_limit = Math.min(1, first?.purchaseLimit || 1)
    form.starts_at = new Date(Date.now() + 5 * 60 * 1000)
    form.ends_at = new Date(Date.now() + 24 * 60 * 60 * 1000)
  },
)

function close() {
  emit('update:modelValue', false)
}

async function submit() {
  const tier = selectedTier.value
  if (!tier) {
    ElMessage.warning('请选择已发布的计数票档；选座活动不能开售')
    return
  }
  const priceCents = Math.round(Number(form.rush_price_yuan) * 100)
  if (!form.name.trim() || !form.starts_at || !form.ends_at) {
    ElMessage.warning('请完整填写开售名称和时间')
    return
  }
  submitting.value = true
  try {
    await api.organizerCreateRushSale(props.organizerId, {
      ticket_tier_id: String(tier.id),
      name: form.name.trim(),
      rush_price_cents: priceCents,
      total_quota: Number(form.total_quota),
      per_user_limit: Number(form.per_user_limit),
      starts_at: new Date(form.starts_at).toISOString(),
      ends_at: new Date(form.ends_at).toISOString(),
    })
    ElMessage.success('限时开售已创建')
    emit('created')
    close()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '创建开售失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="配置限时开售"
    width="min(560px, 92vw)"
    destroy-on-close
    @close="close"
  >
    <el-form label-position="top">
      <el-form-item label="票档">
        <el-select v-model="form.ticket_tier_id" placeholder="选择已发布的计数票档">
          <el-option
            v-for="tier in tierOptions"
            :key="tier.id"
            :label="`${tier.eventTitle} · ${tier.name} · 余 ${tier.remaining} · ¥${(tier.priceCents / 100).toFixed(0)}`"
            :value="tier.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="开售名称">
        <el-input v-model="form.name" maxlength="128" />
      </el-form-item>
      <div class="paired">
        <el-form-item label="抢票价（元）">
          <el-input-number v-model="form.rush_price_yuan" :min="0.01" :precision="2" />
        </el-form-item>
        <el-form-item label="开售票额">
          <el-input-number v-model="form.total_quota" :min="1" :max="selectedTier?.remaining || 1" />
        </el-form-item>
      </div>
      <el-form-item label="每人限购">
        <el-input-number v-model="form.per_user_limit" :min="1" :max="selectedTier?.purchaseLimit || 1" />
      </el-form-item>
      <div class="paired">
        <el-form-item label="开始时间">
          <el-date-picker v-model="form.starts_at" type="datetime" placeholder="开售时间" />
        </el-form-item>
        <el-form-item label="结束时间">
          <el-date-picker v-model="form.ends_at" type="datetime" placeholder="结束时间" />
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="submitting" :disabled="!tierOptions.length" @click="submit">
        创建开售
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.paired { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.paired :deep(.el-date-picker), .paired :deep(.el-input-number) { width: 100%; }
.hint { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.7; }
@media (max-width: 600px) {
  .paired { grid-template-columns: 1fr; }
}
</style>
