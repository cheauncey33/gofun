<template>
  <div>
    <div class="page-header">
      <div>
        <h3>数据看板</h3>
        <p class="page-subtitle">订单、营收、商品和秒杀活动的实时统计</p>
      </div>
      <el-button :loading="loading" @click="fetch">刷新</el-button>
    </div>

    <el-alert
      v-if="loading"
      title="压测数据量较大，正在统计看板数据"
      type="info"
      show-icon
      :closable="false"
      class="load-alert"
    />

    <template v-if="loading && !loaded">
      <div class="stats-row">
        <el-skeleton v-for="i in 7" :key="i" animated class="stat-card skeleton-card">
          <template #template>
            <el-skeleton-item variant="text" style="width: 45%" />
            <el-skeleton-item variant="h1" style="width: 70%; margin-top: 12px" />
          </template>
        </el-skeleton>
      </div>
      <el-row :gutter="20">
        <el-col :xs="24" :md="12"><el-skeleton animated :rows="8" /></el-col>
        <el-col :xs="24" :md="12"><el-skeleton animated :rows="8" /></el-col>
      </el-row>
    </template>

    <template v-else>
      <div class="stats-row">
        <div class="stat-card">
          <div class="stat-label">总用户</div>
          <div class="stat-value">{{ stats.total_users || 0 }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">总订单</div>
          <div class="stat-value">{{ stats.total_orders || 0 }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">总营收</div>
          <div class="stat-value accent">{{ money(stats.total_revenue) }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">今日订单</div>
          <div class="stat-value">{{ stats.today_orders || 0 }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">今日营收</div>
          <div class="stat-value accent">{{ money(stats.today_revenue) }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">在售商品</div>
          <div class="stat-value">{{ stats.products_on_sale || 0 }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">活跃秒杀</div>
          <div class="stat-value primary">{{ stats.active_seckills || 0 }}</div>
        </div>
      </div>

      <el-row :gutter="20">
        <el-col :xs="24" :md="12">
          <el-card shadow="never">
            <template #header><span class="section-title">热门商品 Top 5</span></template>
            <el-table :data="stats.top_products || []" stripe>
              <el-table-column label="商品">
                <template #default="{ row }">{{ productName(row, '商品') }}</template>
              </el-table-column>
              <el-table-column prop="sales_count" label="销量" width="90" />
              <el-table-column label="营收" width="130">
                <template #default="{ row }">{{ money(row.revenue) }}</template>
              </el-table-column>
            </el-table>
          </el-card>
        </el-col>
        <el-col :xs="24" :md="12">
          <el-card shadow="never">
            <template #header><span class="section-title">近 7 日趋势</span></template>
            <el-table :data="stats.order_trend || []" stripe>
              <el-table-column prop="date" label="日期" />
              <el-table-column prop="count" label="订单" width="90" />
              <el-table-column label="金额" width="130">
                <template #default="{ row }">{{ money(row.amount) }}</template>
              </el-table-column>
            </el-table>
          </el-card>
        </el-col>
      </el-row>
    </template>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import api from '../../api/index.js'
import { money, productName } from '../../utils/display.js'

const loading = ref(false)
const loaded = ref(false)
const stats = ref({ top_products: [], order_trend: [] })

async function fetch() {
  loading.value = true
  try {
    const res = await api.getDashboard()
    stats.value = res.data || { top_products: [], order_trend: [] }
    loaded.value = true
  } finally {
    loading.value = false
  }
}

onMounted(fetch)
</script>
