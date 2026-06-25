<template>
  <div class="auth-container">
    <aside class="auth-brand">
      <div class="auth-brand-mark"><i>S</i> WHU Snack</div>
      <div>
        <h1 class="auth-headline">注册一下，<br />开吃<em>新生活</em>。</h1>
        <p class="auth-tagline">
          三十秒创建账号，立刻解锁全场零食与限时秒杀。
          武大同学的深夜食堂，欢迎你。
        </p>
      </div>
      <div class="auth-foot">© WHU Snack GO · 校园零食订购系统</div>
    </aside>

    <section class="auth-panel">
      <div class="auth-box">
        <h2>创建账号</h2>
        <p class="auth-sub">填写下面的信息即可开始</p>
        <el-form ref="formRef" :model="form" :rules="rules" @submit.prevent>
          <el-form-item prop="username">
            <el-input v-model="form.username" placeholder="用户名，3-20 位" size="large">
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item prop="password">
            <el-input v-model="form.password" type="password" placeholder="密码，至少 6 位" size="large" show-password>
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item prop="confirmPassword">
            <el-input v-model="form.confirmPassword" type="password" placeholder="确认密码" size="large" show-password>
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" size="large" class="auth-button" :loading="loading" @click="handleRegister">
              注册
            </el-button>
          </el-form-item>
        </el-form>
        <div class="auth-switch">
          <span>已有账号？</span>
          <el-link type="primary" @click="$router.push('/login')">立即登录</el-link>
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
const form = reactive({ username: '', password: '', confirmPassword: '' })

const validateConfirm = (rule, value, callback) => {
  callback(value !== form.password ? new Error('两次密码不一致') : undefined)
}

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名为 3-20 个字符', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, message: '密码至少 6 位', trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: '请确认密码', trigger: 'blur' },
    { validator: validateConfirm, trigger: 'blur' },
  ],
}

async function handleRegister() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    await api.register(form.username, form.password, form.dorm_id || 1)
    ElMessage.success('注册成功，请登录')
    router.push('/login')
  } catch (e) {
    ElMessage.error(e.response?.data?.msg || '注册失败')
  } finally {
    loading.value = false
  }
}
</script>
