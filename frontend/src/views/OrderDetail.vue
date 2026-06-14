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
      <el-button v-if="order.status === 1" type="primary" @click="pay">支付订单</el-button>
      <el-button v-if="order.status === 2" type="success" @click="confirm">确认收货</el-button>
      <el-button v-if="order.status === 1" type="warning" @click="cancel">取消订单</el-button>
      <el-button v-if="[2, 3].includes(order.status)" type="danger" @click="refund">申请退款</el-button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, inject } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessageBox, ElNotification } from 'element-plus'
import api from '../api/index.js'
import { money, orderStatusTag, orderStatusText, productName, shortDateTime } from '../utils/display.js'

const route = useRoute()
const refreshUser = inject('refreshUser', () => {})
const order = ref({})
const loading = ref(false)

const statusLabel = computed(() => orderStatusText[order.value.status] || order.value.status || '-')
const tagType = computed(() => orderStatusTag[order.value.status] || '')

onMounted(loadOrder)

function notify(type, title, message) {
  ElNotification({
    type,
    title,
    message,
    position: 'top-right',
    duration: 2600,
  })
}

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
    const wasPending = order.value.status === 1
    await api.cancelOrder(order.value.id, reason || '用户取消')
    notify('success', '订单已取消', wasPending ? '库存已回补' : '库存和余额已退回')
    refreshUser()
    loadOrder()
  } catch (e) {
    notify('error', '取消失败', e.response?.data?.msg || '请稍后重试')
  }
}

async function pay() {
  try {
    await ElMessageBox.confirm(`确认支付 ${money(order.value.total_price)}？`, '支付订单', {
      type: 'info',
      confirmButtonText: '确认支付',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    const amount = money(order.value.total_price)
    await api.payOrder(order.value.id)
    notify('success', '支付成功', `已扣款 ${amount}，订单变为已支付`)
    refreshUser()
    loadOrder()
  } catch (e) {
    notify('error', '支付失败', e.response?.data?.msg || '请检查余额或稍后重试')
  }
}

async function confirm() {
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
    await api.confirmOrder(order.value.id)
    notify('success', '确认收货成功', '订单已变为已完成')
    loadOrder()
  } catch (e) {
    notify('error', '确认失败', e.response?.data?.msg || '请稍后重试')
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
    notify('success', '退款成功', '订单已取消，库存和余额已退回')
    refreshUser()
    loadOrder()
  } catch (e) {
    notify('error', '退款失败', e.response?.data?.msg || '请稍后重试')
  }
}
</script>
