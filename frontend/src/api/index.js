import axios from 'axios'

let _router = null
export function setRouter(r) { _router = r }

const api = axios.create({ baseURL: '/api/v1', timeout: 15000 })

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token') || localStorage.getItem('token')
  if (token) config.headers.Authorization = token
  return config
})

function clearAuthStorage() {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
}

let refreshPromise = null

api.interceptors.response.use(
  (res) => res,
  async (err) => {
    const original = err.config
    const isRefreshRequest = original?.url === '/auth/refresh'
    if (err.response?.status === 401 && original && !original._retry && !isRefreshRequest) {
      const refreshToken = localStorage.getItem('refresh_token')
      if (refreshToken) {
        original._retry = true
        try {
          refreshPromise ||= api.post('/auth/refresh', { refresh_token: refreshToken })
            .then((res) => {
              const data = res.data?.data
              localStorage.setItem('access_token', data.access_token)
              localStorage.setItem('refresh_token', data.refresh_token)
              localStorage.setItem('token', data.access_token)
              return data.access_token
            })
            .finally(() => {
              refreshPromise = null
            })
          const accessToken = await refreshPromise
          original.headers.Authorization = accessToken
          return api(original)
        } catch {}
      }
    }
    if (err.response?.status === 401) {
      clearAuthStorage()
      if (_router) _router.push('/login')
      else window.location.href = '/login'
    }
    return Promise.reject(err)
  }
)

const get = (url, params) => api.get(url, { params }).then(r => r.data)
const post = (url, data, config) => api.post(url, data, config).then(r => r.data)
const put = (url, data) => api.put(url, data).then(r => r.data)
const del = (url) => api.delete(url).then(r => r.data)
const newIdempotencyKey = () => {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

export default {
  // Auth
  login: (username, password) => post('/login', { username, password }),
  register: (username, password, dorm_id = 1) => post('/register', { username, password, dorm_id }),
  logout: (refresh_token) => post('/logout', { refresh_token }),

  // Event discovery
  getEvents: (params) => get('/events', params),
  getEventDetail: (id) => get(`/events/${id}`),
  getCatalogMeta: () => get('/catalog/meta'),
  getRushSales: () => get('/rush-sales'),
  getEventComments: (eventId, params) => get(`/events/${eventId}/comments`, params),
  createEventComment: (eventId, content) => post(`/events/${eventId}/comments`, { content }),
  deleteEventComment: (commentId) => del(`/comments/${commentId}`),
  likeEventComment: (commentId) => post(`/comments/${commentId}/like`),

  // Orders
  createOrder: (ticketTierId, quantity, purchaseInfo = {}, idempotencyKey = newIdempotencyKey()) =>
    post('/orders', { ticket_tier_id: String(ticketTierId), quantity, ...purchaseInfo }, {
      headers: { 'X-Idempotency-Key': idempotencyKey },
    }),
  getOrders: (params) => get('/orders', params),
  getOrderDetail: (id) => get(`/orders/${id}`),
  payOrder: (id, scenario = 'success') => post(`/orders/${id}/pay`, { scenario }),
  cancelOrder: (id, reason) => post(`/orders/${id}/cancel`, { reason }),

  // User
  getUserInfo: () => get('/user/info'),
  updateUserInfo: (data) => put('/user/info', data),
  changePassword: (old_password, new_password) => put('/user/password', { old_password, new_password }),

  // Rush sale — 到点直抢，一次 execute（无需前置 token）
  executeRushSale: (id, quantity, purchaseInfo = {}, idempotencyKey = newIdempotencyKey()) =>
    post(`/rush-sales/${id}/execute`, { quantity, ...purchaseInfo }, {
      headers: { 'X-Idempotency-Key': idempotencyKey },
    }),

  // Platform and organizer management APIs are ready for the phase-two console.
  adminGetOrganizers: (params) => get('/admin/organizers', params),
  adminCreateOrganizer: (data) => post('/admin/organizers', data),
  organizerGetMine: () => get('/organizers/mine'),
  organizerGetOverview: (organizerId) => get(`/organizers/${organizerId}/overview`),
  organizerGetVenues: (organizerId) => get(`/organizers/${organizerId}/venues`),
  organizerCreateVenue: (organizerId, data) => post(`/organizers/${organizerId}/venues`, data),
  organizerGetEvents: (organizerId, params) => get(`/organizers/${organizerId}/events`, params),
  organizerCreateEvent: (organizerId, data) => post(`/organizers/${organizerId}/events`, data),
  organizerCreateSession: (organizerId, eventId, data) =>
    post(`/organizers/${organizerId}/events/${eventId}/sessions`, data),
  organizerCreateTicketTier: (organizerId, sessionId, data) =>
    post(`/organizers/${organizerId}/sessions/${sessionId}/ticket-tiers`, data),
  organizerPublishEvent: (organizerId, eventId) =>
    post(`/organizers/${organizerId}/events/${eventId}/publish`),
  organizerUnpublishEvent: (organizerId, eventId) =>
    post(`/organizers/${organizerId}/events/${eventId}/unpublish`),
  organizerCancelEvent: (organizerId, eventId, data = {}) =>
    post(`/organizers/${organizerId}/events/${eventId}/cancel`, data),
  organizerBatchRefundEvent: (organizerId, eventId, data = {}) =>
    post(`/organizers/${organizerId}/events/${eventId}/refunds`, data),
  organizerDisableTicketTier: (organizerId, tierId) =>
    post(`/organizers/${organizerId}/ticket-tiers/${tierId}/disable`),
  organizerGetOrders: (organizerId, params) =>
    get(`/organizers/${organizerId}/orders`, params),
  organizerVerifyTicket: (organizerId, credential, sessionId) =>
    post(`/organizers/${organizerId}/verifications`, {
      credential,
      ...(sessionId ? { session_id: String(sessionId) } : {}),
    }),
  organizerGetVerifications: (organizerId, params) =>
    get(`/organizers/${organizerId}/verifications`, params),
}
