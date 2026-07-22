import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(() => {
  // IT 栈默认 18080；本地 go run 可设 VITE_API_PROXY_TARGET=http://127.0.0.1:8080
  const apiProxyTarget = process.env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:18080'
  const apiProxy = {
    '/api': {
      target: apiProxyTarget,
      changeOrigin: true,
      ws: true,
    },
  }

  return {
    plugins: [vue()],
    server: {
      proxy: apiProxy,
    },
    // vite preview 不会继承 server.proxy，需单独配置，否则首页会报「暂时无法连接票务服务」
    preview: {
      proxy: apiProxy,
    },
  }
})
