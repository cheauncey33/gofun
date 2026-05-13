<template>
  <div>
    <!-- Search + Filter bar -->
    <div style="display:flex;gap:12px;margin-bottom:24px;flex-wrap:wrap;align-items:center">
      <el-input v-model="search" placeholder="搜索零食..." style="width:260px" size="large" clearable
        @change="page=1;fetch" :prefix-icon="Search">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-select v-model="categoryId" placeholder="全部分类" style="width:150px" size="large" clearable @change="page=1;fetch">
        <el-option v-for="c in categories" :key="c.id" :label="c.name" :value="c.id" />
      </el-select>
      <el-select v-model="sortBy" style="width:150px" size="large" @change="page=1;fetch">
        <el-option label="默认排序" value="" />
        <el-option label="💰 价格从低到高" value="price_asc" />
        <el-option label="💰 价格从高到低" value="price_desc" />
        <el-option label="🔥 销量优先" value="sales" />
      </el-select>
    </div>

    <!-- Product cards -->
    <div class="card-grid" v-loading="loading">
      <el-card v-for="p in products" :key="p.id" shadow="hover" class="product-card"
        :body-style="{padding:0}" @click="$router.push(`/product/${p.id}`)">
        <el-image :src="p.image_url || 'https://placehold.co/400x320/f0f0ff/6C5CE7?text=Snack'" fit="cover" style="height:180px;width:100%" />
        <div style="padding:14px 16px 16px">
          <div style="font-weight:600;font-size:15px;margin-bottom:6px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ p.name }}</div>
          <div style="display:flex;align-items:baseline;gap:6px">
            <span style="color:#E17055;font-size:22px;font-weight:700">¥{{ p.price?.toFixed(2) }}</span>
          </div>
          <div style="display:flex;justify-content:space-between;margin-top:6px;color:var(--text-secondary);font-size:12px">
            <span>库存 {{ p.stock }}</span>
            <span>已售 {{ p.sales_count }}</span>
          </div>
          <el-button type="primary" size="small" style="margin-top:8px;width:100%;border-radius:8px"
            @click.stop="cart.addItem(p, 1)">加入购物车</el-button>
        </div>
      </el-card>
      <el-empty v-if="!loading && products.length===0" description="没有找到商品" style="grid-column:1/-1;padding:60px 0" />
    </div>

    <!-- Pagination -->
    <div style="margin-top:28px;display:flex;justify-content:center">
      <el-pagination background
        v-model:current-page="page" v-model:page-size="pageSize"
        :page-sizes="[8,16,24]" layout="total,sizes,prev,pager,next,jumper"
        :total="total" @current-change="fetch" @size-change="fetch"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { useCart } from '../stores/cart.js'
import api from '../api/index.js'

const cart = useCart()

const products = ref([])
const categories = ref([])
const loading = ref(false)
const search = ref('')
const categoryId = ref(null)
const sortBy = ref('')
const page = ref(1)
const pageSize = ref(8)
const total = ref(0)

async function fetch() {
  loading.value = true
  try {
    const res = await api.getProducts({
      page: page.value, page_size: pageSize.value,
      keyword: search.value || undefined,
      category_id: categoryId.value || undefined,
      sort_by: sortBy.value || undefined,
    })
    products.value = res.data?.list || []
    total.value = res.data?.total || 0
  } finally { loading.value = false }
}

onMounted(async () => {
  try { const r = await api.getCategories(); categories.value = r.data || [] } catch {}
  fetch()
})
</script>
