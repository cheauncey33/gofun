export function salePhase(sale, now = Date.now()) {
  if (!sale) return 'unavailable'
  const remaining = Number(sale.remaining_quota || 0)
  const total = Number(sale.total_quota || 0)
  const start = sale.starts_at ? new Date(sale.starts_at).getTime() : 0
  const end = sale.ends_at ? new Date(sale.ends_at).getTime() : Infinity
  if (remaining <= 0) return 'sold_out'
  if (start && now < start) return 'scheduled'
  if (Number.isFinite(end) && now > end) return 'ended'
  if (total > 0 && remaining / total <= 0.25) return 'ending'
  return 'live'
}

export function phaseLabel(phase) {
  return {
    live: '抢票中',
    ending: '即将售罄',
    scheduled: '即将开售',
    sold_out: '已抢光',
    ended: '已结束',
  }[phase] || ''
}

export function canRush(sale, now = Date.now()) {
  const phase = salePhase(sale, now)
  return phase === 'live' || phase === 'ending'
}

export function matchesRushFilter(sale, filter, now = Date.now()) {
  if (filter === 'live') return canRush(sale, now)
  if (filter === 'scheduled') return salePhase(sale, now) === 'scheduled'
  return true
}
