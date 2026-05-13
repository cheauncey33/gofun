<template>
  <div>
    <div class="page-header"><h3>订单管理</h3></div>

    <el-radio-group v-model="statusFilter" style="margin-bottom:16px" @change="fetch" size="large">
      <el-radio-button value="">全部</el-radio-button>
      <el-radio-button :value="2">已支付</el-radio-button>
      <el-radio-button :value="3">配送中</el-radio-button>
      <el-radio-button :value="4">已完成</el-radio-button>
      <el-radio-button :value="5">已取消</el-radio-button>
    </el-radio-group>

    <el-table :data="orders" v-loading="loading" stripe>
      <el-table-column label="订单号" width="190"><template #default="{row}"><span style="font-family:monospace;font-size:12px">{{ row.id }}</span></template></el-table-column>
      <el-table-column label="用户ID" width="100"><template #default="{row}"><span style="font-family:monospace;font-size:12px">{{ row.UserID || row.user_id }}</span></template></el-table-column>
      <el-table-column label="金额" width="110"><template #default="{row}"><span style="color:#E17055;font-weight:700">¥{{ row.total_price?.toFixed(2) }}</span></template></el-table-column>
      <el-table-column label="状态" width="90"><template #default="{row}"><el-tag round size="small">{{ statusMap[row.status] }}</el-tag></template></el-table-column>
      <el-table-column label="操作">
        <template #default="{row}">
          <el-select v-model="newStatus[row.id]" size="small" style="width:110px" placeholder="选择状态">
            <el-option v-for="s in allowedTransitions(row.status)" :key="s" :label="statusMap[s]" :value="s" />
          </el-select>
          <el-button size="small" type="primary" style="margin-left:8px" @click="updateStatus(row)" :disabled="!newStatus[row.id]">更新</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../../api/index.js'

const orders = ref([])
const loading = ref(false)
const statusFilter = ref('')
const newStatus = reactive({})

const transitionMap = { 1: [2,5], 2: [3,5,6], 3: [4,5], 4: [6], 6: [7] }
const statusMap = { 1:'待支付', 2:'已支付', 3:'配送中', 4:'已完成', 5:'已取消', 6:'退款中', 7:'已退款' }

function allowedTransitions(s) { return transitionMap[s] || [] }

onMounted(fetch)

async function fetch() {
  loading.value = true
  const params = { page: 1, page_size: 100 }
  if (statusFilter.value) params.status = statusFilter.value
  try { const res = await api.adminGetOrders(params); orders.value = res.data?.list || [] } finally { loading.value = false }
}

async function updateStatus(row) {
  try { await ElMessageBox.confirm('确认修改订单状态？', '提示', { type: 'warning' }) } catch { return }
  try { await api.adminUpdateOrderStatus(row.id, newStatus[row.id]); ElMessage.success('状态已更新'); fetch() }
  catch (e) { ElMessage.error(e.response?.data?.msg) }
}
</script>
