import { createRouter, createWebHistory } from 'vue-router'
import { resolveIdentity, resolveOrganizerAccess, invalidateIdentity, isAdmin } from '../utils/auth'
import { endClientSession } from '../stores/session'

const routes = [
  { path: '/login', name: 'Login', component: () => import('../views/Login.vue'), meta: { title: '登录' } },
  { path: '/register', name: 'Register', component: () => import('../views/Register.vue'), meta: { title: '注册' } },
  {
    path: '/organizer',
    name: 'OrganizerConsole',
    component: () => import('../views/OrganizerConsole.vue'),
    meta: { title: '主办方工作台', requiresAuth: true, requiresOrganizer: true, blockAdmin: true },
  },
  {
    path: '/admin',
    name: 'AdminConsole',
    component: () => import('../views/AdminConsole.vue'),
    meta: { title: '平台管理', requiresAuth: true, requiresAdmin: true },
  },
  {
    path: '/',
    component: () => import('../layouts/LayoutMain.vue'),
    children: [
      { path: '', name: 'Home', component: () => import('../views/Home.vue'), meta: { title: '发现活动' } },
      { path: 'events/:id', name: 'EventDetail', component: () => import('../views/EventDetail.vue'), meta: { title: '活动详情' } },
      { path: 'checkout/:eventId', name: 'Checkout', component: () => import('../views/Checkout.vue'), meta: { title: '确认购票', requiresAuth: true } },
      { path: 'cashier/:id', name: 'Cashier', component: () => import('../views/Cashier.vue'), meta: { title: '收银台', requiresAuth: true } },
      { path: 'waitlist/:id', name: 'WaitlistCashier', component: () => import('../views/WaitlistCashier.vue'), meta: { title: '候补', requiresAuth: true } },
      { path: 'rush-sales', name: 'RushSales', component: () => import('../views/RushSales.vue'), meta: { title: '限时开售' } },
      { path: 'orders', name: 'OrderList', component: () => import('../views/OrderList.vue'), meta: { title: '我的订单', requiresAuth: true } },
      { path: 'tickets', redirect: { path: '/orders', query: { tab: 'tickets' } } },
      { path: 'orders/:id', name: 'OrderDetail', component: () => import('../views/OrderDetail.vue'), meta: { title: '订单详情', requiresAuth: true } },
      { path: 'account', name: 'UserCenter', component: () => import('../views/UserCenter.vue'), meta: { title: '个人中心', requiresAuth: true } },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({ history: createWebHistory(), routes })

function metaFlag(to, flag) {
  return !!(to.meta?.[flag] || to.matched.some(record => record.meta?.[flag]))
}

router.beforeEach(async (to, from, next) => {
  const token = localStorage.getItem('access_token') || localStorage.getItem('token')
  const isAuthPage = to.path === '/login' || to.path === '/register'
  const needsAuth = metaFlag(to, 'requiresAuth')
  const needsAdmin = metaFlag(to, 'requiresAdmin')
  const needsOrganizer = metaFlag(to, 'requiresOrganizer')
  const blockedForAdmin = metaFlag(to, 'blockAdmin')

  // 需要登录的页面：本地没有任何 token 时不必打扰后端，直接拦。
  if (!token && (needsAuth || needsAdmin || needsOrganizer)) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }

  // 只要本次导航涉及身份判断，就一律向后端确认。
  // 不再读取 localStorage 的 role 做放行决策 —— 那个值用户可以在浏览器里直接改写。
  let identity = { authenticated: false, role: '', organizer: false }
  if (token && (needsAuth || needsAdmin || needsOrganizer || isAuthPage)) {
    identity = needsOrganizer ? await resolveOrganizerAccess() : await resolveIdentity()

    if (!identity.authenticated) {
      // token 已失效（过期或被吊销）：清掉本地会话并要求重新登录。
      endClientSession()
      invalidateIdentity()
      next({ path: '/login', query: { redirect: to.fullPath } })
      return
    }

    // 后端确认过的身份可以回写本地，供首屏渲染使用；它不参与下面的放行判断。
    if (identity.username) localStorage.setItem('username', identity.username)
    if (identity.role) localStorage.setItem('role', identity.role)
  }

  if (isAuthPage) {
    if (identity.authenticated) {
      next(isAdmin(identity) ? '/admin' : '/')
      return
    }
    // 未登录时正常展示登录/注册页。
    document.title = `${to.meta.title || '登录'} · Gofun`
    next()
    return
  }

  if (needsAdmin && !isAdmin(identity)) {
    next('/')
    return
  }
  if (blockedForAdmin && isAdmin(identity)) {
    next('/admin')
    return
  }
  if (needsOrganizer && !identity.organizer) {
    // 已登录但没有可用主办方工作台：引导去首页而不是放行。
    next('/')
    return
  }

  document.title = `${to.meta.title || to.matched.find(record => record.meta.title)?.meta.title || '发现活动'} · Gofun`
  next()
})

export default router
