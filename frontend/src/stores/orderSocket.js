// 订单实时推送 WebSocket 客户端（单例）。
// 后端在订单落库/支付/取消/确认/超时/失败时主动推事件，替代前端轮询。
// 收到事件后：① 弹出通知提示用户；② 派发全局 'order:status' 事件，供订单页刷新列表。
import { ElNotification } from 'element-plus'

let socket = null
let reconnectTimer = null
let manualClose = false
let retryDelay = 1000
const MAX_RETRY_DELAY = 15000

// 不同事件对应的通知类型与默认标题
const EVENT_META = {
  created: { type: 'success', title: '下单成功' },
  paid: { type: 'success', title: '支付成功' },
  completed: { type: 'success', title: '订单完成' },
  cancelled: { type: 'info', title: '订单已取消' },
  timeout_cancelled: { type: 'warning', title: '订单超时' },
  failed: { type: 'error', title: '下单失败' },
}

function buildWsUrl(token) {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${window.location.host}/api/v1/ws?token=${encodeURIComponent(token)}`
}

// 尝试用 refresh_token 换取新的 access_token，成功返回 true
async function tryRefreshToken() {
  const refreshToken = localStorage.getItem('refresh_token')
  if (!refreshToken) return false
  try {
    const res = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    if (!res.ok) return false
    const data = await res.json()
    if (data?.data?.access_token) {
      localStorage.setItem('access_token', data.data.access_token)
      if (data.data.refresh_token) localStorage.setItem('refresh_token', data.data.refresh_token)
      return true
    }
    return false
  } catch {
    return false
  }
}

// 断线后尝试刷新 token 再重连；刷新失败说明登录态已失效，停止重连
async function scheduleReconnect() {
  if (manualClose) return
  clearTimeout(reconnectTimer)

  const refreshed = await tryRefreshToken()
  if (!refreshed) {
    // token 已失效，不再重连，等用户重新登录后由 LayoutMain 重新调用 connectOrderSocket
    return
  }

  reconnectTimer = setTimeout(() => {
    connectOrderSocket()
  }, retryDelay)
  // 指数退避，封顶 15s，避免后端短暂不可用时疯狂重连
  retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY)
}

function handleMessage(raw) {
  let ev
  try {
    ev = JSON.parse(raw)
  } catch {
    return
  }
  const meta = EVENT_META[ev.event] || { type: 'info', title: '订单状态更新' }
  ElNotification({
    type: meta.type,
    title: meta.title,
    message: ev.message || '',
    duration: 4000,
  })
  // 通知订单相关页面刷新数据
  window.dispatchEvent(new CustomEvent('order:status', { detail: ev }))
}

export function connectOrderSocket() {
  const token = localStorage.getItem('access_token') || localStorage.getItem('token')
  if (!token) return
  // 已有可用连接则不重复建立
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return
  }

  manualClose = false
  socket = new WebSocket(buildWsUrl(token))

  socket.onopen = () => {
    retryDelay = 1000 // 连接成功，重置退避
  }
  socket.onmessage = (e) => handleMessage(e.data)
  socket.onclose = () => {
    socket = null
    scheduleReconnect()
  }
  socket.onerror = () => {
    // 错误后 onclose 会触发重连，这里仅确保连接被关闭
    if (socket) socket.close()
  }
}

export function disconnectOrderSocket() {
  manualClose = true
  clearTimeout(reconnectTimer)
  retryDelay = 1000
  if (socket) {
    socket.close()
    socket = null
  }
}
