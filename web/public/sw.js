/* 只缓存静态壳。API 与 SSE 必须走网络，手机不保留一份任务数据。 */
const CACHE = 'ingot-shell-v1'

self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting())
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  if (url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api/')) return
  if (event.request.method !== 'GET') return
  event.respondWith(
    fetch(event.request)
      .then((res) => {
        if (res.ok) {
          const copy = res.clone()
          void caches.open(CACHE).then((cache) => cache.put(event.request, copy))
        }
        return res
      })
      .catch(() => caches.match(event.request)),
  )
})

self.addEventListener('push', (event) => {
  let payload = { title: '待评审', body: '', task_id: 0 }
  try {
    if (event.data) payload = { ...payload, ...event.data.json() }
  } catch {
    payload.body = event.data ? event.data.text() : ''
  }
  event.waitUntil(
    self.registration.showNotification(payload.title, {
      body: payload.body,
      data: { task_id: payload.task_id },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const id = event.notification.data && event.notification.data.task_id
  const target = id ? '/?task=' + id : '/'
  event.waitUntil(self.clients.openWindow(target))
})
