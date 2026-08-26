<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Location, Search, User } from '@element-plus/icons-vue'
import api from '../api'
import { useDiscovery } from '../stores/discovery'
import { connectOrderSocket, disconnectOrderSocket } from '../stores/orderSocket'

const route = useRoute()
const router = useRouter()
const token = computed(() => localStorage.getItem('access_token') || localStorage.getItem('token'))
const username = computed(() => localStorage.getItem('username') || '我的')
const role = ref(localStorage.getItem('role') || '')
const isAdmin = computed(() => role.value === 'admin')
const discoverOpen = ref(false)
const searchDraft = ref('')
const { state, setCity, setKeyword, applyMeta } = useDiscovery()

const cityLabel = computed(() => state.city || '全国')

function selectCity(city) {
  setCity(city)
  discoverOpen.value = false
  if (route.name !== 'Home') router.push('/')
}

function submitSearch() {
  setKeyword(searchDraft.value)
  discoverOpen.value = false
  if (route.name !== 'Home') router.push('/')
}

function logout() {
  disconnectOrderSocket()
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
  role.value = ''
  router.push('/')
}

watch(() => state.keyword, (value) => {
  if (searchDraft.value !== value) searchDraft.value = value
}, { immediate: true })

onMounted(async () => {
  if (token.value) {
    connectOrderSocket()
    try {
      const userRes = await api.getUserInfo()
      if (userRes.data?.username) localStorage.setItem('username', userRes.data.username)
      if (userRes.data?.role) {
        localStorage.setItem('role', userRes.data.role)
        role.value = userRes.data.role
      }
    } catch {
      // 登录态失效时拦截器会跳转登录
    }
  }
  try {
    const res = await api.getCatalogMeta()
    applyMeta(res.data || {})
  } catch {
    // meta 失败不影响浏览；Home 仍可按当前筛选请求
  }
})

onBeforeUnmount(() => {
  disconnectOrderSocket()
})
</script>

