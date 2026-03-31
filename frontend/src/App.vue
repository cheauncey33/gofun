<template>
  <div id="app">
    <!-- 登录页 -->
    <Login
      v-if="currentView === 'login'"
      @login-success="handleLoginSuccess"
      @to-register="currentView = 'register'"
    />

    <!-- 注册页（占位） -->
    <Register
      v-else-if="currentView === 'register'"
      @register-success="handleLoginSuccess"
      @to-login="currentView = 'login'"
    />

    <!-- 商品列表页 -->
    <div v-else class="product-page">
      <div class="header">
        <h2>WHU Snack 商品列表</h2>
        <div class="user-info">
          <span>欢迎，{{ username }}</span>
          <el-button type="danger" size="small" @click="handleLogout">退出</el-button>
        </div>
      </div>

      <el-table :data="products" border stripe v-loading="loading" style="width: 100%">
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="name" label="商品名称" />
        <el-table-column prop="price" label="单价（元）" width="120">
          <template #default="{ row }">
            {{ row.price.toFixed(2) }}
          </template>
        </el-table-column>
        <el-table-column prop="stock" label="库存" width="100" />
        <el-table-column label="操作" width="100">
          <template #default="{ row }">
            <el-button type="primary" size="small" @click="openBuyDialog(row)">购买</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 购买弹窗 -->
      <el-dialog v-model="dialogVisible" title="购买商品" width="360px">
        <div style="margin-bottom: 12px">商品：<strong>{{ currentProduct?.name }}</strong></div>
        <div style="margin-bottom: 12px">单价：{{ currentProduct?.price?.toFixed(2) }} 元</div>
        <el-form :model="orderForm" label-width="80px">
          <el-form-item label="购买数量">
            <el-input-number v-model="orderForm.quantity" :min="1" :max="currentProduct?.stock" style="width: 100%" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" :loading="submitting" @click="submitOrder">确认下单</el-button>
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'
import Login from './views/Login.vue'
import Register from './views/Register.vue'

const isLoggedIn = ref(false)
const currentView = ref('login')
const username = ref('')

const products = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const submitting = ref(false)
const currentProduct = ref(null)
const orderForm = ref({ quantity: 1 })

function handleLoginSuccess() {
  isLoggedIn.value = true
  currentView.value = 'product'
  username.value = localStorage.getItem('username') || '用户'
  fetchProducts()
}

function handleLogout() {
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  isLoggedIn.value = false
  currentView.value = 'login'
  products.value = []
}

async function fetchProducts() {
  loading.value = true
  try {
    const res = await axios.get('/api/v1/products', {
      params: { page: 1, page_size: 10 },
    })
    products.value = res.data.data ?? res.data
  } catch (e) {
    ElMessage.error('获取商品列表失败：' + (e.response?.data?.msg ?? e.message))
  } finally {
    loading.value = false
  }
}

function openBuyDialog(row) {
  currentProduct.value = row
  orderForm.value = { quantity: 1 }
  dialogVisible.value = true
}

async function submitOrder() {
  submitting.value = true
  try {
    await axios.post('/api/v1/orders', {
      user_id: 1,
      items: [
        {
          product_id: currentProduct.value.id,
          num: orderForm.value.quantity,
        },
      ],
    })
    ElMessage.success('下单成功！')
    dialogVisible.value = false
    fetchProducts()
  } catch (e) {
    ElMessage.error('下单失败：' + (e.response?.data?.msg ?? e.message))
  } finally {
    submitting.value = false
  }
}

// 检查是否已登录
onMounted(() => {
  const token = localStorage.getItem('token')
  if (token) {
    isLoggedIn.value = true
    username.value = localStorage.getItem('username') || '用户'
    fetchProducts()
  }
})
</script>

<style scoped>
.product-page {
  padding: 32px;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24px;
}

.header h2 {
  margin: 0;
}

.user-info {
  display: flex;
  align-items: center;
  gap: 16px;
}
</style>
