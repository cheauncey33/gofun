<template>
  <div>
    <div class="page-header"><h3>用户管理</h3></div>
    <el-table :data="users" v-loading="loading" stripe>
      <el-table-column prop="id" label="ID" width="120"><template #default="{row}"><span style="font-family:monospace;font-size:12px">{{ row.id }}</span></template></el-table-column>
      <el-table-column prop="username" label="用户名" />
      <el-table-column label="余额" width="110"><template #default="{row}"><span style="color:#E17055;font-weight:700">¥{{ row.balance?.toFixed(2) }}</span></template></el-table-column>
      <el-table-column label="角色" width="90">
        <template #default="{row}">
          <el-tag v-if="row.role==='admin'" type="danger" round size="small">管理员</el-tag>
          <el-tag v-else round size="small">用户</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="160">
        <template #default="{row}">
          <el-button size="small" :type="row.role==='admin'?'warning':'primary'" @click="toggleRole(row)">
            {{ row.role==='admin'?'降级为普通用户':'升级为管理员' }}
          </el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../../api/index.js'

const users = ref([])
const loading = ref(false)

onMounted(fetch)

async function fetch() {
  loading.value = true
  try { const res = await api.adminGetUsers({ page: 1, page_size: 50 }); users.value = res.data?.list || [] } finally { loading.value = false }
}

async function toggleRole(row) {
  const newRole = row.role === 'admin' ? 'user' : 'admin'
  const selfDemotion = row.username === localStorage.getItem('username') && newRole === 'user'
  if (selfDemotion) {
    ElMessage.warning('不能降级自己的管理员权限')
    return
  }
  try { await api.adminUpdateUserRole(row.id, newRole); ElMessage.success('角色已更新'); fetch() }
  catch (e) { ElMessage.error(e.response?.data?.msg) }
}
</script>
