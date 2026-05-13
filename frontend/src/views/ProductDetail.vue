<template>
  <div v-loading="loading" style="max-width:1000px">
    <el-row :gutter="40">
      <el-col :span="10">
        <el-image :src="product.image_url || 'https://placehold.co/500x500/f0f0ff/6C5CE7?text=Snack'"
          fit="cover" style="width:100%;border-radius:16px;box-shadow:var(--shadow)" />
      </el-col>
      <el-col :span="14">
        <div style="font-size:24px;font-weight:700;margin-bottom:8px">{{ product.name }}</div>
        <p v-if="product.description" style="color:var(--text-secondary);line-height:1.6;margin-bottom:20px">
          {{ product.description }}
        </p>
        <div style="background:linear-gradient(135deg,#FFF5F5,#FFF0F0);border-radius:12px;padding:20px;margin-bottom:20px">
          <span style="color:#E17055;font-size:36px;font-weight:800">¥{{ product.price?.toFixed(2) }}</span>
        </div>
        <div style="display:flex;gap:32px;margin-bottom:24px;color:var(--text-secondary)">
          <div><span style="font-weight:600;color:var(--text)">{{ product.stock }}</span> 库存</div>
          <div><span style="font-weight:600;color:var(--text)">{{ product.sales_count }}</span> 已售</div>
          <div v-if="product.category">分类: <span style="color:var(--primary)">{{ product.category.name }}</span></div>
        </div>
        <div style="display:flex;align-items:center;gap:16px;flex-wrap:wrap">
          <el-input-number v-model="quantity" :min="1" :max="product.stock" size="large" style="width:140px" />
          <el-button type="primary" size="large" :loading="submitting" @click="addToCart"
            style="height:48px;padding:0 32px;font-size:15px;font-weight:600;border-radius:12px">
            加入购物车
          </el-button>
          <el-button type="danger" size="large" :loading="submitting" @click="buy"
            style="height:48px;padding:0 32px;font-size:15px;font-weight:600;border-radius:12px">
            立即购买
          </el-button>
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
  try { const res = await api.getProductDetail(route.params.id); product.value = res.data } finally { loading.value = false }
})

function addToCart() {
  cart.addItem(product.value, quantity.value)
  ElMessage.success('已加入购物车')
}

async function buy() {
  submitting.value = true
  try {
    await api.createOrder([{ product_id: Number(product.value.id), num: quantity.value }])
    ElMessage.success('下单成功！')
    refreshUser()
    router.push('/orders')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '下单失败')
  } finally { submitting.value = false }
}
</script>
