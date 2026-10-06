import api from '../api'

/**
 * 判断主办方列表中是否存在「已激活且通过审核」的工作台。
 * 放在 utils 里是为了让 stores/session 依赖 utils/auth，形成单向依赖：
 * session 需要在登录态变化时让身份缓存失效，而 auth 不能反向依赖 session。
 */
export function hasApprovedOrganizerWorkspace(list) {
  return (Array.isArray(list) ? list : []).some((item) => {
    const organizer = item?.organizer || item
    return organizer?.status === 'active' && organizer?.audit_status === 'approved'
  })
}

// 授权判断的唯一来源是后端接口。
// localStorage 里的 role 只用于首屏渲染提示（例如立即显示管理入口），
// 因为它可被用户在浏览器里直接改写，绝不能用来决定放行。
//
// 这里缓存的是「后端确认过的结果」而非本地猜测，TTL 到期后重新向后端确认。
const IDENTITY_TTL_MS = 60 * 1000

let identityCache = null
let identityAt = 0
let organizerCache = null
let organizerAt = 0

function cacheValid(entry, at) {
  return entry !== null && Date.now() - at < IDENTITY_TTL_MS
}

function parseIdentity(data) {
  return {
    authenticated: true,
    userID: data?.id ?? null,
    username: data?.username || '',
    role: String(data?.role || '').toLowerCase(),
  }
}

const UNAUTHENTICATED = { authenticated: false, userID: null, username: '', role: '' }

/**
 * 向后端确认当前调用者身份。
 * @param {boolean} force 是否跳过缓存（登录态刚变化时用）
 */
export async function resolveIdentity({ force = false } = {}) {
  if (!force && cacheValid(identityCache, identityAt)) {
    return identityCache
  }
  try {
    const res = await api.getUserInfo()
    identityCache = parseIdentity(res.data)
  } catch (error) {
    // 401/403 或刷新令牌失败都按未登录处理，由调用方决定跳转。
    identityCache = UNAUTHENTICATED
  }
  identityAt = Date.now()
  return identityCache
}

/**
 * 确认当前用户是否拥有可用（已激活且通过审核）的主办方工作台。
 */
export async function resolveOrganizerAccess({ force = false } = {}) {
  if (!force && cacheValid(organizerCache, organizerAt)) {
    return organizerCache
  }
  try {
    const res = await api.organizerGetMine()
    const list = res.data?.list ?? res.data?.organizers ?? res.data ?? []
    organizerCache = { ...(await resolveIdentity({ force })), organizer: hasApprovedOrganizerWorkspace(list) }
  } catch (error) {
    organizerCache = { ...UNAUTHENTICATED, organizer: false }
  }
  organizerAt = Date.now()
  return organizerCache
}

/**
 * 登录、登出、切换账号后必须调用：让下一次导航重新向后端确认。
 */
export function invalidateIdentity() {
  identityCache = null
  identityAt = 0
  organizerCache = null
  organizerAt = 0
}

export function isAdmin(identity) {
  return Boolean(identity?.authenticated) && identity.role === 'admin'
}
