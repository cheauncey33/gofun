<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { Camera, CircleCheck, Warning } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import api from '../../api'

const props = defineProps({
  organizerId: { type: [String, Number], required: true },
})

const credential = ref('')
const sessionFilter = ref('')
const submitting = ref(false)
const recordsLoading = ref(false)
const records = ref([])
const result = ref(null)
const scanning = ref(false)
const videoRef = ref(null)
let mediaStream = null
let scanTimer = null

const todayStats = computed(() => {
  const start = new Date()
  start.setHours(0, 0, 0, 0)
  const today = records.value.filter(item => new Date(item.verified_at) >= start)
  return {
    success: today.filter(item => item.result === 'success').length,
    failed: today.filter(item => item.result !== 'success').length,
  }
})

const resultMeta = computed(() => {
  const value = result.value?.result
  if (value === 'success') return { tone: 'success', title: '核销成功', icon: CircleCheck }
  if (value === 'already_used') return { tone: 'warning', title: '请勿重复放行', icon: Warning }
  if (value === 'revoked') return { tone: 'danger', title: '该票已作废', icon: Warning }
  return { tone: 'danger', title: '核销未通过', icon: Warning }
})

watch(() => props.organizerId, async value => {
  stopScanner()
  result.value = null
  credential.value = ''
  if (value) await loadRecords()
}, { immediate: true })

onBeforeUnmount(stopScanner)

async function verify(value = credential.value) {
  const normalized = String(value || '').trim()
  if (!normalized) {
    ElMessage.warning('请扫描二维码或输入完整票码')
    return
  }
  submitting.value = true
  try {
    const response = await api.organizerVerifyTicket(
      props.organizerId,
      normalized,
      sessionFilter.value || undefined,
    )
    result.value = response.data
    credential.value = ''
    await loadRecords()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '核销请求失败')
  } finally {
    submitting.value = false
  }
}

async function loadRecords() {
  if (!props.organizerId) return
  recordsLoading.value = true
  try {
    const response = await api.organizerGetVerifications(props.organizerId, {
      page: 1,
      page_size: 30,
    })
    records.value = response.data?.list || []
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '核销记录加载失败')
  } finally {
    recordsLoading.value = false
  }
}

async function startScanner() {
  if (!('BarcodeDetector' in window) || !navigator.mediaDevices?.getUserMedia) {
    ElMessage.info('当前浏览器不支持直接扫码，请使用下方手动输入')
    return
  }
  try {
    mediaStream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: { ideal: 'environment' } },
      audio: false,
    })
    scanning.value = true
    await nextTick()
    videoRef.value.srcObject = mediaStream
    await videoRef.value.play()
    const detector = new window.BarcodeDetector({ formats: ['qr_code'] })
    scanTimer = window.setInterval(async () => {
      if (!videoRef.value || submitting.value) return
      try {
        const codes = await detector.detect(videoRef.value)
        if (codes[0]?.rawValue) {
          const value = codes[0].rawValue
          stopScanner()
          await verify(value)
        }
      } catch {
        // 摄像头画面尚未就绪时下一帧继续尝试。
      }
    }, 350)
  } catch {
    stopScanner()
    ElMessage.warning('无法使用摄像头，请检查权限或改用手动输入')
  }
}

function stopScanner() {
  if (scanTimer) window.clearInterval(scanTimer)
  scanTimer = null
  mediaStream?.getTracks().forEach(track => track.stop())
  mediaStream = null
  scanning.value = false
}

function formatDate(value) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}

function resultLabel(value) {
  return {
    success: '核销成功',
    already_used: '重复扫码',
    revoked: '已作废',
    invalid_credential: '无效票码',
    wrong_organizer: '非本主办方',
    not_found: '票不存在',
  }[value] || value
}

function resultType(value) {
  if (value === 'success') return 'success'
  if (value === 'already_used') return 'warning'
  return 'danger'
}
</script>

