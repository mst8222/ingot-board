import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import { makeQueryClient } from './store/query'
import './styles.css'

if ('serviceWorker' in navigator && window.isSecureContext) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js')
  })
}

const qc = makeQueryClient()

const root = document.getElementById('root')
if (!root) {
  // index.html 被改坏时给出明确信息，而不是 createRoot 抛一堆 null 错误
  throw new Error('#root 不存在，index.html 可能未正确加载')
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
