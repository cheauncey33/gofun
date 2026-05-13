<template>
  <div>
    <div class="page-header">
      <div>
        <h3>秒杀活动</h3>
        <p class="page-subtitle">查看正在进行和即将开始的限时抢购</p>
      </div>
    </div>

    <div v-loading="loading">
      <el-card
        v-for="a in activities"
        :key="a.id"
        shadow="hover"
        class="seckill-card"
        :body-style="{ padding: 0 }"
        @click="$router.push(`/seckill/${a.id}`)"
      >
        <div class="seckill-card-inner">
          <div class="seckill-thumb">
            <el-image :src="imageUrl(a.product?.image_url)" fit="cover" />
            <div v-if="a.status === 1" class="seckill-badge">抢购中</div>
          </div>
          <div class="seckill-info">
            <div class="seckill-title">{{ activityTitle(a) }}</div>
            <div class="seckill-price-line">
              <span class="seckill-price">{{ money(a.seckill_price) }}</span>
              <span class="origin-price">{{ money(a.product?.price) }}</span>
            </div>
            <div class="seckill-meta">
              <span>库存 {{ a.remaining_stock || a.stock }}</span>
              <span>限购 {{ a.limit_per_user }} 件/人</span>
              <span>{{ shortDateTime(a.start_time).slice(0, 16) }} 至 {{ shortDateTime(a.end_time).slice(0, 16) }}</span>
            </div>
          </div>
        </div>
      </el-card>
      <el-empty v-if="!loading && activities.length === 0" description="暂无秒杀活动" />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import api from '../api/index.js'
import { imageUrl, isGarbledText, money, productName, shortDateTime } from '../utils/display.js'

const activities = ref([])
const loading = ref(false)

onMounted(async () => {
  loading.value = true
  try {
    const res = await api.getSeckillActivities({ page: 1, page_size: 20 })
    activities.value = res.data?.list || []
  } finally {
    loading.value = false
  }
})

function activityTitle(activity) {
  if (!isGarbledText(activity.name)) return activity.name
  return productName(activity.product, '秒杀商品')
}
</script>
