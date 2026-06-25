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
      <el-radio-button :value="3">已完成</el-radio-button>
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
          <el-button v-if="row.status === 1" size="small" text type="primary" @click="pay(row)">支付</el-button>
          <el-button v-if="row.status === 2" size="small" text type="success" @click="confirm(row)">确认收货</el-button>
          <el-button v-if="row.status === 1" size="small" text type="warning" @click="cancel(row)">取消</el-button>
          <el-button v-if="[2, 3].includes(row.status)" size="small" text type="danger" @click="refund(row)">退款</el-button>
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
import { ref, onMounted, onUnmounted, inject } from 'vue'
import { ElMessageBox, ElNotification } from 'element-plus'
import api from '../api/index.js'
import { money, orderStatusTag, orderStatusText, shortDateTime } from '../utils/display.js'

const refreshUser = inject('refreshUser', () => {})
const orders = ref([])
const loading = ref(false)
const statusFilter = ref(null)
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)

function notify(type, title, message) {
  ElNotification({
    type,
    title,
    message,
    position: 'top-right',
    duration: 2600,
  })
}

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
    notify('success', '订单已取消', row.status === 1 ? '库存已回补' : '库存和余额已退回')
    refreshUser()
    fetch()
  } catch (e) {
    notify('error', '取消失败', e.response?.data?.msg || '请稍后重试')
  }
}

async function pay(row) {
  try {
    await ElMessageBox.confirm(`确认支付 ${money(row.total_price)}？`, '支付订单', {
      type: 'info',
      confirmButtonText: '确认支付',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.payOrder(row.id)
    notify('success', '支付成功', `已扣款 ${money(row.total_price)}，订单变为已支付`)
    refreshUser()
    fetch()
  } catch (e) {
    notify('error', '支付失败', e.response?.data?.msg || '请检查余额或稍后重试')
  }
}

async function confirm(row) {
  try {
    await ElMessageBox.confirm('确认已收到商品并完成订单？', '确认收货', {
      type: 'success',
      confirmButtonText: '确认收货',
      cancelButtonText: '再等等',
    })
  } catch {
    return
  }
  try {
    await api.confirmOrder(row.id)
    notify('success', '确认收货成功', '订单已变为已完成')
    fetch()
  } catch (e) {
    notify('error', '确认失败', e.response?.data?.msg || '请稍后重试')
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
    notify('success', '退款成功', '订单已取消，库存和余额已退回')
    refreshUser()
    fetch()
  } catch (e) {
    notify('error', '退款失败', e.response?.data?.msg || '请稍后重试')
  }
}

// 收到 WebSocket 推送的订单状态变更后刷新列表，保证页面与后端最终状态一致
function onOrderStatus() {
  fetch()
}

onMounted(() => {
  fetch()
  window.addEventListener('order:status', onOrderStatus)
})

onUnmounted(() => {
  window.removeEventListener('order:status', onOrderStatus)
})
</script>
