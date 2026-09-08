<template>
  <div class="auth-container">
    <aside class="auth-brand">
      <div class="auth-brand-mark"><i>G</i> Gofun</div>
      <div>
        <h1 class="auth-headline">欢迎回来，<br />赴热爱之场。</h1>
        <p class="auth-tagline">
          见想见的人。发现、购票、限时开售，一处完成。
        </p>
      </div>
      <div class="auth-foot">© Gofun · 多主办方活动票务平台</div>
    </aside>

    <section class="auth-panel">
      <div class="auth-box">
        <h2>欢迎回来</h2>
        <p class="auth-sub">登录账号，继续发现值得奔赴的现场</p>
        <el-form ref="formRef" :model="form" :rules="rules" @submit.prevent="handleLogin">
          <el-form-item prop="username">
            <el-input v-model="form.username" placeholder="用户名" size="large">
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item prop="password">
            <el-input v-model="form.password" type="password" placeholder="密码" size="large" show-password @keyup.enter="handleLogin">
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" size="large" class="auth-button" native-type="submit" :loading="loading">
              登录
            </el-button>
          </el-form-item>
        </el-form>
        <div v-if="demoAccounts.length" class="demo-accounts" aria-label="演示账号">
          <p>演示账号，点一下即可填入</p>
          <div>
            <button
              v-for="item in demoAccounts"
              :key="item.username"
              type="button"
              :disabled="loading"
              @click="useDemoAccount(item)"
            >
              <b>{{ item.label }}</b>
              <span>{{ item.username }} / {{ item.password }}</span>
            </button>
          </div>
        </div>
        <div class="auth-switch">
          <span>还没有账号？</span>
          <el-link type="primary" @click="$router.push('/register')">立即注册</el-link>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import api from '../api/index.js'
import { useFavorites } from '../stores/favorites.js'
import { applySession, safeRedirectPath } from '../stores/session.js'

const router = useRouter()
const { reload: reloadFavorites } = useFavorites()
const route = useRoute()
const formRef = ref(null)
const loading = ref(false)
const form = reactive({ username: '', password: '' })
const preferredHome = ref('')
const demoAccounts = import.meta.env.DEV
  ? [
    { label: '管理员', username: 'admin', password: 'admin123', home: '/admin' },
    { label: '主办方', username: 'organizer', password: 'organizer123', home: '/organizer' },
    { label: '购票用户', username: 'user', password: 'user123', home: '/' },
  ]
  : []
const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

function useDemoAccount(item) {
  form.username = item.username
  form.password = item.password
  preferredHome.value = item.home
}

async function handleLogin() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    const res = await api.login(form.username, form.password)
    applySession({
      access_token: res.data.access_token,
      refresh_token: res.data.refresh_token,
      username: res.data.username || form.username,
      role: res.data.role || '',
    })
    reloadFavorites()
    ElMessage.success('登录成功')
    if (res.data.role === 'admin') {
      router.push('/admin')
    } else {
      const redirect = safeRedirectPath(route.query.redirect, '')
      if (redirect) router.push(redirect)
      else if (preferredHome.value) router.push(preferredHome.value)
      else router.push('/')
    }
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>
