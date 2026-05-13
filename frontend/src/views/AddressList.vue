<template>
  <div style="max-width:800px">
    <div class="page-header">
      <h3>收货地址</h3>
      <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon> 新增地址</el-button>
    </div>

    <div v-loading="loading">
      <el-card v-for="addr in addresses" :key="addr.id" shadow="never"
        :body-style="{padding:'16px 20px'}" style="margin-bottom:12px"
        :style="addr.is_default ? 'border:2px solid var(--primary)' : ''">
        <div style="display:flex;justify-content:space-between;align-items:center">
          <div>
            <div style="margin-bottom:4px">
              <span style="font-weight:700;font-size:16px">{{ addr.receiver_name }}</span>
              <span style="margin-left:16px;color:var(--text-secondary)">{{ addr.phone }}</span>
              <el-tag v-if="addr.is_default" size="small" effect="dark" type="primary" style="margin-left:12px">默认</el-tag>
            </div>
            <div style="color:var(--text-secondary);font-size:14px">
              {{ addr.province }}{{ addr.city }}{{ addr.district }} {{ addr.detail }}
            </div>
          </div>
          <div style="display:flex;gap:8px">
            <el-button v-if="!addr.is_default" size="small" text @click="setDefault(addr.id)">设为默认</el-button>
            <el-button size="small" text @click="openDialog(addr)">编辑</el-button>
            <el-button size="small" text type="danger" @click="del(addr.id)">删除</el-button>
          </div>
        </div>
      </el-card>
      <el-empty v-if="!loading && addresses.length===0" description="还没有收货地址，快去添加吧" />
    </div>

    <el-dialog v-model="dialogVisible" :title="editing?.id ? '编辑地址' : '新增地址'" width="500px">
      <el-form ref="formRef" :model="form" :rules="addrRules" label-width="80px">
        <el-form-item label="收货人" prop="receiver_name"><el-input v-model="form.receiver_name" /></el-form-item>
        <el-form-item label="手机号" prop="phone"><el-input v-model="form.phone" /></el-form-item>
        <el-row :gutter="12">
          <el-col :span="8"><el-form-item label="省" prop="province"><el-input v-model="form.province" /></el-form-item></el-col>
          <el-col :span="8"><el-form-item label="市" prop="city"><el-input v-model="form.city" /></el-form-item></el-col>
          <el-col :span="8"><el-form-item label="区" prop="district"><el-input v-model="form.district" /></el-form-item></el-col>
        </el-row>
        <el-form-item label="详细地址" prop="detail">
          <el-input v-model="form.detail" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api/index.js'

const addresses = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const saving = ref(false)
const editing = ref(null)
const formRef = ref(null)
const form = reactive({ receiver_name: '', phone: '', province: '', city: '', district: '', detail: '' })
const addrRules = {
  receiver_name: [{ required: true, message: '请输入收货人' }],
  phone: [{ required: true, message: '请输入手机号' }],
}

onMounted(fetch)

async function fetch() {
  loading.value = true
  try { const res = await api.getAddresses(); addresses.value = res.data || [] } finally { loading.value = false }
}

function openDialog(addr) {
  editing.value = addr || null
  if (addr) Object.assign(form, addr)
  else Object.keys(form).forEach(k => form[k] = '')
  dialogVisible.value = true
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    if (editing.value?.id) await api.updateAddress(editing.value.id, form)
    else await api.createAddress(form)
    ElMessage.success('保存成功')
    dialogVisible.value = false; fetch()
  } catch (e) { ElMessage.error(e.response?.data?.msg) } finally { saving.value = false }
}

async function del(id) {
  try { await ElMessageBox.confirm('确认删除？', '提示', { type: 'warning' }) } catch { return }
  await api.deleteAddress(id).then(() => { ElMessage.success('已删除'); fetch() }).catch(() => {})
}

async function setDefault(id) {
  await api.setDefaultAddress(id).then(() => { ElMessage.success('已设为默认'); fetch() }).catch(() => {})
}
</script>
