<template>
  <div>
    <div class="page-header">
      <div>
        <h3>我的订单</h3>
        <p class="page-subtitle">查看订单状态，支持取消和退款申请</p>
      </div>
    </div>

    <el-radio-group v-model="statusFilter" size="large" class="filter-tabs" @change="onFilterChange">
      <el-radio-button :value="null">全部</el-radio-button>
      <el-radio-button :value="2">已支付</el-radio-button>
      <el-radio-button :value="3">配送中</el-radio-button>
      <el-radio-button :value="4">已完成</el-radio-button>
      <el-radio-button :value="5">已取消</el-radio-button>
    </el-radio-group>

    <el-table :data="orders" v-loading="loading" stripe>
      <el-table-column label="订单号" width="190">
        <template #default="{ row }"><span class="mono">{{ row.id }}</span></template>
      </el-table-column>
      <el-table-column label="金额" width="120">
        <template #default="{ row }"><span class="price-text">{{ money(row.total_price) }}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="orderStatusTag[row.status]" effect="light" round>{{ orderStatusText[row.status] }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ shortDateTime(row.CreateTime) }}</template>
      </el-table-column>
      <el-table-column label="操作">
        <template #default="{ row }">
          <el-button size="small" text type="primary" @click="$router.push(`/orders/${row.id}`)">详情</el-button>
          <el-button v-if="[1, 2].includes(row.status)" size="small" text type="warning" @click="cancel(row)">取消</el-button>
          <el-button v-if="row.status === 4" size="small" text type="danger" @click="refund(row)">退款</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-wrap">
      <el-pagination
        background
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :page-sizes="[5, 10, 20]"
        layout="total,sizes,prev,pager,next"
        :total="total"
        @current-change="fetch"
        @size-change="fetch"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api/index.js'
import { money, orderStatusTag, orderStatusText, shortDateTime } from '../utils/display.js'

const refreshUser = inject('refreshUser', () => {})
const orders = ref([])
const loading = ref(false)
const statusFilter = ref(null)
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)

function onFilterChange() {
  page.value = 1
  fetch()
}

async function fetch() {
  loading.value = true
  try {
    const params = { page: page.value, page_size: pageSize.value }
    if (statusFilter.value != null) params.status = statusFilter.value
    const res = await api.getOrders(params)
    orders.value = res.data?.list || []
    total.value = res.data?.total || 0
  } finally {
    loading.value = false
  }
}

async function cancel(row) {
  let reason = ''
  try {
    const r = await ElMessageBox.prompt('取消原因（可选）', '取消订单')
    reason = r.value
  } catch {
    return
  }
  try {
    await api.cancelOrder(row.id, reason || '用户取消')
    ElMessage.success('已取消，余额已退回')
    refreshUser()
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '取消失败')
  }
}

async function refund(row) {
  let reason = ''
  try {
    const r = await ElMessageBox.prompt('退款原因（可选）', '申请退款')
    reason = r.value
  } catch {
    return
  }
  try {
    await api.refundOrder(row.id, reason || '用户退款')
    ElMessage.success('已退款，余额已退回')
    refreshUser()
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '退款失败')
  }
}

onMounted(fetch)
</script>
