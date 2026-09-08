import { salePhase } from './rush'

export function isGarbledText(value) {
  if (!value || typeof value !== 'string') return true
  const text = value.trim()
  if (!text) return true
  if (/[�锟]/.test(text)) return true
  if (/\?{2,}/.test(text)) return true
  if (/\(.*\?{2,}.*\)/.test(text)) return true
  return false
}

export function productName(product, fallback = '演出') {
  const id = product?.id ?? product?.ID ?? product?.product_id ?? product?.ProductID
  const name = product?.name ?? product?.Name ?? product?.product_name
  if (isGarbledText(name)) return id ? `${fallback} #${id}` : fallback
  return name
}

export function money(value) {
  const n = Number(value || 0)
  return `¥${n.toFixed(2)}`
}

export function moneyCents(cents) {
  return money(Number(cents || 0) / 100)
}

export function rushStockLevel(sale) {
  const remaining = Number(sale?.remaining_quota || 0)
  const total = Number(sale?.total_quota || 0)
  if (remaining <= 0) return 'gone'
  const ratio = total > 0 ? remaining / total : 1
  if (remaining <= 5 || ratio <= 0.15) return 'low'
  if (ratio <= 0.4) return 'tight'
  return 'plenty'
}

export function rushStockLabel(sale, now = Date.now()) {
  const phase = salePhase(sale, now)
  if (phase !== 'live' && phase !== 'ending' && phase !== 'scheduled') return ''
  return {
    plenty: '余票充足',
    tight: '余票紧张',
    low: '所剩不多',
    gone: '已抢光',
  }[rushStockLevel(sale)] || '余票充足'
}

export const eventPlaceholder = '/event-placeholder.svg'

export function imageUrl(value) {
  if (!value || typeof value !== 'string') return eventPlaceholder
  const text = value.trim()
  if (!text || isGarbledText(text)) return eventPlaceholder
  return text
}

export function shortDateTime(value) {
  const ms = parseTimeMs(value)
  if (!Number.isFinite(ms)) return '-'
  const d = new Date(ms)
  const pad = n => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export function formatClock(value) {
  const ms = parseTimeMs(value)
  if (!Number.isFinite(ms)) return '--'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(ms))
}

export function parseTimeMs(value) {
  if (value == null || value === '') return NaN
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value < 1e12 ? value * 1000 : value
  }
  const parsed = Date.parse(String(value))
  return Number.isFinite(parsed) ? parsed : NaN
}

export const waitlistStatusText = {
  pending_payment: '待支付',
  queued: '候补排队中',
  fulfilled: '已配票',
  cancelled: '已取消',
  expired: '已截止退款',
}

export const ticketOrderStatusText = {
  queued: '排队确认中',
  pending_payment: '待支付',
  paid: '已支付',
  cancelled: '已取消',
  failed: '创建失败',
}

export function ticketOrderLabel(order) {
  if (order?.payment_status === 'refunded') return '已退款'
  if (order?.payment_status === 'refunding') return '退款中'
  return ticketOrderStatusText[order?.status] || order?.status || '—'
}

export function ticketOrderStatusClass(order) {
  if (order?.payment_status === 'refunded' || order?.payment_status === 'refunding') {
    return order.payment_status
  }
  return order?.status || ''
}

export function remainingSecondsUntil(expiresAt, expiresAtUnix, nowMs = Date.now(), orderId) {
  const candidates = []
  const unix = Number(expiresAtUnix)
  if (unix > 0) candidates.push(unix * 1000)
  const parsed = parseTimeMs(expiresAt)
  if (Number.isFinite(parsed)) candidates.push(parsed)
  const createdMs = snowflakeCreatedMs(orderId)
  if (Number.isFinite(createdMs)) candidates.push(createdMs + 15 * 60 * 1000)
  if (!candidates.length) return null
  const deadline = Math.max(...candidates)
  return Math.max(0, Math.floor((deadline - nowMs) / 1000))
}

function snowflakeCreatedMs(id) {
  if (id == null || id === '') return NaN
  try {
    const n = BigInt(String(id))
    if (n <= 0n) return NaN
    return Number((n >> 22n) + 1288834974657n)
  } catch {
    return NaN
  }
}

export const orderStatusText = {
  1: '待支付',
  2: '已支付',
  3: '已完成',
  5: '已取消',
}

export const orderStatusTag = {
  1: 'warning',
  2: 'primary',
  3: 'success',
  5: 'info',
}
