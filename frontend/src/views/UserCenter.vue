<template>
  <div class="account-page">
    <header>
      <div class="account-identity">
        <el-avatar :size="44" :src="user.avatar_url">
          {{ (user.username || '用').charAt(0).toUpperCase() }}
        </el-avatar>
        <div>
          <h1>个人中心</h1>
          <p>
            {{ user.username || '—' }}
            · {{ user.role === 'admin' ? '平台管理员' : '购票用户' }}
          </p>
        </div>
      </div>
      <nav class="account-tabs" aria-label="个人中心">
        <button
          v-for="item in tabs"
          :key="item.id"
          type="button"
          :class="{ active: tab === item.id }"
          @click="selectTab(item.id)"
        >{{ item.label }}</button>
      </nav>
    </header>

    <section class="account-panel">
      <template v-if="tab === 'favorites'">
        <p class="card-lead">收藏限时开售和普通购票，方便回来继续买。</p>
        <div class="fav-tabs">
          <button type="button" :class="{ active: favFilter === 'all' }" @click="favFilter = 'all'">全部</button>
          <button type="button" :class="{ active: favFilter === 'rush_sale' }" @click="favFilter = 'rush_sale'">限时开售</button>
          <button type="button" :class="{ active: favFilter === 'event' }" @click="favFilter = 'event'">普通购票</button>
        </div>
        <div v-if="favLoading" class="fav-empty">正在加载收藏…</div>
        <div v-else-if="!visibleFavorites.length" class="fav-empty">还没有收藏</div>
        <ul v-else class="fav-list">
          <li v-for="item in visibleFavorites" :key="item.key || item.id" :class="item.status">
            <router-link :to="item.href || '/'">
              <img v-if="item.cover_url" :src="item.cover_url" :alt="item.title" />
              <div>
                <small>
                  {{ item.kind_label }}
                  <em v-if="item.status_label" :class="item.status">{{ item.status_label }}</em>
                </small>
                <strong>{{ item.title }}</strong>
                <span>{{ item.subtitle }}</span>
              </div>
            </router-link>
            <button type="button" @click="removeFavorite(item)">取消</button>
          </li>
        </ul>
      </template>

      <template v-else-if="tab === 'profile'">
        <p class="card-lead">手机号和头像会用在订单联系与评论展示。</p>
        <dl class="profile-meta">
          <div><dt>用户名</dt><dd>{{ user.username || '—' }}</dd></div>
          <div><dt>注册时间</dt><dd>{{ shortDateTime(user.create_time).slice(0, 10) }}</dd></div>
        </dl>
        <el-form class="form-narrow" :model="infoForm" label-position="top">
          <el-form-item label="手机号">
            <el-input v-model="infoForm.phone" placeholder="11 位手机号" maxlength="11" />
          </el-form-item>
          <el-form-item>
            <CoverUpload v-model="infoForm.avatar_url" variant="avatar" label="头像" />
          </el-form-item>
          <el-button type="primary" :loading="saving" @click="saveInfo">保存</el-button>
        </el-form>
        <router-link v-if="user.role === 'admin'" class="admin-link" to="/admin">进入平台管理</router-link>
        <router-link v-else-if="hasOrganizerWorkspace" class="admin-link" to="/organizer">主办方工作台</router-link>
      </template>

      <template v-else-if="tab === 'attendees'">
        <p class="card-lead">实名制场次购票时，从这里选用已绑定的证件。</p>
        <ul v-if="attendees.length" class="attendee-list">
          <li v-for="item in attendees" :key="item.id">
            <div>
              <strong>{{ item.name }}</strong>
              <span>居民身份证 {{ item.id_number_masked }}</span>
            </div>
            <button type="button" @click="removeAttendee(item)">删除</button>
          </li>
        </ul>
        <el-form :model="attendeeForm" label-position="top" class="attendee-form form-narrow">
          <el-form-item label="姓名">
            <el-input v-model="attendeeForm.name" maxlength="64" />
          </el-form-item>
          <el-form-item label="身份证号">
            <el-input v-model="attendeeForm.id_number" maxlength="18" />
          </el-form-item>
          <el-button type="primary" :loading="savingAttendee" @click="addAttendee">绑定证件</el-button>
        </el-form>
      </template>

      <template v-else>
        <p class="card-lead">修改后需用新密码重新登录部分已打开的页面。</p>
        <el-form ref="pwdRef" class="form-narrow" :model="pwdForm" :rules="pwdRules" label-position="top">
          <el-form-item label="原密码" prop="old_password">
            <el-input v-model="pwdForm.old_password" type="password" show-password />
          </el-form-item>
          <el-form-item label="新密码" prop="new_password">
            <el-input v-model="pwdForm.new_password" type="password" show-password />
          </el-form-item>
          <el-button type="danger" :loading="changing" @click="changePwd">修改密码</el-button>
        </el-form>
      </template>
    </section>
  </div>
