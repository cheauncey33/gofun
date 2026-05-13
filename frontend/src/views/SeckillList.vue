<template>
  <div>
    <div class="page-header"><h3>秒杀活动</h3></div>
    <div v-loading="loading">
      <el-card v-for="a in activities" :key="a.id" shadow="hover"
        :body-style="{padding:0}" style="margin-bottom:16px;cursor:pointer;overflow:hidden"
        @click="$router.push(`/seckill/${a.id}`)">
        <div style="display:flex;align-items:stretch">
          <div style="width:160px;min-height:140px;position:relative;overflow:hidden">
            <el-image :src="a.product?.image_url || 'https://placehold.co/160x140/f0f0ff/6C5CE7?text=Snack'"
              style="width:100%;height:100%;object-fit:cover" />
            <div v-if="a.status===1" style="position:absolute;top:0;left:0;background:#E17055;color:#fff;padding:4px 12px;font-size:12px;font-weight:700">抢购中</div>
          </div>
          <div style="flex:1;padding:20px 24px">
            <div style="font-size:18px;font-weight:700;margin-bottom:8px">{{ a.name || a.product?.name }}</div>
            <div style="display:flex;align-items:baseline;gap:12px;margin-bottom:8px">
              <span style="color:#E17055;font-size:28px;font-weight:800">¥{{ a.seckill_price?.toFixed(2) }}</span>
              <span style="color:#ccc;text-decoration:line-through;font-size:14px">¥{{ a.product?.price?.toFixed(2) }}</span>
            </div>
            <div style="display:flex;gap:24px;color:var(--text-secondary);font-size:13px">
              <span>库存 {{ a.remaining_stock || a.stock }}</span>
              <span>限购 {{ a.limit_per_user }} 件/人</span>
              <span>{{ a.start_time?.slice(0,16) }} ~ {{ a.end_time?.slice(0,16) }}</span>
            </div>
          </div>
        </div>
      </el-card>
      <el-empty v-if="!loading && activities.length===0" description="暂无秒杀活动" />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import api from '../api/index.js'

const activities = ref([])
const loading = ref(false)

onMounted(async () => {
  loading.value = true
  try { const res = await api.getSeckillActivities({ page: 1, page_size: 20 }); activities.value = res.data?.list || [] } finally { loading.value = false }
})
</script>
