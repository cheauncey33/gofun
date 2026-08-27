<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../../api'
import CoverUpload from '../CoverUpload.vue'
import EventSalesPanel from './EventSalesPanel.vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  organizerId: { type: [String, Number], required: true },
  event: { type: Object, default: null },
  venues: { type: Array, default: () => [] },
})

const emit = defineEmits(['update:modelValue', 'saved'])

const submitting = ref(false)
const isDraft = computed(() => props.event?.status === 'draft')
const form = reactive({
  title: '',
  subtitle: '',
  category: '',
  cover_url: '',
  description: '',
  max_tickets_per_order: 6,
  real_name_required: false,
  sale_mode: 'counter',
})

watch(
  () => props.modelValue,
  (visible) => {
    if (!visible || !props.event) return
    Object.assign(form, {
      title: props.event.title || '',
      subtitle: props.event.subtitle || '',
      category: props.event.category || '',
      cover_url: props.event.cover_url || '',
      description: props.event.description || '',
      max_tickets_per_order: props.event.max_tickets_per_order || 6,
      real_name_required: !!props.event.real_name_required,
      sale_mode: props.event.sale_mode || 'counter',
    })
  },
)

function close() {
  emit('update:modelValue', false)
}

async function save() {
  if (!form.title.trim() || !form.category.trim() || !form.description.trim()) {
    ElMessage.warning('请完整填写活动名称、分类和介绍')
    return
  }
  submitting.value = true
  try {
    await api.organizerUpdateEvent(props.organizerId, props.event.id, {
      title: form.title.trim(),
      subtitle: form.subtitle.trim(),
      category: form.category,
      cover_url: form.cover_url.trim(),
      description: form.description.trim(),
      max_tickets_per_order: form.max_tickets_per_order,
      real_name_required: form.real_name_required,
      sale_mode: form.sale_mode,
    })
    ElMessage.success('活动资料已更新')
    emit('saved')
    close()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '保存失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="编辑活动"
    width="min(720px, 94vw)"
    destroy-on-close
    @close="close"
  >
    <el-form label-position="top">
      <el-form-item label="活动名称">
        <el-input v-model="form.title" maxlength="160" show-word-limit />
      </el-form-item>
      <el-form-item label="副标题">
        <el-input v-model="form.subtitle" maxlength="256" />
      </el-form-item>
      <el-form-item label="活动分类">
        <el-select v-model="form.category" placeholder="请选择分类">
          <el-option
            v-for="item in ['音乐现场', '演唱会', '音乐节', '脱口秀', '展览', '戏剧', '体育']"
            :key="item"
            :label="item"
            :value="item"
          />
        </el-select>
      </el-form-item>
      <el-form-item>
        <CoverUpload v-model="form.cover_url" />
      </el-form-item>
      <el-form-item label="活动介绍">
        <el-input v-model="form.description" type="textarea" :rows="5" maxlength="1200" show-word-limit />
      </el-form-item>
      <div class="paired-fields">
        <el-form-item label="单笔限购">
          <el-input-number v-model="form.max_tickets_per_order" :min="1" :max="20" :disabled="!isDraft" />
        </el-form-item>
        <el-form-item label="实名制">
          <el-switch v-model="form.real_name_required" :disabled="!isDraft" />
        </el-form-item>
      </div>
      <el-form-item label="售卖方式">
        <el-select v-model="form.sale_mode" :disabled="!isDraft">
          <el-option label="计数售卖：展览 / 演唱会分区" value="counter" />
          <el-option label="必须选座：电影 / 脱口秀" value="seated" />
        </el-select>
      </el-form-item>
    </el-form>
    <h3 class="sales-heading">场次与票档</h3>
    <EventSalesPanel
      v-if="event"
      :event="event"
      :organizer-id="organizerId"
      :venues="venues"
      @saved="emit('saved')"
    />
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.paired-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.hint { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.7; }
.sales-heading { margin: 8px 0 12px; font: 700 16px var(--font-display); }
</style>
