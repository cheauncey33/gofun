<script setup>
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../api'

const props = defineProps({
  modelValue: { type: String, default: '' },
  label: { type: String, default: '封面图' },
  variant: { type: String, default: 'cover' },
  successText: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue'])
const uploading = ref(false)
const fileInput = ref(null)

const preview = computed(() => String(props.modelValue || '').trim())

async function onFileChange(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  if (file.size > 2 * 1024 * 1024) {
    ElMessage.warning('图片需小于 2MB')
    return
  }
  uploading.value = true
  try {
    const res = await api.uploadImage(file)
    emit('update:modelValue', res.data?.url || '')
    ElMessage.success(props.successText || (props.variant === 'avatar' ? '头像已上传' : '封面已上传'))
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || (props.variant === 'avatar' ? '头像上传失败' : '封面上传失败'))
  } finally {
    uploading.value = false
  }
}

function clearCover() {
  emit('update:modelValue', '')
}
</script>

<template>
  <div class="cover-upload" :class="variant">
    <span class="cover-label">{{ label }}</span>
    <div class="cover-row">
      <div class="cover-preview" :class="{ empty: !preview, round: variant === 'avatar' }">
        <img v-if="preview" :src="preview" :alt="variant === 'avatar' ? '头像预览' : '封面预览'" />
        <span v-else>尚未上传</span>
      </div>
      <div class="cover-actions">
        <input
          ref="fileInput"
          type="file"
          accept="image/jpeg,image/png,image/webp,image/gif"
          hidden
          @change="onFileChange"
        />
        <button type="button" :disabled="uploading" @click="fileInput?.click()">
          {{ uploading ? '上传中…' : (preview ? '更换图片' : '上传图片') }}
        </button>
        <button v-if="preview" type="button" class="ghost" @click="clearCover">移除</button>
        <p>{{ variant === 'avatar' ? 'JPG / PNG / WebP，不超过 2MB。' : 'JPG / PNG / WebP，不超过 2MB。也可继续粘贴外链。' }}</p>
      </div>
    </div>
    <el-input
      :model-value="modelValue"
      maxlength="512"
      placeholder="https:// 或上传后自动填写"
      @update:model-value="emit('update:modelValue', $event)"
    />
  </div>
</template>

<style scoped>
.cover-upload { display: grid; gap: 8px; }
.cover-label { font-size: 13px; font-weight: 650; }
.cover-row { display: grid; grid-template-columns: 112px 1fr; gap: 14px; align-items: center; }
.cover-preview {
  width: 112px;
  height: 84px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: rgba(255,255,255,.4);
  display: grid;
  place-content: center;
  color: var(--muted);
  font-size: 12px;
}
.cover-preview img { width: 100%; height: 100%; object-fit: cover; }
.cover-preview.round { width: 72px; height: 72px; border-radius: 50%; }
.cover-upload.avatar .cover-row { grid-template-columns: 72px 1fr; }
.cover-actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.cover-actions button {
  height: 34px;
  padding: 0 14px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: #fff;
  font-weight: 650;
  cursor: pointer;
}
.cover-actions .ghost { background: transparent; color: var(--muted); }
.cover-actions p { margin: 0; flex-basis: 100%; color: var(--muted); font-size: 12px; }
@media (max-width: 600px) {
  .cover-row { grid-template-columns: 1fr; }
}
</style>
