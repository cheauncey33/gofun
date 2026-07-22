import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(() => {
  const apiProxyTarget = process.env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8080'

  return {
    plugins: [vue()],
    server: {
      proxy: {
        '/api': {
          target: apiProxyTarget,
          changeOrigin: true,
          ws: true,
        },
      },
    },
  }
})
