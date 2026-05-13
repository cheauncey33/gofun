<template>
  <div v-loading="loading" class="detail-page">
    <div class="page-header">
      <div>
        <h3>{{ activityTitle(activity) }}</h3>
        <p class="page-subtitle">先获取一次性令牌，再提交秒杀订单</p>
      </div>
      <el-tag v-if="activity.status === 1" type="danger" effect="dark" size="large" round>抢购中</el-tag>
      <el-tag v-else-if="activity.status === 0" type="warning" size="large" round>即将开始</el-tag>
      <el-tag v-else type="info" size="large" round>已结束</el-tag>
    </div>

    <el-row :gutter="32">
      <el-col :xs="24" :sm="10">
        <el-image :src="imageUrl(activity.product?.image_url)" fit="cover" class="detail-image" />
      </el-col>
      <el-col :xs="24" :sm="14">
        <div class="detail-kicker">{{ productName(activity.product, '秒杀商品') }}</div>
        <div class="price-panel">
          <span>{{ money(activity.seckill_price) }}</span>
          <small>{{ money(activity.product?.price) }}</small>
        </div>
        <div class="detail-meta">
          <div><b>{{ activity.remaining_stock || activity.stock || 0 }}</b><span>库存</span></div>
          <div><b>{{ activity.limit_per_user || 1 }}</b><span>限购/人</span></div>
        </div>

        <div v-if="activity.status === 1" class="seckill-actions">
          <el-alert
            v-if="!token"
            title="请先获取秒杀令牌"
            type="warning"
            show-icon
            :closable="false"
            class="load-alert"
          />
          <el-button v-if="!token" type="danger" size="large" :loading="gettingToken" @click="getToken">
            获取秒杀令牌
          </el-button>
          <div v-else>
            <el-alert title="令牌已获取，有效期 60 秒" type="success" show-icon :closable="false" class="load-alert" />
            <div class="action-row">
              <el-input-number v-model="quantity" :min="1" :max="activity.limit_per_user || 1" size="large" />
              <el-button type="danger" size="large" :loading="executing" @click="execute">
                立即秒杀
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
import { imageUrl, isGarbledText, money, productName } from '../utils/display.js'

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
  try {
    const res = await api.getSeckillDetail(route.params.id)
    activity.value = res.data || { product: {} }
  } finally {
    loading.value = false
  }
})

function activityTitle(item) {
  if (!isGarbledText(item.name)) return item.name
  return productName(item.product, '秒杀商品')
}

async function getToken() {
  gettingToken.value = true
  try {
    const res = await api.getSeckillToken(activity.value.id)
    token.value = res.data.token
    ElMessage.success('令牌获取成功')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '令牌获取失败')
  } finally {
    gettingToken.value = false
  }
}

async function execute() {
  executing.value = true
  try {
    await api.executeSeckill(activity.value.id, token.value, quantity.value)
    ElMessage.success('秒杀成功')
    refreshUser()
    token.value = ''
    const r = await api.getSeckillDetail(activity.value.id)
    activity.value = r.data
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '秒杀失败')
  } finally {
    executing.value = false
  }
}
</script>
