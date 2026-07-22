<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Location, Search, User } from '@element-plus/icons-vue'
import api from '../api'
import { useDiscovery } from '../stores/discovery'

const route = useRoute()
const router = useRouter()
const token = computed(() => localStorage.getItem('access_token') || localStorage.getItem('token'))
const username = computed(() => localStorage.getItem('username') || '我的')
const overHero = ref(false)
const searchDraft = ref('')
const { state, setCity, setKeyword, applyMeta } = useDiscovery()

const cityLabel = computed(() => state.city || '全国')

function updateOverHero() {
  if (route.name !== 'Home') {
    overHero.value = false
    return
  }
  overHero.value = !document.documentElement.classList.contains('home-flipping')
    && window.scrollY < 24
}

function logout() {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('role')
  router.push('/')
}

function selectCity(city) {
  setCity(city)
  if (route.name !== 'Home') router.push('/')
  else document.getElementById('home-listings')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function submitSearch() {
  setKeyword(searchDraft.value)
  if (route.name !== 'Home') router.push('/')
  requestAnimationFrame(() => {
    document.getElementById('home-listings')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

watch(() => route.name, () => {
  updateOverHero()
}, { immediate: true })

watch(() => state.keyword, (value) => {
  if (searchDraft.value !== value) searchDraft.value = value
}, { immediate: true })

onMounted(async () => {
  window.addEventListener('scroll', updateOverHero, { passive: true })
  window.addEventListener('resize', updateOverHero)
  try {
    const res = await api.getCatalogMeta()
    applyMeta(res.data || {})
  } catch {
    // meta 失败不影响浏览；Home 仍可按当前筛选请求
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', updateOverHero)
  window.removeEventListener('resize', updateOverHero)
})
</script>

<template>
  <div class="site-shell" :class="{ 'is-home': route.name === 'Home' }">
    <header class="site-header" :class="{ 'over-hero': overHero, 'home-fixed': route.name === 'Home' }">
      <router-link class="brand" to="/" aria-label="赴场首页">赴场</router-link>
      <el-dropdown trigger="click" @command="selectCity">
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
        <router-link to="/" :class="{ active: route.name === 'Home' }">活动</router-link>
        <router-link to="/rush-sales" :class="{ active: route.name === 'RushSales' }">限时开售</router-link>
        <router-link to="/orders" :class="{ active: String(route.name).startsWith('Order') }">我的订单</router-link>
      </nav>
      <router-link v-if="!token" class="account-link" to="/login">登录</router-link>
      <el-dropdown v-else trigger="click">
        <button class="account-link account-button" type="button">
          <el-icon><User /></el-icon>{{ username }}
        </button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item @click="router.push('/orders')">我的订单</el-dropdown-item>
            <el-dropdown-item @click="router.push('/organizer')">主办方工作台</el-dropdown-item>
            <el-dropdown-item divided @click="logout">退出登录</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </header>

    <main>
      <router-view />
    </main>

    <footer class="site-footer">
      <div>
        <strong>赴场</strong>
        <p>多主办方活动票务平台</p>
      </div>
      <p>赴热爱之场，见想见的人。</p>
      <p>当前阶段 · 无选座电子票与在线核销</p>
    </footer>
  </div>
</template>

<style scoped>
.site-shell { min-height: 100vh; }
.site-shell.is-home .site-footer {
  margin-top: 28px;
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
.site-header.over-hero {
  border-bottom-color: transparent;
  background: linear-gradient(180deg, rgba(10, 7, 5, .55), rgba(10, 7, 5, 0));
  backdrop-filter: none;
  color: #f4ebe1;
}
.site-header.home-fixed {
  position: fixed;
  inset: 0 0 auto;
  width: 100%;
}
.site-header.over-hero .brand,
.site-header.over-hero .main-nav a,
.site-header.over-hero .account-link,
.site-header.over-hero .account-button {
  color: #f4ebe1;
}
.site-header.over-hero .main-nav a.active {
  color: #ef6d58;
}
.site-header.over-hero .city-button,
.site-header.over-hero .site-search {
  border-color: rgba(244, 235, 225, .35);
  color: rgba(244, 235, 225, .82);
  background: rgba(255, 255, 255, .06);
}
.site-header.over-hero .account-button {
  background: transparent;
}
.site-header.over-hero .site-search input {
  color: #f4ebe1;
}
.site-shell.is-home > main {
  padding-top: 0;
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
  width: min(330px, 25vw);
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
  margin: 54px 3.2vw 0;
  min-height: 120px;
  border-top: 1px solid var(--line);
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 30px;
  color: var(--muted);
  font-size: 12px;
}
.site-footer > :last-child { text-align: right; }
.site-footer strong { color: var(--ink); font-size: 20px; }
.site-footer p { margin: 5px 0; }
@media (max-width: 820px) {
  .site-header { gap: 12px; padding: 0 18px; }
  .site-search, .city-button { display: none; }
  .main-nav { gap: 14px; }
  .main-nav a { font-size: 13px; }
  .site-footer { grid-template-columns: 1fr; padding: 28px 0; }
  .site-footer > :last-child { text-align: left; }
}
</style>
