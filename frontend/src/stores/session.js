import { computed, reactive } from 'vue'
import api from '../api'
import { hasApprovedOrganizerWorkspace, invalidateIdentity } from '../utils/auth'

const state = reactive({
  token: localStorage.getItem('access_token') || localStorage.getItem('token') || '',
  username: localStorage.getItem('username') || '',
  role: localStorage.getItem('role') || '',
  hasOrganizerWorkspace: false,
})

export { hasApprovedOrganizerWorkspace }

export function setOrganizerWorkspace(value) {
  state.hasOrganizerWorkspace = Boolean(value)
}

export function applySession(data = {}) {
  const accessToken = data.access_token || data.token
  if (accessToken) {
    localStorage.setItem('access_token', accessToken)
    localStorage.setItem('token', accessToken)
    state.token = accessToken
  }
  if (data.refresh_token) localStorage.setItem('refresh_token', data.refresh_token)
  if (data.username != null) {
    localStorage.setItem('username', data.username)
    state.username = data.username
  }
  if (data.role != null) {
    localStorage.setItem('role', data.role)
    state.role = data.role
  }
  // 账号刚变化，下一次路由导航必须重新向后端确认身份，不能复用旧缓存。
  invalidateIdentity()
}

export function clearSession() {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
  state.token = ''
  state.username = ''
  state.role = ''
  state.hasOrganizerWorkspace = false
  invalidateIdentity()
}

export function isSafeAppPath(path) {
  if (typeof path !== 'string') return false
  const value = path.trim()
  if (!value.startsWith('/')) return false
  if (value.startsWith('//') || value.startsWith('/\\')) return false
  if (value.includes('\\') || value.includes('://')) return false
  return true
}

export function safeRedirectPath(path, fallback = '/') {
  return isSafeAppPath(path) ? path : fallback
}

export function endClientSession() {
  clearSession()
  import('./favorites').then((mod) => mod.resetFavoriteState()).catch(() => {})
  import('./orderSocket').then((mod) => mod.disconnectOrderSocket()).catch(() => {})
}

export async function logoutSession() {
  const refreshToken = localStorage.getItem('refresh_token')
  try {
    if (refreshToken) {
      await api.logout(refreshToken)
    }
  } catch {}
  endClientSession()
}

export function useSession() {
  return {
    token: computed(() => state.token),
    username: computed(() => state.username || '我的'),
    role: computed(() => state.role),
    isLoggedIn: computed(() => Boolean(state.token)),
    hasOrganizerWorkspace: computed(() => state.hasOrganizerWorkspace),
    applySession,
    clearSession,
    setOrganizerWorkspace,
    logoutSession,
  }
}
