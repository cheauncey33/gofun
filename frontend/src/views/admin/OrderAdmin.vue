<template>
  <div>
    <div class="page-header">
      <div>
        <h3>订单管理</h3>
        <p class="page-subtitle">查看订单并推进配送、完成、取消或退款状态</p>
      </div>
    </div>

    <el-radio-group v-model="statusFilter" class="filter-tabs" @change="fetch" size="large">
      <el-radio-button value="">全部</el-radio-button>
      <el-radio-button :value="2">已支付</el-radio-button>
      <el-radio-button :value="3">已完成</el-radio-button>
      <el-radio-button :value="5">已取消</el-radio-button>
    </el-radio-group>

    <el-table :data="orders" v-loading="loading" stripe>
      <el-table-column label="订单号" width="190"><template #default="{ row }"><span class="mono">{{ row.id }}</span></template></el-table-column>
      <el-table-column label="用户ID" width="100"><template #default="{ row }"><span class="mono">{{ row.UserID || row.user_id }}</span></template></el-table-column>
      <el-table-column label="金额" width="120"><template #default="{ row }"><span class="price-text">{{ money(row.total_price) }}</span></template></el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><el-tag :type="orderStatusTag[row.status]" round size="small">{{ orderStatusText[row.status] }}</el-tag></template>
      </el-table-column>
      <el-table-column label="操作">
        <template #default="{ row }">
          <el-select v-model="newStatus[row.id]" size="small" class="status-select" placeholder="选择状态">
            <el-option v-for="s in allowedTransitions(row.status)" :key="s" :label="orderStatusText[s]" :value="s" />
          </el-select>
          <el-button size="small" type="primary" @click="updateStatus(row)" :disabled="!newStatus[row.id]">更新</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../../api/index.js'
import { money, orderStatusTag, orderStatusText } from '../../utils/display.js'

const orders = ref([])
const loading = ref(false)
const statusFilter = ref('')
const newStatus = reactive({})

const transitionMap = { 1: [2, 5], 2: [3, 5], 3: [5] }

function allowedTransitions(s) {
  return transitionMap[s] || []
}

onMounted(fetch)

async function fetch() {
  loading.value = true
  const params = { page: 1, page_size: 100 }
  if (statusFilter.value) params.status = statusFilter.value
  try {
    const res = await api.adminGetOrders(params)
    orders.value = res.data?.list || []
  } finally {
    loading.value = false
  }
}

async function updateStatus(row) {
  try {
    await ElMessageBox.confirm('确认修改订单状态？', '更新订单', { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.adminUpdateOrderStatus(row.id, newStatus[row.id])
    ElMessage.success('状态已更新')
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '更新失败')
  }
}
</script>
