import { computed, reactive, watch } from 'vue'

const STORAGE_CITY = 'gofun_city'
const LEGACY_STORAGE_CITY = 'fuchang_city'

const storedCity = localStorage.getItem(STORAGE_CITY) || localStorage.getItem(LEGACY_STORAGE_CITY) || ''
if (storedCity && !localStorage.getItem(STORAGE_CITY)) {
  localStorage.setItem(STORAGE_CITY, storedCity)
}

const state = reactive({
  city: storedCity,
  keyword: '',
  selectedCategory: '',
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
    () => !!(state.city || state.keyword || state.selectedCategory),
  )

  function setCity(city) {
    state.city = city || ''
  }

  function setKeyword(keyword) {
    state.keyword = (keyword || '').trim()
  }

  function toggleCategory(category) {
    state.selectedCategory = category || ''
  }

  function clearFilters() {
    state.keyword = ''
    state.selectedCategory = ''
  }

  function eventQuery(extra = {}) {
    const params = { page: 1, page_size: 12, ...extra }
    if (state.city) params.city = state.city
    if (state.selectedCategory) {
      params.category = state.selectedCategory
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
