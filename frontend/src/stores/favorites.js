import { computed, reactive } from 'vue'
import api from '../api'

const state = reactive({
  loaded: false,
  items: [],
})

let loadPromise = null

function hasToken() {
  return Boolean(localStorage.getItem('access_token') || localStorage.getItem('token'))
}

function sameTarget(item, type, id) {
  return item.target_type === type && String(item.target_id) === String(id)
}

export function useFavorites() {
  const items = computed(() => state.items)

  function isFavorited(type, id) {
    return state.items.some(item => sameTarget(item, type, id))
  }

  function find(type, id) {
    return state.items.find(item => sameTarget(item, type, id))
  }

  async function reload() {
    if (loadPromise) return loadPromise
    loadPromise = (async () => {
      if (!hasToken()) {
        state.items = []
        state.loaded = true
        return
      }
      try {
        const res = await api.getFavorites()
        state.items = res.data || []
      } catch {
        state.items = []
      } finally {
        state.loaded = true
      }
    })().finally(() => {
      loadPromise = null
    })
    return loadPromise
  }

  async function ensureLoaded() {
    if (!state.loaded) await reload()
  }

  async function toggle(type, id) {
    if (!hasToken()) return { needLogin: true }
    await ensureLoaded()
    const current = find(type, id)
    if (current) {
      await api.removeFavorite(current.id)
      state.items = state.items.filter(item => item.id !== current.id)
      return { favorited: false }
    }
    const res = await api.addFavorite(type, id)
    if (res.data) state.items = [res.data, ...state.items]
    else await reload()
    return { favorited: true }
  }

  function clear() {
    resetFavoriteState()
  }

  return { items, loaded: computed(() => state.loaded), isFavorited, toggle, reload, ensureLoaded, clear }
}

export function resetFavoriteState() {
  state.items = []
  state.loaded = false
}
