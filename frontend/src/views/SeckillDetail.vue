<template>
  <div v-loading="loading" style="max-width:900px">
    <div class="page-header">
      <h3>{{ activity.name || activity.product?.name }}</h3>
      <el-tag v-if="activity.status===1" type="danger" effect="dark" size="large" round>抢购中</el-tag>
      <el-tag v-else-if="activity.status===0" type="warning" size="large" round>即将开始</el-tag>
      <el-tag v-else type="info" size="large" round>已结束</el-tag>
    </div>

    <el-row :gutter="40">
      <el-col :span="10">
        <el-image :src="activity.product?.image_url || 'https://placehold.co/400x400/f0f0ff/6C5CE7?text=Snack'"
          fit="cover" style="width:100%;border-radius:16px;box-shadow:var(--shadow)" />
      </el-col>
      <el-col :span="14">
        <div style="font-size:18px;color:var(--text-secondary);margin-bottom:16px">{{ activity.product?.name }}</div>
        <div style="background:linear-gradient(135deg,#FFF5F5,#FFF0F0);border-radius:12px;padding:20px;margin-bottom:16px">
          <div style="display:flex;align-items:baseline;gap:12px">
            <span style="color:#E17055;font-size:36px;font-weight:800">¥{{ activity.seckill_price?.toFixed(2) }}</span>
            <span style="color:#ccc;text-decoration:line-through">¥{{ activity.product?.price?.toFixed(2) }}</span>
          </div>
        </div>
        <div style="display:flex;gap:32px;margin-bottom:24px;color:var(--text-secondary)">
          <div>库存 <b style="color:var(--text)">{{ activity.remaining_stock || activity.stock }}</b></div>
          <div>限购 <b style="color:var(--text)">{{ activity.limit_per_user }}</b> 件/人</div>
        </div>

        <div v-if="activity.status===1">
          <el-alert v-if="!token" title="请先获取秒杀令牌" type="warning" show-icon :closable="false" style="margin-bottom:16px;border-radius:12px" />
          <el-button v-if="!token" type="danger" size="large" :loading="gettingToken" @click="getToken"
            style="height:48px;padding:0 40px;font-size:16px;font-weight:600;border-radius:12px">
            获取秒杀令牌
          </el-button>
          <div v-else>
            <el-alert title="令牌已获取，有效期 60 秒" type="success" show-icon :closable="false" style="margin-bottom:16px;border-radius:12px" />
            <div style="display:flex;align-items:center;gap:16px">
              <el-input-number v-model="quantity" :min="1" :max="activity.limit_per_user" size="large" style="width:120px" />
              <el-button type="danger" size="large" :loading="executing" @click="execute"
                style="height:48px;padding:0 40px;font-size:16px;font-weight:600;border-radius:12px">
                ⚡ 立即秒杀
              </el-button>
            </div>
          </div>
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api/index.js'

const route = useRoute()
const refreshUser = inject('refreshUser', () => {})
const activity = ref({ product: {} })
const loading = ref(false)
const gettingToken = ref(false)
const executing = ref(false)
const token = ref('')
const quantity = ref(1)

onMounted(async () => {
  loading.value = true
  try { const res = await api.getSeckillDetail(route.params.id); activity.value = res.data } finally { loading.value = false }
})

async function getToken() {
  gettingToken.value = true
  try { const res = await api.getSeckillToken(activity.value.id); token.value = res.data.token; ElMessage.success('令牌获取成功！') }
  catch (e) { ElMessage.error(e.response?.data?.msg) } finally { gettingToken.value = false }
}

async function execute() {
  executing.value = true
  try {
    await api.executeSeckill(activity.value.id, token.value, quantity.value)
    ElMessage.success('秒杀成功！')
    refreshUser()
    token.value = ''
    const r = await api.getSeckillDetail(activity.value.id)
    activity.value = r.data
  }
  catch (e) { ElMessage.error(e.response?.data?.msg || '秒杀失败') } finally { executing.value = false }
}
</script>
