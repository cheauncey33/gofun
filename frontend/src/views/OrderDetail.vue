<template>
  <div v-loading="loading" class="medium-page">
    <div class="page-header">
      <div>
        <h3>订单详情</h3>
        <p class="page-subtitle">订单号 {{ order.id || '-' }}</p>
      </div>
      <el-button @click="$router.back()">返回列表</el-button>
    </div>

    <el-card shadow="never" class="section-card">
      <template #header><span class="section-title">基本信息</span></template>
      <el-descriptions :column="2">
        <el-descriptions-item label="订单号"><span class="mono">{{ order.id }}</span></el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="tagType" effect="light" round>{{ statusLabel }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="金额"><span class="price-text">{{ money(order.total_price) }}</span></el-descriptions-item>
        <el-descriptions-item label="时间">{{ shortDateTime(order.CreateTime) }}</el-descriptions-item>
        <el-descriptions-item v-if="order.cancel_reason" label="原因" :span="2">
          {{ order.cancel_reason }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header><span class="section-title">商品明细</span></template>
      <el-table :data="order.order_items || []" stripe>
        <el-table-column label="商品">
          <template #default="{ row }">{{ productName(row.Product || row.product, '商品') }}</template>
        </el-table-column>
        <el-table-column label="单价" width="110">
          <template #default="{ row }">{{ money(row.SnapshotPrice || row.snapshot_price) }}</template>
        </el-table-column>
        <el-table-column label="数量" width="80">
          <template #default="{ row }">{{ row.Quantity || row.quantity }}</template>
        </el-table-column>
        <el-table-column label="小计" width="120">
          <template #default="{ row }">
            {{ money((row.SnapshotPrice || row.snapshot_price || 0) * (row.Quantity || row.quantity || 0)) }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <div class="action-row">
      <el-button v-if="[1, 2].includes(order.status)" type="warning" @click="cancel">取消订单</el-button>
      <el-button v-if="order.status === 4" type="danger" @click="refund">申请退款</el-button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, inject } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api/index.js'
import { money, orderStatusTag, orderStatusText, productName, shortDateTime } from '../utils/display.js'

const route = useRoute()
const refreshUser = inject('refreshUser', () => {})
const order = ref({})
const loading = ref(false)

const statusLabel = computed(() => orderStatusText[order.value.status] || order.value.status || '-')
const tagType = computed(() => orderStatusTag[order.value.status] || '')

onMounted(loadOrder)

async function loadOrder() {
  loading.value = true
  try {
    const res = await api.getOrderDetail(route.params.id)
    order.value = res.data || {}
  } finally {
    loading.value = false
  }
}

async function cancel() {
  let reason = ''
  try {
    const r = await ElMessageBox.prompt('取消原因', '取消订单')
    reason = r.value
  } catch {
    return
  }
  try {
    await api.cancelOrder(order.value.id, reason || '用户取消')
    ElMessage.success('已取消，余额已退回')
    refreshUser()
    loadOrder()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '取消失败')
  }
}

async function refund() {
  let reason = ''
  try {
    const r = await ElMessageBox.prompt('退款原因', '申请退款')
    reason = r.value
  } catch {
    return
  }
  try {
    await api.refundOrder(order.value.id, reason || '用户退款')
    ElMessage.success('已退款，余额已退回')
    refreshUser()
    loadOrder()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '退款失败')
  }
}
</script>