</template>

<script setup>
import { computed, inject, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useFavorites } from '../stores/favorites.js'
import { hasApprovedOrganizerWorkspace, useSession } from '../stores/session.js'
import { ElMessage } from 'element-plus'
import api from '../api/index.js'
import CoverUpload from '../components/CoverUpload.vue'
import { shortDateTime } from '../utils/display.js'

const route = useRoute()
const router = useRouter()
const refreshUser = inject('refreshUser', () => {})
const { hasOrganizerWorkspace, setOrganizerWorkspace } = useSession()
const user = ref({})
const saving = ref(false)
const changing = ref(false)
const infoForm = reactive({ phone: '', avatar_url: '' })
const attendees = ref([])
const savingAttendee = ref(false)
const attendeeForm = reactive({ name: '', id_number: '' })
const pwdForm = reactive({ old_password: '', new_password: '' })
const pwdRef = ref(null)
const favFilter = ref('all')
const favLoading = ref(true)
const { items: favorites, reload: reloadFavorites } = useFavorites()
const favoriteStatusRank = {
  live: 70,
  ending: 60,
  scheduled: 50,
  on_sale: 40,
  sale_closed: 25,
  sold_out: 20,
  ended: 10,
  unavailable: 0,
}

function mergeFavoriteGroup(items) {
  if (items.length === 1) {
    return { ...items[0], key: String(items[0].id), ids: [items[0].id] }
  }
  const rushItems = items.filter(item => item.target_type === 'rush_sale')
  const eventItem = items.find(item => item.target_type === 'event')
  const hottest = items.slice().sort((a, b) =>
    (favoriteStatusRank[b.status] || 0) - (favoriteStatusRank[a.status] || 0)
  )[0]
  const rushNames = rushItems.map(item => item.title).filter(Boolean)
  const kinds = [...new Set(items.map(item => item.kind_label).filter(Boolean))]
  return {
    ...hottest,
    key: items.map(item => item.id).join('-'),
    ids: items.map(item => item.id),
    title: eventItem?.title || rushItems[0]?.subtitle || hottest.title,
    subtitle: rushNames.length
      ? (eventItem ? `含限时开售 · ${rushNames.join(' · ')}` : hottest.subtitle)
      : (eventItem?.subtitle || hottest.subtitle),
    cover_url: eventItem?.cover_url || rushItems[0]?.cover_url || hottest.cover_url,
    href: rushItems[0]?.href || eventItem?.href || hottest.href,
    kind_label: kinds.join(' · ') || hottest.kind_label,
    status: hottest.status,
    status_label: hottest.status_label,
  }
}

const visibleFavorites = computed(() => {
  const rows = favFilter.value === 'all'
    ? favorites.value
    : favorites.value.filter(item => item.target_type === favFilter.value)
  if (favFilter.value !== 'all') {
    return rows.map(item => ({ ...item, key: String(item.id), ids: [item.id] }))
  }
  const groups = []
  const byEvent = new Map()
  for (const item of rows) {
    const eventId = Number(item.event_id || 0)
    if (!eventId) {
      groups.push(mergeFavoriteGroup([item]))
      continue
    }
    if (!byEvent.has(eventId)) {
      const bucket = []
      byEvent.set(eventId, bucket)
      groups.push(bucket)
    }
    byEvent.get(eventId).push(item)
  }
  return groups.map(group => Array.isArray(group) ? mergeFavoriteGroup(group) : group)
})
const pwdRules = {
  old_password: [{ required: true, message: '请输入原密码' }],
  new_password: [{ required: true, min: 6, message: '密码至少 6 位' }],
}

