<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import api from '../api'

const router = useRouter()
const loading = ref(true)
const submitting = ref(false)
const organizers = ref([])
const total = ref(0)
const form = reactive({
  name: '',
  slug: '',
  owner_username: '',
  contact_name: '',
  contact_phone: '',
  description: '',
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    const res = await api.adminGetOrganizers({ page: 1, page_size: 50 })
    organizers.value = res.data?.list || []
    total.value = res.data?.total || organizers.value.length
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '无法加载主办方列表')
  } finally {
    loading.value = false
  }
}

function slugFromName() {
  if (form.slug.trim()) return
  form.slug = form.name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64)
}

async function createOrganizer() {
  if (!form.name.trim() || !form.slug.trim() || !form.owner_username.trim()) {
    ElMessage.warning('请填写主办方名称、标识和负责人用户名')
    return
  }
  submitting.value = true
  try {
    await api.adminCreateOrganizer({
      name: form.name.trim(),
      slug: form.slug.trim(),
      owner_username: form.owner_username.trim(),
      contact_name: form.contact_name.trim(),
      contact_phone: form.contact_phone.trim(),
      description: form.description.trim(),
    })
    ElMessage.success('主办方已创建')
    Object.assign(form, {
      name: '', slug: '', owner_username: '', contact_name: '', contact_phone: '', description: '',
    })
    await load()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '创建失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="admin-shell">
    <header class="admin-topbar">
      <button class="brand" type="button" @click="router.push('/')">Gofun</button>
      <span>平台管理</span>
      <button class="back" type="button" @click="router.push('/')">返回购票站</button>
    </header>

    <main>
      <section class="heading">
        <div>
          <h1>主办方</h1>
          <p>创建主办方并指定已注册用户为负责人，对方即可进入工作台发售。</p>
        </div>
        <span>共 {{ total }} 个</span>
      </section>

      <section class="create-card">
        <h2>新建主办方</h2>
        <el-form label-position="top" @submit.prevent="createOrganizer">
          <div class="grid">
            <el-form-item label="主办方名称">
              <el-input v-model="form.name" maxlength="80" @blur="slugFromName" />
            </el-form-item>
            <el-form-item label="标识 slug">
              <el-input v-model="form.slug" maxlength="64" placeholder="例如 wuhan-livehouse" />
            </el-form-item>
            <el-form-item label="负责人用户名">
              <el-input v-model="form.owner_username" placeholder="已注册的购票账号" />
            </el-form-item>
            <el-form-item label="联系人">
              <el-input v-model="form.contact_name" />
            </el-form-item>
            <el-form-item label="联系电话">
              <el-input v-model="form.contact_phone" maxlength="20" />
            </el-form-item>
          </div>
          <el-form-item label="简介">
            <el-input v-model="form.description" type="textarea" :rows="2" maxlength="400" />
          </el-form-item>
          <el-button type="primary" :loading="submitting" @click="createOrganizer">创建主办方</el-button>
        </el-form>
      </section>

      <section class="list-card">
        <div v-if="loading" class="state">正在加载主办方…</div>
        <el-table v-else :data="organizers" empty-text="还没有主办方">
          <el-table-column prop="name" label="名称" min-width="160" />
          <el-table-column prop="slug" label="标识" min-width="140" />
          <el-table-column prop="contact_name" label="联系人" min-width="120" />
          <el-table-column prop="status" label="状态" width="100" />
        </el-table>
      </section>
    </main>
  </div>
</template>

<style scoped>
.admin-shell { min-height: 100vh; background: #f8f4ec; }
.admin-topbar {
  height: 60px;
  padding: 0 28px;
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 16px;
  background: rgba(248,244,236,.96);
}
.brand, .back { border: 0; background: transparent; cursor: pointer; font: inherit; }
.brand { color: var(--ink); font: 800 26px var(--font-display); letter-spacing: .08em; }
.admin-topbar span { color: var(--muted); }
.back { margin-left: auto; font-size: 13px; }
main { max-width: 980px; margin: 0 auto; padding: 32px 24px 70px; }
.heading { display: flex; justify-content: space-between; align-items: end; gap: 16px; }
.heading h1 { margin: 0; font: 760 36px var(--font-display); }
.heading p, .heading span { color: var(--muted); font-size: 13px; }
.create-card, .list-card {
  margin-top: 24px;
  padding: 22px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: rgba(255,255,255,.28);
}
.create-card h2 { margin: 0 0 16px; font: 720 20px var(--font-display); }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.state { padding: 40px; text-align: center; color: var(--muted); }
@media (max-width: 700px) {
  .grid { grid-template-columns: 1fr; }
  main { padding: 24px 16px 60px; }
}
</style>
