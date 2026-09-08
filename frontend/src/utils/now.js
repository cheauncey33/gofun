import { onMounted, onUnmounted, ref } from 'vue'

export function useNow(intervalMs = 1000) {
  const nowTs = ref(Date.now())
  let timer = 0
  onMounted(() => {
    nowTs.value = Date.now()
    timer = window.setInterval(() => {
      nowTs.value = Date.now()
    }, intervalMs)
  })
  onUnmounted(() => {
    window.clearInterval(timer)
  })
  return nowTs
}
