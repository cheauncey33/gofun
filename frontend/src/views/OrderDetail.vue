<template>
  <div v-loading="loading" style="max-width:800px">
    <div class="page-header">
      <h3>订单详情</h3>
      <el-button @click="$router.back()">返回列表</el-button>
    </div>

    <el-card shadow="never" style="margin-bottom:20px">
      <template #header><span style="font-weight:700">基本信息</span></template>
      <el-descriptions :column="2">
        <el-descriptions-item label="订单号">
          <span style="font-family:monospace">{{ order.id }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="tagType" effect="light" round>{{ statusLabel }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="金额">
          <span style="color:#E17055;font-weight:700;font-size:16px">¥{{ order.total_price?.toFixed(2) }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="时间">{{ order.CreateTime?.slice(0,19) }}</el-descriptions-item>
        <el-descriptions-item v-if="order.cancel_reason" label="取消原因" :span="2">
          {{ order.cancel_reason }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" style="margin-bottom:20px">
      <template #header><span style="font-weight:700">商品明细</span></template>
      <el-table :data="order.order_items" stripe>
        <el-table-column prop="Product.name" label="商品" />
        <el-table-column label="单价" width="100">
          <template #default="{row}">¥{{ row.SnapshotPrice?.toFixed(2) }}</template>
        </el-table-column>
        <el-table-column prop="Quantity" label="数量" width="60" />
        <el-table-column label="小计" width="100">
          <template #default="{row}">¥{{ ((row.SnapshotPrice || 0) * (row.Quantity || 0)).toFixed(2) }}</template>
        </el-table-column>
      </el-table>
    </el-card>

    <div style="display:flex;gap:12px">
      <el-button v-if="[1,2].includes(order.status)" type="warning" @click="cancel">取消订单</el-button>
      <el-button v-if="order.status===4" type="danger" @click="refund">申请退款</el-button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, inject } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api/index.js'

const route = useRoute()
const refreshUser = inject('refreshUser', () => {})
const order = ref({})
const loading = ref(false)

const statusMap = {1:'待支付',2:'已支付',3:'配送中',4:'已完成',5:'已取消',6:'退款中',7:'已退款'}
const statusLabel = computed(() => statusMap[order.value.status] || order.value.status)
const tagMap = {1:'warning',2:'primary',3:'primary',4:'success',5:'info',6:'danger',7:'info'}
const tagType = computed(() => tagMap[order.value.status] || '')

onMounted(async () => {
  loading.value = true
  try { const res = await api.getOrderDetail(route.params.id); order.value = res.data } finally { loading.value = false }
})

async function cancel() {
  let reason = ''
  try { const r = await ElMessageBox.prompt('取消原因', '取消订单'); reason = r.value } catch { return }
  try {
    await api.cancelOrder(order.value.id, reason || '用户取消')
    ElMessage.success('已取消，余额已退回')
    refreshUser()
    order.value.status = 5
  } catch (e) { ElMessage.error(e.response?.data?.msg) }
}

async function refund() {
  let reason = ''
  try { const r = await ElMessageBox.prompt('退款原因', '申请退款'); reason = r.value } catch { return }
  try {
    await api.refundOrder(order.value.id, reason || '用户退款')
    ElMessage.success('已退款，余额已退回')
    refreshUser()
    order.value.status = 7
  } catch (e) { ElMessage.error(e.response?.data?.msg) }
}
</script>
