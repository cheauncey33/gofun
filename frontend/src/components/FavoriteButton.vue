<script setup>
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useFavorites } from '../stores/favorites'

const props = defineProps({
  type: { type: String, required: true },
  targetId: { type: [String, Number], required: true },
  compact: { type: Boolean, default: false },
})

const route = useRoute()
const router = useRouter()
const { isFavorited, toggle, ensureLoaded } = useFavorites()
const on = computed(() => isFavorited(props.type, props.targetId))

onMounted(() => {
  ensureLoaded()
})

async function onToggle(event) {
  event.stopPropagation()
  event.preventDefault()
  try {
    const result = await toggle(props.type, props.targetId)
    if (result?.needLogin) {
      router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
      return
    }
    ElMessage.success(result.favorited ? '已收藏' : '已取消收藏')
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '收藏失败')
  }
}
</script>

<template>
  <button
    class="fav-btn"
    :class="{ on, compact }"
    type="button"
    :aria-pressed="on"
    @click="onToggle"
  >{{ compact ? (on ? '已藏' : '收藏') : (on ? '已收藏' : '收藏') }}</button>
</template>

<style scoped>
.fav-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--ink);
  font: 650 12px inherit;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
}
.fav-btn.on {
  border-color: var(--red);
  color: var(--red);
  background: rgba(181, 52, 41, .06);
}
.fav-btn.compact { height: 28px; padding: 0 10px; }
</style>
