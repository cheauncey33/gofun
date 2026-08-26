<template>
  <div class="account-page">
    <header>
      <h1>个人中心</h1>
      <span>管理联系方式、登录密码和观演人证件。</span>
    </header>

    <section class="account-card">
      <div class="profile-head">
        <el-avatar :size="64" :src="user.avatar_url">
          {{ (user.username || '用').charAt(0).toUpperCase() }}
        </el-avatar>
        <div>
          <strong>{{ user.username || '—' }}</strong>
          <p>{{ user.role === 'admin' ? '平台管理员' : '购票用户' }}</p>
          <router-link v-if="user.role === 'admin'" class="admin-link" to="/admin">进入平台管理</router-link>
        </div>
      </div>
      <dl>
        <div><dt>用户名</dt><dd>{{ user.username || '—' }}</dd></div>
        <div><dt>手机号</dt><dd>{{ user.phone || '未设置' }}</dd></div>
        <div><dt>注册时间</dt><dd>{{ shortDateTime(user.create_time).slice(0, 10) }}</dd></div>
      </dl>
    </section>

    <section class="account-card">
      <h2>修改资料</h2>
      <el-form :model="infoForm" label-position="top">
        <el-form-item label="手机号">
          <el-input v-model="infoForm.phone" placeholder="11 位手机号" maxlength="11" />
        </el-form-item>
        <el-form-item>
          <CoverUpload v-model="infoForm.avatar_url" variant="avatar" label="头像" />
        </el-form-item>
        <el-button type="primary" :loading="saving" @click="saveInfo">保存</el-button>
      </el-form>
    </section>

    <section class="account-card">
      <h2>观演人证件</h2>
      <p class="card-lead">实名制活动购票时勾选已绑定证件，不必每次手填。同一证件同一场只能买一张。</p>
      <p v-if="!attendees.length" class="card-lead">还没有绑定证件，填下面的姓名和身份证号即可。</p>
      <ul v-else class="attendee-list">
        <li v-for="item in attendees" :key="item.id">
          <div>
            <strong>{{ item.name }}</strong>
            <span>居民身份证 {{ item.id_number_masked }}</span>
          </div>
          <button type="button" @click="removeAttendee(item)">删除</button>
        </li>
      </ul>
      <el-form :model="attendeeForm" label-position="top" class="attendee-form">
        <el-form-item label="姓名">
          <el-input v-model="attendeeForm.name" maxlength="64" />
        </el-form-item>
        <el-form-item label="身份证号">
          <el-input v-model="attendeeForm.id_number" maxlength="18" />
        </el-form-item>
        <el-button type="primary" :loading="savingAttendee" @click="addAttendee">绑定证件</el-button>
      </el-form>
    </section>

    <section class="account-card">
      <h2>修改密码</h2>
      <el-form ref="pwdRef" :model="pwdForm" :rules="pwdRules" label-position="top">
        <el-form-item label="原密码" prop="old_password">
          <el-input v-model="pwdForm.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码" prop="new_password">
          <el-input v-model="pwdForm.new_password" type="password" show-password />
        </el-form-item>
        <el-button type="danger" :loading="changing" @click="changePwd">修改密码</el-button>
      </el-form>
    </section>
  </div>
</template>

<script setup>
import { inject, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../api/index.js'
import CoverUpload from '../components/CoverUpload.vue'
import { shortDateTime } from '../utils/display.js'

const refreshUser = inject('refreshUser', () => {})
const user = ref({})
const saving = ref(false)
const changing = ref(false)
const infoForm = reactive({ phone: '', avatar_url: '' })
const attendees = ref([])
const savingAttendee = ref(false)
const attendeeForm = reactive({ name: '', id_number: '' })
const pwdForm = reactive({ old_password: '', new_password: '' })
const pwdRef = ref(null)
const pwdRules = {
  old_password: [{ required: true, message: '请输入原密码' }],
  new_password: [{ required: true, min: 6, message: '密码至少 6 位' }],
}

onMounted(async () => {
  await loadUser()
  await loadAttendees()
})

async function loadUser() {
  try {
    const res = await api.getUserInfo()
    user.value = res.data || {}
    infoForm.phone = res.data?.phone || ''
    infoForm.avatar_url = res.data?.avatar_url || ''
  } catch {
    ElMessage.error('无法加载账户信息')
  }
}

async function saveInfo() {
  saving.value = true
  try {
    await api.updateUserInfo(infoForm)
    ElMessage.success('保存成功')
    refreshUser()
    await loadUser()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '保存失败')
  } finally {
    saving.value = false
  }
}

async function loadAttendees() {
  try {
    const res = await api.getUserAttendees()
    attendees.value = res.data || []
  } catch {
    attendees.value = []
  }
}

async function addAttendee() {
  savingAttendee.value = true
  try {
    await api.createUserAttendee({
      name: attendeeForm.name,
      id_type: 'id_card',
      id_number: attendeeForm.id_number,
    })
    attendeeForm.name = ''
    attendeeForm.id_number = ''
    ElMessage.success('已绑定')
    await loadAttendees()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '绑定失败')
  } finally {
    savingAttendee.value = false
  }
}

async function removeAttendee(item) {
  try {
    await api.deleteUserAttendee(item.id)
    attendees.value = attendees.value.filter(row => row.id !== item.id)
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '删除失败')
  }
}

async function changePwd() {
  const valid = await pwdRef.value?.validate().catch(() => false)
  if (!valid) return
  changing.value = true
  try {
    await api.changePassword(pwdForm.old_password, pwdForm.new_password)
    ElMessage.success('密码修改成功')
    pwdForm.old_password = ''
    pwdForm.new_password = ''
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '密码修改失败')
  } finally {
    changing.value = false
  }
}
</script>

<style scoped>
.account-page { max-width: 720px; min-height: 70vh; margin: 0 auto; padding: 55px 30px; }
header { margin-bottom: 32px; }
header p { color: var(--red); font-size: 11px; letter-spacing: .2em; }
header h1 { margin: 6px 0; font-family: var(--font-display); font-size: 44px; }
header span { color: var(--muted); }
.account-card {
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: rgba(255,255,255,.28);
}
.account-card h2 { margin: 0 0 18px; font: 700 20px var(--font-display); }
.card-lead { margin: -8px 0 16px; color: var(--muted); font-size: 13px; line-height: 1.6; }
.attendee-list { list-style: none; margin: 0 0 16px; padding: 0; display: grid; gap: 10px; }
.attendee-list li { display: flex; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--line); }
.attendee-list span { display: block; color: var(--muted); font-size: 12px; margin-top: 4px; }
.attendee-list button { border: 0; background: transparent; color: var(--red); cursor: pointer; }
.attendee-form { display: grid; gap: 0; }
.profile-head { display: flex; align-items: center; gap: 16px; margin-bottom: 22px; }
.profile-head strong { display: block; font-size: 20px; }
.profile-head p { margin: 6px 0 0; color: var(--muted); font-size: 13px; }
.admin-link { display: inline-block; margin-top: 8px; color: var(--red); font-size: 13px; text-decoration: none; }
dl { display: grid; gap: 12px; margin: 0; }
dl div { display: flex; justify-content: space-between; gap: 16px; }
dt { color: var(--muted); }
dd { margin: 0; }
</style>