<template>
  <div class="site-shell">
    <header class="site-header">
      <router-link class="brand" to="/" aria-label="Gofun 首页">Gofun</router-link>
      <button class="discover-toggle" type="button" @click="discoverOpen = true">
        <el-icon><Search /></el-icon>
        <span>{{ cityLabel }}</span>
      </button>
      <el-dropdown class="city-dropdown" trigger="click" @command="selectCity">
        <button class="city-button" type="button">
          <el-icon><Location /></el-icon>
          {{ cityLabel }}
          <span>⌄</span>
        </button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="">全国</el-dropdown-item>
            <el-dropdown-item
              v-for="city in state.cities"
              :key="city"
              :command="city"
              :class="{ 'is-active-city': city === state.city }"
            >
              {{ city }}
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
      <form class="site-search" @submit.prevent="submitSearch">
        <el-icon><Search /></el-icon>
        <input
          v-model="searchDraft"
          aria-label="搜索活动"
          placeholder="搜索演出、场馆、城市"
        />
      </form>
      <nav class="main-nav" aria-label="主导航">
        <router-link class="nav-home" to="/" :class="{ active: route.name === 'Home' }">活动</router-link>
        <router-link to="/rush-sales" :class="{ active: route.name === 'RushSales' }">限时开售</router-link>
      </nav>
      <router-link v-if="!token" class="account-link" to="/login">登录</router-link>
      <el-dropdown v-else trigger="click">
        <button class="account-link account-button" type="button">
          <el-icon><User /></el-icon>{{ username }}
        </button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item @click="router.push('/orders')">我的订单</el-dropdown-item>
            <el-dropdown-item @click="router.push('/account')">个人中心</el-dropdown-item>
            <el-dropdown-item @click="router.push('/organizer')">主办方工作台</el-dropdown-item>
            <el-dropdown-item v-if="isAdmin" @click="router.push('/admin')">平台管理</el-dropdown-item>
            <el-dropdown-item divided @click="logout">退出登录</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </header>

    <el-drawer
      v-model="discoverOpen"
      direction="ttb"
      size="auto"
      append-to-body
      title="搜索活动"
    >
      <form class="discover-sheet" @submit.prevent="submitSearch">
        <el-dropdown trigger="click" @command="selectCity">
          <button class="city-button sheet-city" type="button">
            <el-icon><Location /></el-icon>
            {{ cityLabel }}
            <span>⌄</span>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="">全国</el-dropdown-item>
              <el-dropdown-item
                v-for="city in state.cities"
                :key="city"
                :command="city"
                :class="{ 'is-active-city': city === state.city }"
              >
                {{ city }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <div class="site-search sheet-search">
          <el-icon><Search /></el-icon>
          <input
            v-model="searchDraft"
            aria-label="搜索活动"
            placeholder="搜索演出、场馆、城市"
          />
        </div>
        <button class="sheet-submit" type="submit">查看场次</button>
      </form>
    </el-drawer>

    <main>
      <router-view />
    </main>

    <footer class="site-footer">
      <strong>Gofun</strong>
      <span>多主办方活动票务平台</span>
      <p>赴热爱之场，见想见的人。</p>
    </footer>
  </div>
</template>

<style scoped>
.site-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}
.site-shell > main {
  flex: 1 0 auto;
}
.site-header {
  height: 68px;
  padding: 0 3.2vw;
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 24px;
  position: sticky;
  top: 0;
  z-index: 30;
  background: rgba(247, 243, 235, .94);
  backdrop-filter: blur(12px);
  transition: background .35s ease, border-color .35s ease, color .35s ease;
}
.brand {
  color: var(--red);
  font-family: var(--font-display);
  font-size: 30px;
  font-weight: 800;
  letter-spacing: .08em;
  text-decoration: none;
  white-space: nowrap;
}
.city-button, .account-button {
  border: 0;
  font: inherit;
  color: inherit;
  background: transparent;
  cursor: pointer;
}
.city-button {
  height: 38px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  display: flex;
  align-items: center;
  gap: 7px;
  border-radius: var(--radius-pill);
}
.account-button {
  height: 38px;
  padding: 0 4px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.site-search {
  flex: 1;
  width: auto;
  max-width: 520px;
  min-width: 180px;
  height: 38px;
  border: 1px solid var(--line-strong);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  border-radius: var(--radius-pill);
  color: var(--muted);
}
.site-search input {
  width: 100%;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--ink);
}
.main-nav { display: flex; gap: 31px; margin-left: auto; }
.main-nav a, .account-link {
  color: var(--ink);
  text-decoration: none;
  font-size: 14px;
  font-weight: 650;
}
.main-nav a.active { color: var(--red); }
.account-link {
  min-width: 56px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.site-footer {
  flex-shrink: 0;
  margin: 0;
  padding: 14px 3.2vw;
  border-top: 1px solid var(--line);
  display: flex;
  align-items: baseline;
  gap: 16px 24px;
  flex-wrap: wrap;
  color: var(--muted);
  font-size: 12px;
  background: var(--paper);
}
.site-footer strong { color: var(--ink); font-size: 18px; }
.site-footer p { margin: 0; }
.discover-toggle { display: none; }
.city-dropdown { flex-shrink: 0; }
.discover-sheet { display: grid; gap: 12px; padding-bottom: 8px; }
.sheet-city, .sheet-search { width: 100%; max-width: none; }
.sheet-submit {
  height: 42px;
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--red);
  color: #fff;
  font-weight: 700;
  cursor: pointer;
}
@media (max-width: 820px) {
  .site-header { gap: 12px; padding: 0 18px; }
  .site-search, .city-dropdown { display: none; }
  .discover-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 38px;
    padding: 0 12px;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-pill);
    background: transparent;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .main-nav { gap: 14px; }
  .main-nav a { font-size: 13px; }
  .nav-home { display: none; }
  .site-footer { padding: 14px 18px; }
}
</style>
