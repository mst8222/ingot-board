import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// 构建产物直接输出到 Go 内嵌目录；开发时 /api 代理到本机 ingot 服务。
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:18721',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../internal/webassets/dist',
    // 关闭 vite 自带的清空：它在带安全删除保护的环境里会卡住/半途失败，
    // 留下"index.html 引用了已消失 chunk"的产物目录，直接白屏。
    // 改由 clean-dist.mjs 用 fs.unlinkSync 逐个清理，见该文件注释。
    emptyOutDir: false,
    // 依赖拆包：React / 查询库迭代频率远低于业务代码，
    // 不拆开会让用户每次发版都重下几百 KB 几乎没变的代码。
    //
    // 用函数形式而非对象形式：对象形式（manualChunks: {react:[...]}）
    // 会把 react 整个打进名为 react 的 chunk，但 React 19 的
    // react/jsx-runtime 常被提升到入口，最后产出一个 0KB 的空 chunk，
    // 实际代码仍留在主包里 —— 拆包形同虚设。
    // 函数形式按模块 id 精确归属，不受提升影响。
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return undefined
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return 'react'
          if (id.includes('@tanstack')) return 'query'
          if (id.includes('@dnd-kit')) return 'dnd'
          // 其余依赖（radix / lucide / zustand 等）跟随主包，
          // 单独拆出来收益不大，反而多几个请求。
          return undefined
        },
      },
    },
  },
})
