<template>
  <el-container class="app-shell">
    <el-aside width="240px" class="sidebar">
      <div class="brand">
        <div class="brand-title"><span>WHU</span> Snack</div>
        <div class="brand-subtitle">校园零食商城</div>
      </div>

      <el-menu
        :default-active="route.path"
        router
        background-color="transparent"
        text-color="rgba(255,255,255,0.68)"
        active-text-color="#fff"
        class="side-menu"
      >
        <el-menu-item index="/"><el-icon><Shop /></el-icon><span>商品浏览</span></el-menu-item>
        <el-menu-item index="/orders"><el-icon><Tickets /></el-icon><span>我的订单</span></el-menu-item>
        <el-menu-item index="/addresses"><el-icon><LocationFilled /></el-icon><span>收货地址</span></el-menu-item>
        <el-menu-item index="/profile"><el-icon><UserFilled /></el-icon><span>个人中心</span></el-menu-item>
        <el-menu-item index="/seckill">
          <el-icon><Timer /></el-icon>
          <span class="menu-label">秒杀活动</span>
          <span v-if="activeSeckillCount" class="menu-count">{{ activeSeckillCount }}</span>
        </el-menu-item>

        <template v-if="isAdmin">
          <div class="menu-section">管理端</div>
          <el-menu-item index="/admin/dashboard"><el-icon><DataLine /></el-icon><span>数据看板</span></el-menu-item>
          <el-menu-item index="/admin/products"><el-icon><Goods /></el-icon><span>商品管理</span></el-menu-item>
          <el-menu-item index="/admin/orders"><el-icon><Postcard /></el-icon><span>订单管理</span></el-menu-item>
          <el-menu-item index="/admin/users"><el-icon><User /></el-icon><span>用户管理</span></el-menu-item>
          <el-menu-item index="/admin/seckill"><el-icon><Timer /></el-icon><span>秒杀管理</span></el-menu-item>
        </template>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="topbar">
        <div class="topbar-title">{{ pageTitle }}</div>
        <div class="topbar-actions">
          <el-badge :value="cartTotal" :hidden="!cartTotal" :max="99">
            <el-button circle :icon="ShoppingCartFull" size="large" @click="cartDrawer = true" />
          </el-badge>
          <div class="user-summary">
            <div class="user-name">{{ username }}</div>
            <div class="user-balance">余额 {{ money(balance) }}</div>
          </div>
          <el-avatar :size="36" class="user-avatar">{{ username.charAt(0).toUpperCase() }}</el-avatar>
          <el-button size="small" text @click="logout">退出</el-button>
        </div>
      </el-header>

      <el-main class="main-content">
        <router-view />
      </el-main>

      <el-drawer v-model="cartDrawer" title="购物车" size="400px" direction="rtl">
        <div v-if="cartItems.length === 0" class="cart-empty">
          <el-icon :size="48"><ShoppingCartFull /></el-icon>
          <div>购物车是空的</div>
          <el-button type="primary" @click="cartDrawer = false">去逛逛</el-button>
        </div>
        <div v-else>
          <div v-for="item in cartItems" :key="item.id" class="cart-item">
            <el-image :src="imageUrl(item.image_url)" fit="cover" class="cart-thumb" />
            <div class="cart-info">
              <div class="cart-name">{{ item.name }}</div>
              <div class="cart-price">{{ money(item.price) }}</div>
            </div>
            <el-input-number
              v-model="item.quantity"
              :min="1"
              :max="item.stock"
              size="small"
              class="cart-qty"
              @change="cart.updateQuantity(item.id, $event)"
            />
            <el-button :icon="Delete" circle size="small" @click="cart.removeItem(item.id)" />
          </div>
          <div class="cart-summary">
            <div class="cart-total-line">
              <span>共 {{ cartTotal }} 件</span>
              <span>{{ money(cartAmount) }}</span>
            </div>
            <el-button type="danger" size="large" class="checkout-button" :loading="checkingOut" @click="checkout">
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
import {
  DataLine,
  Delete,
  Goods,
  LocationFilled,
  Postcard,
  Shop,
  ShoppingCartFull,
  Tickets,
  Timer,
  User,
  UserFilled,
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useCart } from '../stores/cart.js'
import api from '../api/index.js'
import { imageUrl, money } from '../utils/display.js'

const cart = useCart()
const cartDrawer = ref(false)
const cartItems = cart.items
const cartTotal = cart.totalCount
const cartAmount = cart.totalAmount
const checkingOut = ref(false)

const route = useRoute()
const router = useRouter()
const username = ref(localStorage.getItem('username') || '用户')
const balance = ref(0)
const isAdmin = ref(false)
const activeSeckillCount = ref(0)

const pageTitle = computed(() => route.meta?.title || route.name || 'WHU Snack')

async function checkout() {
  if (cartItems.value.length === 0) return
  checkingOut.value = true
  try {
    const orderItems = cartItems.value.map((i) => ({ product_id: i.id, num: i.quantity }))
    await api.createOrder(orderItems)
    ElMessage.success('下单成功')
    cart.clear()
    cartDrawer.value = false
    await refreshUser()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '下单失败')
  } finally {
    checkingOut.value = false
  }
}

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
    activeSeckillCount.value = (res.data?.list || []).filter((a) => a.status === 1).length
  } catch {}
})

function logout() {
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
  router.push('/login')
}
</script>