const tabIds = ['favorites', 'profile', 'attendees', 'password']
const tabs = computed(() => [
  { id: 'favorites', label: favorites.value.length ? `收藏 ${favorites.value.length}` : '收藏' },
  { id: 'profile', label: '资料' },
  { id: 'attendees', label: attendees.value.length ? `观演人 ${attendees.value.length}` : '观演人' },
  { id: 'password', label: '密码' },
])
const tab = computed(() => {
  const queryTab = String(route.query.tab || '')
  if (tabIds.includes(queryTab)) return queryTab
  const hash = String(route.hash || '').replace(/^#/, '')
  if (tabIds.includes(hash)) return hash
  return 'favorites'
})

function selectTab(next) {
  const query = { ...route.query }
  if (next === 'favorites') delete query.tab
  else query.tab = next
  router.replace({ query, hash: '' })
}

onMounted(async () => {
  await loadUser()
  await Promise.all([loadAttendees(), loadOrganizerAccess()])
  favLoading.value = true
  await reloadFavorites()
  favLoading.value = false
})

async function removeFavorite(item) {
  const ids = item.ids?.length ? item.ids : [item.id]
  try {
    await Promise.all(ids.map(id => api.removeFavorite(id)))
    await reloadFavorites()
  } catch (error) {
    ElMessage.error(error.response?.data?.msg || '取消收藏失败')
  }
}

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

async function loadOrganizerAccess() {
  if (user.value.role === 'admin') {
    setOrganizerWorkspace(false)
    return
  }
  try {
    const res = await api.organizerGetMine()
    setOrganizerWorkspace(hasApprovedOrganizerWorkspace(res.data))
  } catch {
    setOrganizerWorkspace(false)
  }
}

async function saveInfo() {
  saving.value = true
  try {
    await api.updateUserInfo(infoForm)
    ElMessage.success('保存成功')
    try {
      await refreshUser()
    } catch {}
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
.account-page { min-height: 70vh; padding: 20px 3.2vw 48px; }
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.account-identity { display: flex; align-items: center; gap: 12px; min-width: 0; }
.account-identity h1 { margin: 0; font-family: var(--font-display); font-size: 26px; }
.account-identity p { margin: 4px 0 0; color: var(--muted); font-size: 13px; }
.account-tabs { display: flex; flex-wrap: wrap; gap: 6px; }
.account-tabs button {
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--ink);
  font: 650 13px inherit;
  cursor: pointer;
}
.account-tabs button.active { border-color: var(--red); background: var(--red); color: #fff; }
.account-panel {
  min-height: 52vh;
  padding: 24px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: rgba(255,255,255,.28);
}
.card-lead { margin: 0 0 16px; color: var(--muted); font-size: 13px; line-height: 1.6; }
.form-narrow { max-width: 420px; }
.admin-link { display: inline-block; margin-top: 18px; color: var(--red); font-size: 13px; text-decoration: none; }
.profile-meta { display: grid; gap: 10px; margin: 0 0 18px; max-width: 420px; }
.profile-meta div { display: flex; justify-content: space-between; gap: 16px; }
.profile-meta dt { color: var(--muted); }
.profile-meta dd { margin: 0; }
.attendee-list { list-style: none; margin: 0 0 16px; padding: 0; display: grid; gap: 10px; }
.attendee-list li { display: flex; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--line); }
.attendee-list span { display: block; color: var(--muted); font-size: 12px; margin-top: 4px; }
.attendee-list button { border: 0; background: transparent; color: var(--red); cursor: pointer; }
.fav-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.fav-tabs button {
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-pill);
  background: transparent;
  cursor: pointer;
  font: 650 13px inherit;
}
.fav-tabs button.active { border-color: var(--red); background: var(--red); color: #fff; }
.fav-empty { padding: 28px 0; color: var(--muted); font-size: 13px; }
.fav-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
.fav-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid var(--line);
}
.fav-list a {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 12px;
  align-items: center;
  min-width: 0;
  color: inherit;
  text-decoration: none;
}
.fav-list img { width: 72px; height: 48px; object-fit: cover; border-radius: 8px; background: var(--paper-deep); }
.fav-list small { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; color: var(--muted); font-size: 11px; }
.fav-list em { font-style: normal; font-weight: 700; }
.fav-list em.live, .fav-list em.ending, .fav-list em.on_sale { color: var(--red); }
.fav-list em.scheduled { color: var(--blue); }
.fav-list em.ended, .fav-list em.sold_out, .fav-list em.sale_closed, .fav-list em.unavailable { color: var(--muted); }
.fav-list li.ended, .fav-list li.sold_out, .fav-list li.sale_closed, .fav-list li.unavailable { opacity: .72; }
.fav-list strong { display: block; margin: 2px 0; }
.fav-list span { color: var(--muted); font-size: 12px; }
.fav-list button { border: 0; background: transparent; color: var(--red); cursor: pointer; }
@media (max-width: 720px) {
  header { flex-direction: column; align-items: flex-start; }
}
</style>
