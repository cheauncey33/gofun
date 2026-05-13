<template>
  <div v-loading="loading" class="detail-page">
    <el-row :gutter="32">
      <el-col :xs="24" :sm="10">
        <el-image :src="imageUrl(product.image_url)" fit="cover" class="detail-image" />
      </el-col>
      <el-col :xs="24" :sm="14">
        <h2 class="detail-title">{{ productName(product) }}</h2>
        <p v-if="product.description && !isGarbledText(product.description)" class="detail-desc">
          {{ product.description }}
        </p>

        <div class="price-panel">
          <span>{{ money(product.price) }}</span>
        </div>

        <div class="detail-meta">
          <div><b>{{ product.stock ?? 0 }}</b><span>库存</span></div>
          <div><b>{{ product.sales_count ?? 0 }}</b><span>已售</span></div>
          <div v-if="product.category"><b>{{ product.category.name }}</b><span>分类</span></div>
        </div>

        <div class="action-row">
          <el-input-number v-model="quantity" :min="1" :max="Math.max(product.stock || 1, 1)" size="large" />
          <el-button type="primary" size="large" :loading="submitting" @click="addToCart">加入购物车</el-button>
          <el-button type="danger" size="large" :loading="submitting" @click="buy">立即购买</el-button>
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useCart } from '../stores/cart.js'
import api from '../api/index.js'
import { imageUrl, isGarbledText, money, productName } from '../utils/display.js'

const cart = useCart()
const route = useRoute()
const router = useRouter()
const refreshUser = inject('refreshUser', () => {})
const product = ref({})
const loading = ref(false)
const submitting = ref(false)
const quantity = ref(1)

onMounted(async () => {
  loading.value = true
  try {
    const res = await api.getProductDetail(route.params.id)
    product.value = res.data || {}
  } finally {
    loading.value = false
  }
})

function addToCart() {
  cart.addItem(product.value, quantity.value)
  ElMessage.success('已加入购物车')
}

async function buy() {
  submitting.value = true
  try {
    await api.createOrder([{ product_id: Number(product.value.id), num: quantity.value }])
    ElMessage.success('下单成功')
    refreshUser()
    router.push('/orders')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '下单失败')
  } finally {
    submitting.value = false
  }
}
</script>
