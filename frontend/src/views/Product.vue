<template>
  <div class="product-page">
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
          {{ row.price?.toFixed(2) }}
        </template>
      </el-table-column>
      <el-table-column prop="stock" label="库存" width="100" />
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button type="primary" size="small" @click="openBuyDialog(row)">购买</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 分页组件 -->
    <div style="margin-top: 20px; display: flex; justify-content: flex-end;">
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :page-sizes="[5, 10, 20]"
        layout="total, sizes, prev, pager, next, jumper"
        :total="totalProducts"
        @size-change="handleSizeChange"
        @current-change="handleCurrentChange"
      />
    </div>

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
</template>

<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'

// 与 App.vue 进行通信
const emit = defineEmits(['logout'])

const username = ref(localStorage.getItem('username') || '用户')

const products = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const submitting = ref(false)
const currentProduct = ref(null)
const orderForm = ref({ quantity: 1 })

// 分页相关变量
const currentPage = ref(1)
const pageSize = ref(10)
const totalProducts = ref(0)

// 发起请求给后端获取商品列表
async function fetchProducts() {
  loading.value = true
  try {
    const res = await axios.get('/api/v1/products', {
      params: { page: currentPage.value, page_size: pageSize.value },
    })
    const body = res.data
    products.value = body.data?.list ?? body.data ?? []
    totalProducts.value = body.data?.total ?? 0
  } catch (e) {
    ElMessage.error('获取商品列表失败：' + (e.response?.data?.msg ?? e.message))
  } finally {
    loading.value = false
  }
}

// 分页变化处理
function handleSizeChange(val) {
  pageSize.value = val
  currentPage.value = 1
  fetchProducts()
}

function handleCurrentChange(val) {
  currentPage.value = val
  fetchProducts()
}

// 购买商品逻辑
function openBuyDialog(row) {
  currentProduct.value = row
  orderForm.value = { quantity: 1 }
  dialogVisible.value = true
}

async function submitOrder() {
  submitting.value = true
  try {
    await axios.post('/api/v1/orders', {
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

function handleLogout() {
  emit('logout')
}

// 页面加载完毕后立刻获取第一页数据
onMounted(() => {
  fetchProducts()
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
