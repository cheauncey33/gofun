<template>
  <div style="max-width:640px">
    <div class="page-header"><h3>个人中心</h3></div>

    <el-card shadow="never" style="margin-bottom:24px">
      <template #header><span style="font-weight:700">基本信息</span></template>
      <el-descriptions :column="1">
        <el-descriptions-item label="用户名">{{ user.username }}</el-descriptions-item>
        <el-descriptions-item label="余额">
          <span style="color:#E17055;font-weight:700;font-size:18px">¥{{ user.balance?.toFixed(2) }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="手机号">{{ user.phone || '未设置' }}</el-descriptions-item>
        <el-descriptions-item label="角色">
          <el-tag v-if="user.role==='admin'" type="danger" effect="dark" size="small">管理员</el-tag>
          <span v-else>普通用户</span>
        </el-descriptions-item>
        <el-descriptions-item label="注册时间">{{ user.CreateTime?.slice(0,10) }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" style="margin-bottom:16px">
      <template #header><span style="font-weight:700">修改信息</span></template>
      <el-form :model="infoForm" label-width="80px">
        <el-form-item label="手机号"><el-input v-model="infoForm.phone" placeholder="11位手机号" /></el-form-item>
        <el-form-item label="头像URL"><el-input v-model="infoForm.avatar_url" placeholder="头像图片链接" /></el-form-item>
        <el-button type="primary" :loading="saving" @click="saveInfo">保存</el-button>
      </el-form>
    </el-card>

    <el-card shadow="never">
      <template #header><span style="font-weight:700">修改密码</span></template>
      <el-form ref="pwdRef" :model="pwdForm" :rules="pwdRules" label-width="100px">
        <el-form-item label="原密码" prop="old_password">
          <el-input v-model="pwdForm.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码" prop="new_password">
          <el-input v-model="pwdForm.new_password" type="password" show-password />
        </el-form-item>
        <el-button type="danger" :loading="changing" @click="changePwd">修改密码</el-button>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, inject } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../api/index.js'

const refreshUser = inject('refreshUser', () => {})
const user = ref({})
const saving = ref(false)
const changing = ref(false)
const infoForm = reactive({ phone: '', avatar_url: '' })
const pwdForm = reactive({ old_password: '', new_password: '' })
const pwdRef = ref(null)
const pwdRules = {
  old_password: [{ required: true, message: '请输入原密码' }],
  new_password: [{ required: true, min: 6, message: '密码至少6位' }],
}

onMounted(async () => {
  try {
    const res = await api.getUserInfo()
    user.value = res.data
    infoForm.phone = res.data?.phone || ''
    infoForm.avatar_url = res.data?.avatar_url || ''
  } catch {}
})

async function saveInfo() {
  saving.value = true
  try {
    await api.updateUserInfo(infoForm)
    ElMessage.success('保存成功')
    refreshUser()
    const res = await api.getUserInfo(); user.value = res.data
  } catch (e) { ElMessage.error(e.response?.data?.msg) } finally { saving.value = false }
}

async function changePwd() {
  const valid = await pwdRef.value?.validate().catch(() => false)
  if (!valid) return
  changing.value = true
  try {
    await api.changePassword(pwdForm.old_password, pwdForm.new_password)
    ElMessage.success('密码修改成功')
    pwdForm.old_password = pwdForm.new_password = ''
  } catch (e) { ElMessage.error(e.response?.data?.msg) } finally { changing.value = false }
}
</script>
