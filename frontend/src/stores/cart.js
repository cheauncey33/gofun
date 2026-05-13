import { reactive, computed } from 'vue'
import { imageUrl, productName } from '../utils/display.js'

const state = reactive({ items: [] })

export function useCart() {
  function addItem(product, quantity = 1) {
    const pid = Number(product.id) // IDs come as strings from API (json:"id,string"), convert to number
    const existing = state.items.find(i => i.id === pid)
    if (existing) {
      existing.quantity = Math.min(existing.quantity + quantity, product.stock)
    } else {
      state.items.push({
        id: pid,
        name: productName(product),
        price: product.price,
        stock: product.stock,
        image_url: imageUrl(product.image_url),
        quantity: Math.min(quantity, product.stock),
      })
    }
  }

  function removeItem(productId) {
    const idx = state.items.findIndex(i => i.id === productId)
    if (idx >= 0) state.items.splice(idx, 1)
  }

  function updateQuantity(productId, qty) {
    const item = state.items.find(i => i.id === productId)
    if (item && qty > 0 && qty <= item.stock) item.quantity = qty
  }

  function clear() { state.items = [] }

  const items = computed(() => state.items)
  const totalCount = computed(() => state.items.reduce((s, i) => s + i.quantity, 0))
  const totalAmount = computed(() => state.items.reduce((s, i) => s + i.price * i.quantity, 0))

  return { items, totalCount, totalAmount, addItem, removeItem, updateQuantity, clear }
}
