<template>
  <div class="login-container">
    <div class="login-box">
      <h2 class="login-title">WHU Snack 登录</h2>
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        class="login-form"
        @submit.prevent="handleLogin"
      >
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            placeholder="请输入用户名"
            prefix-icon="User"
            size="large"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="请输入密码"
            prefix-icon="Lock"
            size="large"
            show-password
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            size="large"
            style="width: 100%"
            :loading="loading"
            @click="handleLogin"
          >
            登 录
          </el-button>
        </el-form-item>
      </el-form>
      <div class="login-footer">
        <span>还没有账号？</span>
        <el-link type="primary" @click="toRegister">立即注册</el-link>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { User, Lock } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const emit = defineEmits(['login-success', 'to-register'])

const formRef = ref(null)
const loading = ref(false)

const form = reactive({
  username: '',
  password: '',
})

const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function handleLogin() {
  console.log('clicked')
  const valid = await formRef.value.validate().catch(()=>false)
  if(!valid) return
  loading.value=true
  try{
    console.log('try-in')
    const res =await axios.post('http://127.0.0.1:8080/api/v1/login',{
      username:form.username,
      password:form.password,
    })
    localStorage.setItem('token',res.data.token)
    localStorage.setItem('username',form.username)
    ElMessage.success('登录 成功')
    emit('login success')
  }catch(e){
    ElMessage.error(e.response?.data?.msg||'登陆失败')
  }finally{
    loading.value=false
  }
}

function toRegister() {
  console.log('toRegister clicked')
  emit('to-register')
}
</script>

<style scoped>
.login-container {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}

.login-box {
  width: 380px;
  padding: 40px 32px;
  background: #fff;
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
}

.login-title {
  text-align: center;
  margin-bottom: 32px;
  font-size: 24px;
  color: #333;
}

.login-form {
  margin-bottom: 16px;
}

.login-footer {
  text-align: center;
  color: #666;
  font-size: 14px;
}
</style>
