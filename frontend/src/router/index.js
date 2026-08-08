import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/login', name: 'Login', component: () => import('../views/Login.vue'), meta: { title: '登录' } },
  { path: '/register', name: 'Register', component: () => import('../views/Register.vue'), meta: { title: '注册' } },
  {
    path: '/organizer',
    name: 'OrganizerConsole',
    component: () => import('../views/OrganizerConsole.vue'),
    meta: { title: '主办方工作台', requiresAuth: true },
  },
  {
    path: '/',
    component: () => import('../layouts/LayoutMain.vue'),
    children: [
      { path: '', name: 'Home', component: () => import('../views/Home.vue'), meta: { title: '发现活动' } },
      { path: 'events/:id', name: 'EventDetail', component: () => import('../views/EventDetail.vue'), meta: { title: '活动详情' } },
      { path: 'checkout/:eventId', name: 'Checkout', component: () => import('../views/Checkout.vue'), meta: { title: '确认购票', requiresAuth: true } },
      { path: 'cashier/:id', name: 'Cashier', component: () => import('../views/Cashier.vue'), meta: { title: '收银台', requiresAuth: true } },
      { path: 'rush-sales', name: 'RushSales', component: () => import('../views/RushSales.vue'), meta: { title: '限时开售' } },
      { path: 'orders', name: 'OrderList', component: () => import('../views/OrderList.vue'), meta: { title: '我的订单', requiresAuth: true } },
      { path: 'orders/:id', name: 'OrderDetail', component: () => import('../views/OrderDetail.vue'), meta: { title: '订单详情', requiresAuth: true } },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach((to, from, next) => {
  const token = localStorage.getItem('access_token') || localStorage.getItem('token')
  const role = localStorage.getItem('role')
  const isAuthPage = to.path === '/login' || to.path === '/register'

  if (!token && to.meta.requiresAuth) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }
  if (token && isAuthPage) {
    next('/')
    return
  }
  document.title = `${to.meta.title || '发现活动'} · Gofun`
  next()
})

export default router
