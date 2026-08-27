const VISITOR_STORAGE_KEY = 'gofun_visitor_id'

function fallbackVisitorId() {
  const random = Math.random().toString(36).slice(2)
  return `v-${Date.now().toString(36)}-${random}`
}

export function getVisitorId() {
  try {
    const existing = localStorage.getItem(VISITOR_STORAGE_KEY)
    if (existing) return existing
    const created = globalThis.crypto?.randomUUID?.() || fallbackVisitorId()
    localStorage.setItem(VISITOR_STORAGE_KEY, created)
    return created
  } catch {
    return fallbackVisitorId()
  }
}
