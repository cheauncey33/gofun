<template>
  <div style="padding: 32px">
    <h2 style="margin-bottom: 16px">商品列表</h2>

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
        <el-form-item label="用户 ID">
          <el-input-number v-model="orderForm.user_id" :min="1" style="width: 100%" />
        </el-form-item>
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
</template>

<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'

const products = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const submitting = ref(false)
const currentProduct = ref(null)
const orderForm = ref({ user_id: 1, quantity: 1 })

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
  orderForm.value = { user_id: 1, quantity: 1 }
  dialogVisible.value = true
}

async function submitOrder() {
  submitting.value = true
  try {
    await axios.post('/api/v1/orders', {
      user_id: orderForm.value.user_id,
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

onMounted(fetchProducts)
</script>
