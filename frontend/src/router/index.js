import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/login', name: 'Login', component: () => import('../views/Login.vue') },
  { path: '/register', name: 'Register', component: () => import('../views/Register.vue') },
  {
    path: '/',
    component: () => import('../layouts/LayoutMain.vue'),
    children: [
      { path: '', name: 'Home', component: () => import('../views/Home.vue'), meta: { title: '商品浏览' } },
      { path: 'product/:id', name: 'ProductDetail', component: () => import('../views/ProductDetail.vue'), meta: { title: '商品详情' } },
      { path: 'orders', name: 'OrderList', component: () => import('../views/OrderList.vue'), meta: { title: '我的订单' } },
      { path: 'orders/:id', name: 'OrderDetail', component: () => import('../views/OrderDetail.vue'), meta: { title: '订单详情' } },
      { path: 'addresses', name: 'AddressList', component: () => import('../views/AddressList.vue'), meta: { title: '收货地址' } },
      { path: 'profile', name: 'UserCenter', component: () => import('../views/UserCenter.vue'), meta: { title: '个人中心' } },
      { path: 'seckill', name: 'SeckillList', component: () => import('../views/SeckillList.vue'), meta: { title: '秒杀活动' } },
      { path: 'seckill/:id', name: 'SeckillDetail', component: () => import('../views/SeckillDetail.vue'), meta: { title: '秒杀详情' } },
      { path: 'admin/dashboard', name: 'AdminDashboard', component: () => import('../views/admin/Dashboard.vue'), meta: { title: '数据看板', requiresAdmin: true } },
      { path: 'admin/products', name: 'AdminProducts', component: () => import('../views/admin/ProductAdmin.vue'), meta: { title: '商品管理', requiresAdmin: true } },
      { path: 'admin/orders', name: 'AdminOrders', component: () => import('../views/admin/OrderAdmin.vue'), meta: { title: '订单管理', requiresAdmin: true } },
      { path: 'admin/users', name: 'AdminUsers', component: () => import('../views/admin/UserAdmin.vue'), meta: { title: '用户管理', requiresAdmin: true } },
      { path: 'admin/seckill', name: 'AdminSeckill', component: () => import('../views/admin/SeckillAdmin.vue'), meta: { title: '秒杀管理', requiresAdmin: true } },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach((to, from, next) => {
  const token = localStorage.getItem('access_token') || localStorage.getItem('token')
  const role = localStorage.getItem('role')
  const isAuthPage = to.path === '/login' || to.path === '/register'

  if (!token && !isAuthPage) {
    next('/login')
    return
  }
  if (token && isAuthPage) {
    next('/')
    return
  }
  if (to.meta.requiresAdmin && role !== 'admin') {
    next('/')
    return
  }
  next()
})

export default router
