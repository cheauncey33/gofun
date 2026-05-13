<template>
  <div v-loading="loading">
    <div class="page-header"><h3>数据看板</h3></div>

    <div class="stats-row">
      <div class="stat-card">
        <div class="stat-label">总用户</div>
        <div class="stat-value">{{ stats.total_users }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">总订单</div>
        <div class="stat-value">{{ stats.total_orders }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">总营收</div>
        <div class="stat-value" style="color:#E17055">¥{{ stats.total_revenue?.toFixed(2) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">今日订单</div>
        <div class="stat-value">{{ stats.today_orders }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">今日营收</div>
        <div class="stat-value" style="color:#E17055">¥{{ stats.today_revenue?.toFixed(2) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">在售商品</div>
        <div class="stat-value">{{ stats.products_on_sale }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">活跃秒杀</div>
        <div class="stat-value" style="color:var(--primary)">{{ stats.active_seckills }}</div>
      </div>
    </div>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-card shadow="never">
          <template #header><span style="font-weight:700">热门商品 Top 5</span></template>
          <el-table :data="stats.top_products" stripe>
            <el-table-column prop="product_name" label="商品" />
            <el-table-column prop="sales_count" label="销量" width="80" />
            <el-table-column label="营收" width="120"><template #default="{row}">¥{{ row.revenue?.toFixed(2) }}</template></el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card shadow="never">
          <template #header><span style="font-weight:700">近7日趋势</span></template>
          <el-table :data="stats.order_trend" stripe>
            <el-table-column prop="date" label="日期" />
            <el-table-column prop="count" label="订单" width="80" />
            <el-table-column label="金额" width="120"><template #default="{row}">¥{{ row.amount?.toFixed(2) }}</template></el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import api from '../../api/index.js'

const loading = ref(false)
const stats = ref({ top_products: [], order_trend: [] })

onMounted(async () => {
  loading.value = true
  try { const res = await api.getDashboard(); stats.value = res.data || {} } finally { loading.value = false }
})
</script>
