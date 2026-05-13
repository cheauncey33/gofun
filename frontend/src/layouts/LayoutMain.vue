<template>
  <el-container style="min-height:100vh;background:var(--bg)">
    <!-- Sidebar -->
    <el-aside width="240px" style="background:linear-gradient(180deg,#1a1a2e 0%,#16213e 100%);overflow:hidden">
      <div style="padding:28px 24px;border-bottom:1px solid rgba(255,255,255,0.06)">
        <div style="font-size:22px;font-weight:800;color:#fff;letter-spacing:-0.5px">
          <span style="color:var(--primary-light)">WHU</span> Snack
        </div>
        <div style="color:rgba(255,255,255,0.4);font-size:12px;margin-top:4px">校园零食商城</div>
      </div>

      <el-menu :default-active="route.path" router
        background-color="transparent" text-color="rgba(255,255,255,0.55)"
        active-text-color="#fff" style="border:none;padding:12px 0"
      >
        <el-menu-item index="/" style="margin:2px 12px;border-radius:10px;height:44px">
          <el-icon><Shop /></el-icon> 商品浏览
        </el-menu-item>
        <el-menu-item index="/orders" style="margin:2px 12px;border-radius:10px;height:44px">
          <el-icon><Tickets /></el-icon> 我的订单
        </el-menu-item>
        <el-menu-item index="/addresses" style="margin:2px 12px;border-radius:10px;height:44px">
          <el-icon><LocationFilled /></el-icon> 收货地址
        </el-menu-item>
        <el-menu-item index="/profile" style="margin:2px 12px;border-radius:10px;height:44px">
          <el-icon><UserFilled /></el-icon> 个人中心
        </el-menu-item>
        <el-menu-item index="/seckill" style="margin:2px 12px;border-radius:10px;height:44px">
          <el-icon><Timer /></el-icon>
          <span>秒杀活动</span>
          <el-badge v-if="activeSeckillCount" :value="activeSeckillCount" style="margin-left:8px" />
        </el-menu-item>

        <template v-if="isAdmin">
          <div style="margin:16px 24px 8px;font-size:11px;color:rgba(255,255,255,0.25);text-transform:uppercase;letter-spacing:2px;font-weight:600">管理端</div>
          <el-menu-item index="/admin/dashboard" style="margin:2px 12px;border-radius:10px;height:40px;font-size:13px">
            <el-icon><DataLine /></el-icon> 数据看板
          </el-menu-item>
          <el-menu-item index="/admin/products" style="margin:2px 12px;border-radius:10px;height:40px;font-size:13px">
            <el-icon><Goods /></el-icon> 商品管理
          </el-menu-item>
          <el-menu-item index="/admin/orders" style="margin:2px 12px;border-radius:10px;height:40px;font-size:13px">
            <el-icon><Postcard /></el-icon> 订单管理
          </el-menu-item>
          <el-menu-item index="/admin/users" style="margin:2px 12px;border-radius:10px;height:40px;font-size:13px">
            <el-icon><User /></el-icon> 用户管理
          </el-menu-item>
          <el-menu-item index="/admin/seckill" style="margin:2px 12px;border-radius:10px;height:40px;font-size:13px">
            <el-icon><Timer /></el-icon> 秒杀管理
          </el-menu-item>
        </template>
      </el-menu>
    </el-aside>

    <!-- Main -->
    <el-container>
      <el-header style="height:64px;display:flex;align-items:center;justify-content:space-between;
        background:var(--card-bg);border-bottom:1px solid var(--border);padding:0 28px">
        <div>
          <span style="font-size:17px;font-weight:600;color:var(--text)">{{ pageTitle }}</span>
        </div>
        <!-- Cart button -->
          <el-badge :value="cartTotal" :hidden="!cartTotal" :max="99" style="margin-right:8px">
            <el-button circle :icon="ShoppingCartFull" size="large" @click="cartDrawer = true" />
          </el-badge>

          <div style="display:flex;align-items:center;gap:20px">
          <div style="text-align:right">
            <div style="font-size:14px;font-weight:600;color:var(--text)">{{ username }}</div>
            <div style="font-size:12px;color:var(--primary);font-weight:500">余额 ¥{{ balance?.toFixed(2) }}</div>
          </div>
          <el-avatar :size="36" style="background:var(--primary-light);color:#fff;font-weight:600">
            {{ username.charAt(0).toUpperCase() }}
          </el-avatar>
          <el-button size="small" text style="color:var(--text-secondary)" @click="logout">退出</el-button>
        </div>
      </el-header>
      <el-main style="padding:28px;max-width:1400px;margin:0 auto;width:100%">
        <router-view />
      </el-main>

    <!-- Cart drawer -->
    <el-drawer v-model="cartDrawer" title="购物车" size="400px" direction="rtl">
      <div v-if="cartItems.length === 0" style="text-align:center;padding:60px 0;color:var(--text-secondary)">
        <el-icon :size="48" style="margin-bottom:12px"><ShoppingCartFull /></el-icon>
        <div>购物车是空的</div>
        <el-button type="primary" style="margin-top:16px" @click="cartDrawer = false">去逛逛</el-button>
      </div>
      <div v-else>
        <div v-for="item in cartItems" :key="item.id" style="display:flex;align-items:center;padding:12px 0;border-bottom:1px solid var(--border)">
          <el-image :src="item.image_url || 'https://placehold.co/60x60/f0f0ff/6C5CE7'" style="width:60px;height:60px;border-radius:8px;flex-shrink:0" />
          <div style="flex:1;margin-left:12px">
            <div style="font-weight:600;font-size:14px">{{ item.name }}</div>
            <div style="color:#E17055;font-weight:700">¥{{ item.price?.toFixed(2) }}</div>
          </div>
          <el-input-number v-model="item.quantity" :min="1" :max="item.stock" size="small" style="width:90px" @change="cart.updateQuantity(item.id, $event)" />
          <el-button :icon="Delete" circle size="small" style="margin-left:8px" @click="cart.removeItem(item.id)" />
        </div>
        <div style="margin-top:16px;padding:16px;background:var(--bg);border-radius:12px">
          <div style="display:flex;justify-content:space-between;margin-bottom:8px">
            <span>共 {{ cartTotal }} 件</span>
            <span style="color:#E17055;font-size:20px;font-weight:800">¥{{ cartAmount?.toFixed(2) }}</span>
          </div>
          <el-button type="danger" size="large" style="width:100%;height:48px;font-size:16px;font-weight:600;border-radius:12px"
            :loading="checkingOut" @click="checkout">
            立即结算
          </el-button>
        </div>
      </div>
    </el-drawer>
    </el-container>
  </el-container>
