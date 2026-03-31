<template>
  <div class="login-container">
    <div class="login-box">
      <h2 class="login-title">WHU Snack 注册</h2>
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        class="login-form"
        @submit.prevent="handleRegister"
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
        <el-form-item prop="confirmPassword">
          <el-input
            v-model="form.confirmPassword"
            type="password"
            placeholder="请确认密码"
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
            @click="handleRegister"
          >
            注 册
          </el-button>
        </el-form-item>
      </el-form>
      <div class="login-footer">
        <span>已有账号？</span>
        <el-link type="primary" @click="$emit('to-login')">立即登录</el-link>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { User, Lock } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const emit = defineEmits(['register-success', 'to-login'])

const formRef = ref(null)
const loading = ref(false)

const form = reactive({
  username: '',
  password: '',
  confirmPassword: '',
})

const validateConfirmPassword = (rule, value, callback) => {
  if (value !== form.password) {
    callback(new Error('两次输入的密码不一致'))
  } else {
    callback()
  }
}

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名长度在 3 到 20 个字符', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, message: '密码长度至少 6 个字符', trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: '请确认密码', trigger: 'blur' },
    { validator: validateConfirmPassword, trigger: 'blur' },
  ],
}

async function handleRegister() {
  //这行语句的作用/操作就是检验引用formRef（引用名）所引用的form（变量名）内的数据是否满足rules（也是规则名）这三个都是自定义的名字，不是关键字。
  //具体看行6，7，8
  
  const valid =await formRef.value.validate().catch(()=>false)
  //说明当前输入不满足规则则返回
  if(!valid) return
  loading.value =true
  try {
    console.log('try-in')
    console.log('即将发请求，参数是:', form.username, form.password)
    const res=await axios.post('http://127.0.0.1:8080/api/v1/register',{
      username:form.username,
      password:form.password,
      dorm_id:1,//默认宿舍1 
    })
    console.log('请求成功了', res)
    ElMessage.success('register success!')
    emit('to-login')//跳回登录页
  }catch(e){
    console.log('完整错误对象:', e)  // 改成这个
    console.log('错误消息:', e.message)
    ElMessage.error(e.response?.data?.msg || '注册失败')
  }finally{
    loading.value=false
  }
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
