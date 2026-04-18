<template>
  <div id="app">
    <!-- 登录页 -->
    <Login
      v-if="currentView === 'login'"
      @login-success="handleLoginSuccess"
      @to-register="currentView = 'register'"
    />

    <!-- 注册页 -->
    <Register
      v-else-if="currentView === 'register'"
      @register-success="handleLoginSuccess"
      @to-login="currentView = 'login'"
    />

    <!-- 商品列表页 -->
    <Product
      v-else-if="currentView === 'product'"
      @logout="handleLogout"
    />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
import Login from './views/Login.vue'
import Register from './views/Register.vue'
import Product from './views/Product.vue'

// 设置 axios 全局【请求】拦截器
axios.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers['Authorization'] = `${token}`
  }
  return config
}, error => {
  return Promise.reject(error)
})
//【响应】拦截器
axios.interceptors.response.use(
  (response)=>{
    return response
  },
  (error)=>{
    if(error.response&&error.response.status===401){
      localStorage.removeItem('token')
      localStorage.removeItem('username')
      currentView.value='login'
    }
    return Promise.reject(error)
  }
)

const currentView = ref('login')

function handleLoginSuccess() {
  currentView.value = 'product'
}

function handleLogout() {
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  currentView.value = 'login'
}

// 检查是否在刷新页面前已登录
onMounted(() => {
  const token = localStorage.getItem('token')
  if (token) {
    currentView.value = 'product'
  }
})
</script>

<style>
/* 可以在这里添加一些全局的基础样式 */
body {
  margin: 0;
  font-family: Arial, sans-serif;
}
</style>