</template>

<script setup>
import { ref, computed, onMounted, provide } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ShoppingCartFull, Delete } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useCart } from '../stores/cart.js'
import api from '../api/index.js'

const cart = useCart()
const cartDrawer = ref(false)
const cartItems = cart.items
const cartTotal = cart.totalCount
const cartAmount = cart.totalAmount
const checkingOut = ref(false)

async function checkout() {
  if (cartItems.value.length === 0) return
  checkingOut.value = true
  try {
    const orderItems = cartItems.value.map(i => ({ product_id: i.id, num: i.quantity }))
    await api.createOrder(orderItems)
    ElMessage.success('下单成功！')
    cart.clear()
    cartDrawer.value = false
    await refreshUser()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '下单失败')
  } finally { checkingOut.value = false }
}

const route = useRoute()
const router = useRouter()
const username = ref(localStorage.getItem('username') || '用户')
const balance = ref(0)
const isAdmin = ref(false)
const activeSeckillCount = ref(0)

const pageTitle = computed(() => route.meta?.title || route.name || '')

async function refreshUser() {
  try {
    const res = await api.getUserInfo()
    username.value = res.data?.username || username.value
    balance.value = res.data?.balance || 0
    isAdmin.value = res.data?.role === 'admin'
    if (res.data?.role) localStorage.setItem('role', res.data.role)
  } catch {}
}

provide('refreshUser', refreshUser)

onMounted(async () => {
  await refreshUser()
  try {
    const res = await api.getSeckillActivities({ page: 1, page_size: 50 })
    activeSeckillCount.value = (res.data?.list || []).filter(a => a.status === 1).length
  } catch {}
})

function logout() {
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  router.push('/login')
}
</script>
