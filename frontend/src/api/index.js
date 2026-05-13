import axios from 'axios'

let _router = null
export function setRouter(r) { _router = r }

const api = axios.create({ baseURL: '/api/v1', timeout: 15000 })

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) config.headers.Authorization = token
  return config
})

api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('username')
      localStorage.removeItem('role')
      if (_router) _router.push('/login')
      else window.location.href = '/login'
    }
    return Promise.reject(err)
  }
)

const get = (url, params) => api.get(url, { params }).then(r => r.data)
const post = (url, data) => api.post(url, data).then(r => r.data)
const put = (url, data) => api.put(url, data).then(r => r.data)
const del = (url) => api.delete(url).then(r => r.data)

export default {
  // Auth
  login: (username, password) => post('/login', { username, password }),
  register: (username, password, dorm_id) => post('/register', { username, password, dorm_id }),

  // Products
  getProducts: (params) => get('/products', params),
  getProductDetail: (id) => get(`/products/${id}`),
  getCategories: () => get('/categories'),
  getCategoryProducts: (id, params) => get(`/categories/${id}/products`, params),

  // Orders
  createOrder: (items) => post('/orders', { items }),
  getOrders: (params) => get('/orders', params),
  getOrderDetail: (id) => get(`/orders/${id}`),
  cancelOrder: (id, reason) => post(`/orders/${id}/cancel`, { reason }),
  refundOrder: (id, reason) => post(`/orders/${id}/refund`, { reason }),

  // Addresses
  getAddresses: () => get('/addresses'),
  createAddress: (data) => post('/addresses', data),
  updateAddress: (id, data) => put(`/addresses/${id}`, data),
  deleteAddress: (id) => del(`/addresses/${id}`),
  setDefaultAddress: (id) => put(`/addresses/${id}/default`),

  // User
  getUserInfo: () => get('/user/info'),
  updateUserInfo: (data) => put('/user/info', data),
  changePassword: (old_password, new_password) => put('/user/password', { old_password, new_password }),

  // Seckill
  getSeckillActivities: (params) => get('/seckill/activities', params),
  getSeckillDetail: (id) => get(`/seckill/activities/${id}`),
  getSeckillToken: (id) => post(`/seckill/activities/${id}/token`),
  executeSeckill: (id, token, quantity) => post(`/seckill/activities/${id}/execute`, { token, quantity }),

  // Admin
  getDashboard: () => get('/admin/dashboard'),
  adminCreateProduct: (data) => post('/admin/products', data),
  adminUpdateProduct: (id, data) => put(`/admin/products/${id}`, data),
  adminDeleteProduct: (id) => del(`/admin/products/${id}`),
  adminUpdateProductStatus: (id, status) => put(`/admin/products/${id}/status`, { status }),
  adminGetOrders: (params) => get('/admin/orders', params),
  adminUpdateOrderStatus: (id, status) => put(`/admin/orders/${id}/status`, { status }),
  adminGetUsers: (params) => get('/admin/users', params),
  adminUpdateUserRole: (id, role) => put(`/admin/users/${id}/role`, { role }),
  adminCreateSeckill: (data) => post('/admin/seckill', data),
  adminUpdateSeckill: (id, data) => put(`/admin/seckill/${id}`, data),
  adminDeleteSeckill: (id) => del(`/admin/seckill/${id}`),
  adminWarmUpSeckill: (id) => post(`/admin/seckill/${id}/warmup`),
}
