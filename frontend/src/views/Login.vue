<template>
  <div class="auth-container">
    <aside class="auth-brand">
      <div class="auth-brand-mark"><i>S</i> WHU Snack</div>
      <div>
        <h1 class="auth-headline">下课了，<br />来点<em>好吃的</em>。</h1>
        <p class="auth-tagline">
          武大校园零食铺 —— 下单、秒杀、到货提醒，一站搞定。
          热乎的小零嘴，正在等你翻牌。
        </p>
      </div>
      <div class="auth-foot">© WHU Snack GO · 校园零食订购系统</div>
    </aside>

    <section class="auth-panel">
      <div class="auth-box">
        <h2>欢迎回来</h2>
        <p class="auth-sub">登录你的账号，继续逛吃逛吃</p>
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
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import api from '../api/index.js'

const router = useRouter()
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
    router.push('/')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>
