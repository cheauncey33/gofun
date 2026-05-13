<template>
  <div>
    <div class="page-header">
      <div>
        <h3>商品浏览</h3>
        <p class="page-subtitle">筛选商品、加入购物车或进入详情页购买</p>
      </div>
    </div>

    <div class="toolbar">
      <el-input
        v-model="search"
        placeholder="搜索零食"
        clearable
        class="toolbar-search"
        @change="page = 1; fetch()"
      >
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-select v-model="categoryId" placeholder="全部分类" clearable class="toolbar-select" @change="page = 1; fetch()">
        <el-option v-for="c in categories" :key="c.id" :label="c.name" :value="c.id" />
      </el-select>
      <el-select v-model="sortBy" class="toolbar-select" @change="page = 1; fetch()">
        <el-option label="默认排序" value="" />
        <el-option label="价格从低到高" value="price_asc" />
        <el-option label="价格从高到低" value="price_desc" />
        <el-option label="销量优先" value="sales" />
      </el-select>
    </div>

    <div class="card-grid" v-loading="loading">
      <el-card
        v-for="p in products"
        :key="p.id"
        shadow="hover"
        class="product-card"
        :body-style="{ padding: 0 }"
        @click="$router.push(`/product/${p.id}`)"
      >
        <el-image :src="imageUrl(p.image_url)" fit="cover" class="product-image" />
        <div class="product-body">
          <div class="product-title">{{ productName(p) }}</div>
          <div class="product-price">{{ money(p.price) }}</div>
          <div class="product-meta">
            <span>库存 {{ p.stock }}</span>
            <span>已售 {{ p.sales_count }}</span>
          </div>
          <el-button type="primary" size="small" class="full-button" @click.stop="cart.addItem(p, 1)">
            加入购物车
          </el-button>
        </div>
      </el-card>
      <el-empty v-if="!loading && products.length === 0" description="没有找到商品" class="grid-empty" />
    </div>

    <div class="pagination-wrap">
      <el-pagination
        background
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :page-sizes="[8, 16, 24]"
        layout="total,sizes,prev,pager,next,jumper"
        :total="total"
        @current-change="fetch"
        @size-change="fetch"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { useCart } from '../stores/cart.js'
import api from '../api/index.js'
import { imageUrl, money, productName } from '../utils/display.js'

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
      page: page.value,
      page_size: pageSize.value,
      keyword: search.value || undefined,
      category_id: categoryId.value || undefined,
      sort_by: sortBy.value || undefined,
    })
    products.value = res.data?.list || []
    total.value = res.data?.total || 0
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  try {
    const r = await api.getCategories()
    categories.value = r.data || []
  } catch {}
  fetch()
})
</script>
