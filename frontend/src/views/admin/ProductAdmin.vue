<template>
  <div>
    <div class="page-header">
      <div>
        <h3>商品管理</h3>
        <p class="page-subtitle">维护商品信息，并同步刷新商品缓存和库存缓存</p>
      </div>
      <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon> 新增商品</el-button>
    </div>

    <el-table :data="products" v-loading="loading" stripe>
      <el-table-column prop="id" label="ID" width="120">
        <template #default="{ row }"><span class="mono">{{ row.id }}</span></template>
      </el-table-column>
      <el-table-column label="名称">
        <template #default="{ row }">{{ productName(row) }}</template>
      </el-table-column>
      <el-table-column label="价格" width="110">
        <template #default="{ row }"><span class="price-text">{{ money(row.price) }}</span></template>
      </el-table-column>
      <el-table-column prop="stock" label="库存" width="90" />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 1 ? 'success' : 'info'" round size="small">
            {{ row.status === 1 ? '在售' : '下架' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="220">
        <template #default="{ row }">
          <el-button size="small" text @click="openDialog(row)">编辑</el-button>
          <el-button size="small" text :type="row.status === 1 ? 'warning' : 'success'" @click="toggleStatus(row)">
            {{ row.status === 1 ? '下架' : '上架' }}
          </el-button>
          <el-button size="small" text type="danger" @click="del(row.id)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editing?.id ? '编辑商品' : '新增商品'" width="500px">
      <el-form :model="form" label-width="80px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="价格"><el-input v-model.number="form.price" type="number" /></el-form-item>
        <el-form-item label="库存"><el-input v-model.number="form.stock" type="number" /></el-form-item>
        <el-form-item label="图片 URL"><el-input v-model="form.image_url" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { Plus } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../../api/index.js'
import { money, productName } from '../../utils/display.js'

const products = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const saving = ref(false)
const editing = ref(null)
const form = reactive({ name: '', description: '', price: 0, stock: 0, image_url: '' })

onMounted(fetch)

async function fetch() {
  loading.value = true
  try {
    const res = await api.getProducts({ page: 1, page_size: 200 })
    products.value = res.data?.list || []
  } finally {
    loading.value = false
  }
}

function openDialog(p) {
  editing.value = p || null
  if (p) Object.assign(form, p)
  else Object.keys(form).forEach((k) => { form[k] = k === 'price' || k === 'stock' ? 0 : '' })
  dialogVisible.value = true
}

async function save() {
  saving.value = true
  try {
    if (editing.value?.id) await api.adminUpdateProduct(editing.value.id, form)
    else await api.adminCreateProduct(form)
    ElMessage.success('保存成功')
    dialogVisible.value = false
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '保存失败')
  } finally {
    saving.value = false
  }
}

async function toggleStatus(row) {
  try {
    await api.adminUpdateProductStatus(row.id, row.status === 1 ? 0 : 1)
    ElMessage.success(row.status === 1 ? '已下架' : '已上架')
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '操作失败')
  }
}

async function del(id) {
  try {
    await ElMessageBox.confirm('确认删除该商品？', '删除商品', { type: 'warning' })
  } catch {
    return
  }
  await api.adminDeleteProduct(id).then(() => {
    ElMessage.success('已删除')
    fetch()
  }).catch(() => {})
}
</script>