<template>
  <section class="verification-grid">
    <div class="verification-station">
      <header>
        <div>
          <h3>现场核销</h3>
          <p>扫码与手动输入走同一个后端校验流程。</p>
        </div>
        <el-button v-if="!scanning" :icon="Camera" @click="startScanner">打开摄像头</el-button>
        <el-button v-else @click="stopScanner">关闭摄像头</el-button>
      </header>

      <div v-if="scanning" class="camera-frame">
        <video ref="videoRef" muted playsinline />
        <span>将电子票二维码放入取景框</span>
      </div>

      <label class="manual-label" for="session-filter">场次约束（可选）</label>
      <el-input
        id="session-filter"
        v-model="sessionFilter"
        clearable
        placeholder="填写 session_id 后只核销该场次的票"
      />

      <label class="manual-label" for="ticket-credential">手动输入票码</label>
      <div class="manual-row">
        <el-input
          id="ticket-credential"
          v-model="credential"
          clearable
          placeholder="粘贴以 FC1. 开头的完整票码"
          @keyup.enter="verify()"
        />
        <el-button type="primary" :loading="submitting" @click="verify()">核销</el-button>
      </div>

      <div v-if="result" class="verification-result" :class="resultMeta.tone">
        <el-icon><component :is="resultMeta.icon" /></el-icon>
        <div>
          <strong>{{ resultMeta.title }}</strong>
          <p>{{ result.message }}</p>
          <small v-if="result.ticket">
            {{ result.ticket.order_item?.event_title_snapshot }} ·
            {{ result.ticket.order_item?.tier_name_snapshot }} ·
            {{ result.ticket.ticket_no }}
          </small>
        </div>
      </div>
    </div>

    <div v-loading="recordsLoading" class="verification-records">
      <header>
        <div>
          <h3>最近核销记录</h3>
          <p>今日成功 {{ todayStats.success }} · 失败 {{ todayStats.failed }}；成功与失败均保留。</p>
        </div>
      </header>
      <el-table :data="records" empty-text="还没有核销记录">
        <el-table-column label="结果" width="96">
          <template #default="{ row }">
            <el-tag :type="resultType(row.result)" effect="plain">{{ resultLabel(row.result) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="电子票" min-width="170">
          <template #default="{ row }">
            {{ row.ticket?.ticket_no || '未识别票码' }}
          </template>
        </el-table-column>
        <el-table-column label="时间" width="145">
          <template #default="{ row }">{{ formatDate(row.verified_at) }}</template>
        </el-table-column>
      </el-table>
    </div>
  </section>
</template>

<style scoped>
.verification-grid {
  display: grid;
  grid-template-columns: minmax(340px, .82fr) minmax(440px, 1.18fr);
  border: 1px solid var(--line-strong);
  background: rgba(255,255,255,.2);
}
.verification-station, .verification-records { min-width: 0; padding: 24px; }
.verification-station { border-right: 1px solid var(--line); }
header { min-height: 46px; display: flex; justify-content: space-between; align-items: start; gap: 18px; }
h3 { margin: 0; font: 720 18px var(--font-display); }
header p { margin: 6px 0 0; color: var(--muted); font-size: 12px; }
.manual-label { display: block; margin: 28px 0 9px; color: var(--muted); font-size: 12px; }
.manual-row { display: grid; grid-template-columns: 1fr auto; gap: 10px; }
.camera-frame { margin-top: 20px; min-height: 260px; background: #171310; position: relative; overflow: hidden; }
.camera-frame video { width: 100%; height: 300px; display: block; object-fit: cover; }
.camera-frame::after { content: ''; position: absolute; inset: 48px 76px; border: 2px solid rgba(255,255,255,.85); box-shadow: 0 0 0 999px rgba(0,0,0,.22); }
.camera-frame span { position: absolute; left: 0; right: 0; bottom: 16px; z-index: 1; color: white; text-align: center; font-size: 12px; }
.verification-result { margin-top: 20px; padding: 18px; border-left: 3px solid; display: flex; gap: 13px; background: rgba(255,255,255,.45); }
.verification-result .el-icon { margin-top: 2px; font-size: 24px; }
.verification-result strong { font: 700 18px var(--font-display); }
.verification-result p { margin: 5px 0; font-size: 13px; }
.verification-result small { color: var(--muted); }
.verification-result.success { border-color: #4d8b59; color: #2d713c; }
.verification-result.warning { border-color: #c57b17; color: #955a0b; }
.verification-result.danger { border-color: var(--red); color: var(--red); }
.verification-records :deep(.el-table),
.verification-records :deep(.el-table tr),
.verification-records :deep(.el-table th.el-table__cell) { background: transparent; }
.verification-records :deep(.el-table) { margin-top: 15px; }
@media (max-width: 1050px) {
  .verification-grid { grid-template-columns: 1fr; }
  .verification-station { border-right: 0; border-bottom: 1px solid var(--line); }
}
@media (max-width: 600px) {
  .verification-station, .verification-records { padding: 18px; }
  .manual-row { grid-template-columns: 1fr; }
  .manual-row .el-button { width: 100%; }
  .camera-frame::after { inset: 38px; }
}
</style>
