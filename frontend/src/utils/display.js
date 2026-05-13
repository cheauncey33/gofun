export function isGarbledText(value) {
  if (!value || typeof value !== 'string') return true
  const text = value.trim()
  if (!text) return true
  if (/[�锟]/.test(text)) return true
  if (/\?{2,}/.test(text)) return true
  if (/\(.*\?{2,}.*\)/.test(text)) return true
  return false
}

export function productName(product, fallback = '零食') {
  const id = product?.id ?? product?.ID ?? product?.product_id ?? product?.ProductID
  const name = product?.name ?? product?.Name ?? product?.product_name
  if (isGarbledText(name)) return id ? `${fallback} #${id}` : fallback
  return name
}

export function money(value) {
  const n = Number(value || 0)
  return `¥${n.toFixed(2)}`
}

export const snackPlaceholder = '/snack-placeholder.svg'

export function imageUrl(value) {
  if (!value || typeof value !== 'string') return snackPlaceholder
  const text = value.trim()
  if (!text || isGarbledText(text)) return snackPlaceholder
  return text
}

export function shortDateTime(value) {
  if (!value) return '-'
  return String(value).replace('T', ' ').slice(0, 19)
}

export const orderStatusText = {
  1: '待支付',
  2: '已支付',
  3: '配送中',
  4: '已完成',
  5: '已取消',
  6: '退款中',
  7: '已退款',
}

export const orderStatusTag = {
  1: 'warning',
  2: 'primary',
  3: 'primary',
  4: 'success',
  5: 'info',
  6: 'danger',
  7: 'info',
}
