<template>
  <div>
    <div class="page-header">
      <div>
        <h3>秒杀管理</h3>
        <p class="page-subtitle">创建活动、预热库存并管理限时抢购</p>
      </div>
      <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon> 新增活动</el-button>
    </div>

    <el-table :data="activities" v-loading="loading" stripe>
      <el-table-column prop="id" label="ID" width="90" />
      <el-table-column label="活动名称">
        <template #default="{ row }">{{ activityTitle(row) }}</template>
      </el-table-column>
      <el-table-column label="秒杀价" width="110">
        <template #default="{ row }"><span class="price-text">{{ money(row.seckill_price) }}</span></template>
      </el-table-column>
      <el-table-column prop="stock" label="库存" width="90" />
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="row.status === 1 ? 'danger' : row.status === 0 ? 'warning' : 'info'" round size="small">
            {{ ({ 0: '未开始', 1: '进行中', 2: '已结束' })[row.status] || '未知' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="起止时间" width="280">
        <template #default="{ row }">{{ shortDateTime(row.start_time).slice(0, 16) }} 至 {{ shortDateTime(row.end_time).slice(0, 16) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="210">
        <template #default="{ row }">
          <el-button size="small" text @click="openDialog(row)">编辑</el-button>
          <el-button v-if="row.status === 0" size="small" text type="warning" @click="warmUp(row)">预热</el-button>
          <el-button size="small" text type="danger" @click="del(row.id)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editing?.id ? '编辑活动' : '新增活动'" width="520px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="活动名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="商品 ID"><el-input v-model.number="form.product_id" type="number" /></el-form-item>
        <el-form-item label="秒杀价"><el-input v-model.number="form.seckill_price" type="number" /></el-form-item>
        <el-form-item label="库存"><el-input v-model.number="form.stock" type="number" /></el-form-item>
        <el-form-item label="限购数"><el-input v-model.number="form.limit_per_user" type="number" /></el-form-item>
        <el-form-item label="开始时间"><el-input v-model="form.start_time" type="datetime-local" /></el-form-item>
        <el-form-item label="结束时间"><el-input v-model="form.end_time" type="datetime-local" /></el-form-item>
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
import { isGarbledText, money, productName, shortDateTime } from '../../utils/display.js'

const activities = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const saving = ref(false)
const editing = ref(null)
const form = reactive({ name: '', product_id: null, seckill_price: 0, stock: 0, limit_per_user: 1, start_time: '', end_time: '' })

onMounted(fetch)

async function fetch() {
  loading.value = true
  try {
    const res = await api.getSeckillActivities({ page: 1, page_size: 50 })
    activities.value = res.data?.list || []
  } finally {
    loading.value = false
  }
}

function activityTitle(activity) {
  if (!isGarbledText(activity.name)) return activity.name
  return productName(activity.product, '秒杀商品')
}

function openDialog(a) {
  editing.value = a || null
  if (a) Object.assign(form, a)
  else Object.keys(form).forEach((k) => { form[k] = k === 'limit_per_user' ? 1 : '' })
  dialogVisible.value = true
}

async function save() {
  saving.value = true
  try {
    if (editing.value?.id) await api.adminUpdateSeckill(editing.value.id, form)
    else await api.adminCreateSeckill(form)
    ElMessage.success('保存成功')
    dialogVisible.value = false
    fetch()
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '保存失败')
  } finally {
    saving.value = false
  }
}

async function del(id) {
  try {
    await ElMessageBox.confirm('确认删除该活动？', '删除活动', { type: 'warning' })
  } catch {
    return
  }
  await api.adminDeleteSeckill(id).then(() => {
    ElMessage.success('已删除')
    fetch()
  })
}

async function warmUp(row) {
  await api.adminWarmUpSeckill(row.id).then(() => {
    ElMessage.success('预热成功')
    fetch()
  }).catch((e) => ElMessage.error(e.response?.data?.msg || '预热失败'))
}
</script>
