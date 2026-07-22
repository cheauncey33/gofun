<template>
  <div class="auth-container">
    <aside class="auth-brand">
      <div class="auth-brand-mark"><i>赴</i> 赴场</div>
      <div>
        <h1 class="auth-headline">欢迎回来，<br />继续奔赴<em>热爱</em>。</h1>
        <p class="auth-tagline">
          多主办方活动票务平台 —— 发现、购票、限时开售，一处完成。
          你想见的人和现场，正在前方等你。
        </p>
      </div>
      <div class="auth-foot">© 赴场 · 多主办方活动票务平台</div>
    </aside>

    <section class="auth-panel">
      <div class="auth-box">
        <h2>欢迎回来</h2>
        <p class="auth-sub">登录账号，继续发现值得奔赴的现场</p>
        <el-form ref="formRef" :model="form" :rules="rules" @submit.prevent>
          <el-form-item prop="username">
            <el-input v-model="form.username" placeholder="用户名" size="large">
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item prop="password">
            <el-input v-model="form.password" type="password" placeholder="密码" size="large" show-password>
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" size="large" class="auth-button" :loading="loading" @click="handleLogin">
              登录
            </el-button>
          </el-form-item>
        </el-form>
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

const router = useRouter()
const route = useRoute()
const formRef = ref(null)
const loading = ref(false)
const form = reactive({ username: '', password: '' })
const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function handleLogin() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    const res = await api.login(form.username, form.password)
    localStorage.setItem('access_token', res.data.access_token)
    localStorage.setItem('refresh_token', res.data.refresh_token)
    localStorage.setItem('token', res.data.access_token)
    localStorage.setItem('username', form.username)
    ElMessage.success('登录成功')
    router.push(typeof route.query.redirect === 'string' ? route.query.redirect : '/')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>
