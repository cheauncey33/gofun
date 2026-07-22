import { computed, reactive, watch } from 'vue'

const STORAGE_CITY = 'fuchang_city'

const state = reactive({
  city: localStorage.getItem(STORAGE_CITY) || '',
  keyword: '',
  selectedCategories: [],
  cities: [],
  categories: [],
})

watch(
  () => state.city,
  (value) => {
    if (value) localStorage.setItem(STORAGE_CITY, value)
    else localStorage.removeItem(STORAGE_CITY)
  },
)

export function useDiscovery() {
  const hasActiveFilters = computed(
    () => !!(state.city || state.keyword || state.selectedCategories.length),
  )

  function setCity(city) {
    state.city = city || ''
  }

  function setKeyword(keyword) {
    state.keyword = (keyword || '').trim()
  }

  function toggleCategory(category) {
    if (!category) {
      state.selectedCategories = []
      return
    }
    const idx = state.selectedCategories.indexOf(category)
    if (idx >= 0) {
      state.selectedCategories.splice(idx, 1)
    } else {
      state.selectedCategories.push(category)
    }
  }

  function clearFilters() {
    state.keyword = ''
    state.selectedCategories = []
  }

  function eventQuery(extra = {}) {
    const params = { page: 1, page_size: 24, ...extra }
    if (state.city) params.city = state.city
    if (state.selectedCategories.length) {
      params.category = state.selectedCategories.join(',')
    }
    if (state.keyword) params.keyword = state.keyword
    return params
  }

  function applyMeta(meta = {}) {
    state.cities = meta.cities || []
    state.categories = meta.categories || []
  }

  return {
    state,
    hasActiveFilters,
    setCity,
    setKeyword,
    toggleCategory,
    clearFilters,
    eventQuery,
    applyMeta,
  }
}
